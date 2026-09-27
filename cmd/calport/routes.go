package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/sean-brydon/calport/internal/agent"
)

// routeCommand handles `calport route add|rm` and `calport routes`.
func routeCommand(l laptop, args []string) error {
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	switch {
	case len(args) == 4 && args[0] == "add":
		p, err := port(args[3])
		if err != nil {
			return err
		}
		r := agent.Route{Pattern: args[1], Box: args[2], Port: p}
		if err := c.Call(context.Background(), "POST", "/v1/routes", r, nil); err != nil {
			return err
		}
		fmt.Printf("%s → %s:%d (hostnames passed through unchanged)\n", r.Pattern, r.Box, r.Port)
		return nil
	case len(args) == 2 && args[0] == "rm":
		if err := c.Call(context.Background(), "DELETE", "/v1/routes?pattern="+url.QueryEscape(args[1]), nil, nil); err != nil {
			return err
		}
		fmt.Printf("Removed route %s\n", args[1])
		return nil
	}
	return errors.New("usage: calport route add '*.name.localhost' BOX PORT | calport route rm '*.name.localhost'")
}

func listRoutes(l laptop, args []string) error {
	_, asJSON, err := flags("routes", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s.Routes)
	}
	if len(s.Routes) == 0 {
		fmt.Println("No routes. Send a whole hostname pattern to a box with: calport route add '*.name.localhost' BOX PORT")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATTERN\tBOX\tPORT")
	for _, r := range s.Routes {
		fmt.Fprintf(w, "%s\t%s\t%d\n", r.Pattern, r.Box, r.Port)
	}
	return w.Flush()
}
