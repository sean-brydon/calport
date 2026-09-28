package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/user"
	"text/tabwriter"
	"time"

	"github.com/sean-brydon/calport/internal/network"
)

// candidate is a tailnet machine offered as a box.
type candidate struct {
	network.Peer
	// Box is the paired box at this address, if any.
	Box string `json:"box,omitempty"`
}

type discovery struct {
	// User is the SSH user to suggest: this computer's.
	User     string      `json:"user"`
	Machines []candidate `json:"machines"`
}

// discover handles `calport discover [--network NET] [--json]`: the machines
// on this computer's tailnet, or on one of calport's networks.
func discover(l laptop, args []string) error {
	var via string
	_, asJSON, err := flags("discover", args, func(fs *flag.FlagSet) {
		fs.StringVar(&via, "network", "", "list a calport network's machines instead of this computer's tailnet")
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var peers []network.Peer
	if via == "" {
		peers, err = network.SystemPeers(ctx)
	} else {
		c, aerr := ensureAgent(l)
		if err = aerr; err == nil {
			err = c.Call(ctx, "GET", "/v1/networks/"+url.PathEscape(via)+"/peers", nil, &peers)
		}
	}
	if err != nil {
		return err
	}
	paired := map[string]string{}
	if boxes, err := l.boxes().List(); err == nil {
		for _, b := range boxes {
			if host, _, err := net.SplitHostPort(b.Address); err == nil {
				paired[host] = b.Name
			}
		}
	}
	out := discovery{Machines: make([]candidate, len(peers))}
	if u, err := user.Current(); err == nil {
		out.User = u.Username
	}
	for i, p := range peers {
		out.Machines[i] = candidate{Peer: p, Box: paired[p.IP]}
	}
	if asJSON {
		return printJSON(out)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tIP\tOS\tSTATE")
	for _, m := range out.Machines {
		state := "offline"
		switch {
		case m.Box != "":
			state = "paired as " + m.Box
		case m.Online:
			state = "online: calport add ssh " + out.User + "@" + m.IP
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, m.IP, m.OS, state)
	}
	return w.Flush()
}
