package agent

import (
	"context"
	"time"

	"github.com/sean-brydon/calport/internal/box"
)

// serviceTTL bounds how stale the map of servers to worktrees may be; a dev
// server that just started shows up within this time.
const serviceTTL = 10 * time.Second

type serviceCache struct {
	at   time.Time
	list []box.Service
}

// services returns which worktree each listening server on a box belongs to.
func (a *Agent) services(ctx context.Context, name string) []box.Service {
	a.mu.Lock()
	cached, fresh := a.svc[name]
	st := a.clients[name]
	a.mu.Unlock()
	if fresh && time.Since(cached.at) < serviceTTL {
		return cached.list
	}
	if st == nil || st.status.State != StateOnline {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, err := box.NewClient(st.client).Services(ctx)
	if err != nil {
		return cached.list
	}
	a.mu.Lock()
	if a.svc == nil {
		a.svc = map[string]serviceCache{}
	}
	a.svc[name] = serviceCache{at: time.Now(), list: list}
	a.mu.Unlock()
	return list
}

// worktree resolves host labels to a worktree's dev server, its lowest port:
//
//	[worktree, location, box]  feat-billing.cal.devl.localhost
//	[location, box]            cal.devl.localhost (the main checkout)
//	[worktree, location]       feat-billing.cal.localhost, when one box has it
func (a *Agent) worktree(labels []string) (string, int, bool) {
	ctx := context.Background()
	lowest := func(boxName string, match func(box.Service) bool) (int, bool) {
		port := 0
		for _, s := range a.services(ctx, boxName) {
			if match(s) && (port == 0 || s.Port < port) {
				port = s.Port
			}
		}
		return port, port != 0
	}
	switch len(labels) {
	case 3:
		wt, loc, boxName := labels[0], labels[1], labels[2]
		port, ok := lowest(boxName, func(s box.Service) bool { return s.Location == loc && s.Worktree == wt })
		return boxName, port, ok
	case 2:
		if _, ok := a.client(labels[1]); ok {
			loc, boxName := labels[0], labels[1]
			port, ok := lowest(boxName, func(s box.Service) bool { return s.Location == loc && s.Main })
			return boxName, port, ok
		}
		wt, loc := labels[0], labels[1]
		found, foundPort := "", 0
		for _, b := range a.status().Boxes {
			if port, ok := lowest(b.Name, func(s box.Service) bool { return s.Location == loc && s.Worktree == wt }); ok {
				if found != "" {
					return "", 0, false // ambiguous: name the box
				}
				found, foundPort = b.Name, port
			}
		}
		return found, foundPort, found != ""
	}
	return "", 0, false
}
