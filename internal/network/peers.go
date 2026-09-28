package network

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"tailscale.com/ipn/ipnstate"
)

// Peer is another machine on a tailnet, as a candidate box.
type Peer struct {
	Name    string `json:"name"`
	DNSName string `json:"dns_name,omitempty"`
	IP      string `json:"ip"`
	OS      string `json:"os"`
	Online  bool   `json:"online"`
}

// boxOS reports whether calportd can run on a peer with this OS.
func boxOS(os string) bool { return os == "linux" || os == "macOS" }

// peersFrom lists the peers of st that could be boxes, online ones first.
func peersFrom(st *ipnstate.Status) []Peer {
	var out []Peer
	for _, p := range st.Peer {
		if !boxOS(p.OS) || len(p.TailscaleIPs) == 0 {
			continue
		}
		ip := p.TailscaleIPs[0]
		for _, a := range p.TailscaleIPs {
			if a.Is4() {
				ip = a
				break
			}
		}
		name := strings.SplitN(strings.TrimSuffix(p.DNSName, "."), ".", 2)[0]
		if name == "" {
			name = p.HostName
		}
		out = append(out, Peer{Name: name, DNSName: strings.TrimSuffix(p.DNSName, "."), IP: ip.String(), OS: p.OS, Online: p.Online})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Peers lists the machines on the named network.
func (m *Manager) Peers(ctx context.Context, name string) ([]Peer, error) {
	s, err := m.server(name)
	if err != nil {
		return nil, err
	}
	if err := m.waitRunning(ctx, name); err != nil {
		return nil, err
	}
	lc, err := s.LocalClient()
	if err != nil {
		return nil, err
	}
	// A node that just started reports Running before its peers arrive.
	for {
		st, err := lc.Status(ctx)
		if err != nil {
			return nil, err
		}
		if st.BackendState != "Running" {
			return nil, ErrNeedsLogin
		}
		if len(st.Peer) > 0 || !m.warmingUp(name) {
			return peersFrom(st), nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// systemTailscale finds the Tailscale CLI this computer's own tailnet uses.
func systemTailscale() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// SystemPeers lists the machines on the tailnet this computer is joined to
// through the Tailscale app, if it is.
func SystemPeers(ctx context.Context) ([]Peer, error) {
	cli := systemTailscale()
	if cli == "" {
		return nil, errors.New("Tailscale is not installed on this computer")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "status", "--json").Output()
	if err != nil {
		return nil, errors.New("Tailscale on this computer is not connected")
	}
	var st ipnstate.Status
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	if st.BackendState != "Running" {
		return nil, errors.New("Tailscale on this computer is not connected")
	}
	return peersFrom(&st), nil
}
