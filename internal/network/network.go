// Package network lets the laptop reach boxes on tailnets it is not joined
// to, such as a personal tailnet while the system Tailscale is on a work
// one. Each named network is an embedded Tailscale node owned by the agent,
// logged in once with a browser. calport's own pinned TLS still runs on top:
// the tailnet only provides the route.
package network

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sean-brydon/calport/internal/trust"
	"tailscale.com/tsnet"
)

// Info describes a network for listings.
type Info struct {
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Tailnet string   `json:"tailnet,omitempty"`
	IPs     []string `json:"ips,omitempty"`
}

var ErrNeedsLogin = errors.New("network needs a login; run: calport network login")

type Manager struct {
	// Dir holds one state directory per network.
	Dir string
	Log *log.Logger

	mu      sync.Mutex
	servers map[string]*tsnet.Server
	started map[string]time.Time
}

func (m *Manager) stateDir(name string) string { return filepath.Join(m.Dir, name) }

// server starts (or returns) the node for name without logging in.
func (m *Manager) server(name string) (*tsnet.Server, error) {
	if !trust.ValidName(name) {
		return nil, fmt.Errorf("invalid network name %q", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.servers[name]; s != nil {
		return s, nil
	}
	dir := m.stateDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &tsnet.Server{
		Dir:      dir,
		Hostname: "calport-" + name,
		Logf:     func(string, ...any) {},
	}
	if m.Log != nil {
		s.UserLogf = m.Log.Printf
	} else {
		s.UserLogf = func(string, ...any) {}
	}
	if err := s.Start(); err != nil {
		return nil, err
	}
	if m.servers == nil {
		m.servers = map[string]*tsnet.Server{}
		m.started = map[string]time.Time{}
	}
	m.servers[name] = s
	m.started[name] = time.Now()
	return s, nil
}

// Known reports the networks that have state on disk.
func (m *Manager) Known() []string {
	entries, _ := os.ReadDir(m.Dir)
	var names []string
	for _, e := range entries {
		if e.IsDir() && trust.ValidName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Login brings the network up, reporting the sign-in URL through onURL as
// soon as Tailscale asks for one. It returns once the node is connected.
func (m *Manager) Login(ctx context.Context, name string, onURL func(string)) (Info, error) {
	s, err := m.server(name)
	if err != nil {
		return Info{}, err
	}
	lc, err := s.LocalClient()
	if err != nil {
		return Info{}, err
	}
	done := make(chan error, 1)
	go func() { _, err := s.Up(ctx); done <- err }()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	shown := ""
	for {
		select {
		case err := <-done:
			if err != nil {
				return Info{}, err
			}
			return m.info(ctx, name)
		case <-tick.C:
			if st, err := lc.Status(ctx); err == nil && st.AuthURL != "" && st.AuthURL != shown {
				shown = st.AuthURL
				onURL(shown)
			}
		case <-ctx.Done():
			return Info{}, ctx.Err()
		}
	}
}

func (m *Manager) info(ctx context.Context, name string) (Info, error) {
	s, err := m.server(name)
	if err != nil {
		return Info{}, err
	}
	lc, err := s.LocalClient()
	if err != nil {
		return Info{}, err
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return Info{}, err
	}
	i := Info{Name: name, State: st.BackendState}
	if st.CurrentTailnet != nil {
		i.Tailnet = st.CurrentTailnet.Name
	}
	for _, ip := range st.TailscaleIPs {
		i.IPs = append(i.IPs, ip.String())
	}
	return i, nil
}

// List describes every known network. Networks are started on demand, so
// listing one also brings it back after the agent restarts.
func (m *Manager) List(ctx context.Context) []Info {
	var out []Info
	for _, name := range m.Known() {
		i, err := m.info(ctx, name)
		if err != nil {
			i = Info{Name: name, State: "error: " + err.Error()}
		}
		out = append(out, i)
	}
	return out
}

// Dial connects to addr through the named network.
func (m *Manager) Dial(ctx context.Context, name, addr string) (net.Conn, error) {
	s, err := m.server(name)
	if err != nil {
		return nil, err
	}
	// A node that is still starting (just after the agent starts or the
	// laptop wakes) is worth a short wait; one that needs a login is not.
	deadline := time.Now().Add(20 * time.Second)
	for {
		i, err := m.info(ctx, name)
		if err == nil && i.State == "Running" {
			break
		}
		if err == nil && i.State == "NeedsLogin" {
			return nil, fmt.Errorf("%w %s", ErrNeedsLogin, name)
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	// A freshly started node reports Running a few seconds before it has a
	// path to its peers, so early failures are retried; later ones are real.
	for {
		conn, err := s.Dial(ctx, "tcp", addr)
		if err == nil || ctx.Err() != nil || !m.warmingUp(name) {
			return conn, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *Manager) warmingUp(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Since(m.started[name]) < 30*time.Second
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		s.Close()
	}
	m.servers = nil
}
