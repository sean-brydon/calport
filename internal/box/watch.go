package box

import (
	"context"
	"sync"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

// Watcher notices worktrees that appear in or vanish from a location without
// going through calport: made by Orca, Herdr, an agent, or plain git. That
// makes every tool's worktrees visible to hooks and the laptop, not only the
// ones calport created.
type Watcher struct {
	Locations *Locations
	Events    *events.Bus
	Box       string
	Interval  time.Duration

	mu    sync.Mutex
	known map[string]map[string]Worktree // location → path → worktree
	own   map[string]time.Time           // paths calport itself just changed
}

// Own records that calport changed path itself and has already announced it,
// so the next scan does not announce it a second time.
func (w *Watcher) Own(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.own == nil {
		w.own = map[string]time.Time{}
	}
	w.own[path] = time.Now()
}

func (w *Watcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval == 0 {
		interval = 10 * time.Second
	}
	w.Scan(ctx, false)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Scan(ctx, true)
		}
	}
}

// Scan compares each location's worktrees with the previous scan and, when
// announce is set, publishes the differences.
func (w *Watcher) Scan(ctx context.Context, announce bool) {
	locs, err := w.Locations.List(ctx)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.known == nil {
		w.known = map[string]map[string]Worktree{}
	}
	seen := map[string]bool{}
	for _, loc := range locs {
		if !loc.Repo {
			continue
		}
		seen[loc.Name] = true
		now := map[string]Worktree{}
		for _, wt := range loc.Worktrees {
			now[wt.Path] = wt
		}
		before, tracked := w.known[loc.Name]
		w.known[loc.Name] = now
		// A newly registered location is a baseline, not a burst of news.
		if !announce || !tracked {
			continue
		}
		for path, wt := range now {
			if _, ok := before[path]; !ok && !w.claim(path) {
				w.publish("worktree.created", loc.Name, wt)
			}
		}
		for path, wt := range before {
			if _, ok := now[path]; !ok && !w.claim(path) {
				w.publish("worktree.removed", loc.Name, wt)
			}
		}
	}
	for name := range w.known {
		if !seen[name] {
			delete(w.known, name)
		}
	}
	for path, at := range w.own {
		if time.Since(at) > time.Minute {
			delete(w.own, path)
		}
	}
}

// claim reports whether calport itself made this change; w.mu is held.
func (w *Watcher) claim(path string) bool {
	if _, ok := w.own[path]; ok {
		delete(w.own, path)
		return true
	}
	return false
}

func (w *Watcher) publish(typ, location string, wt Worktree) {
	w.Events.Publish(events.Event{
		Type:   typ,
		Box:    w.Box,
		Origin: "detected",
		Data:   map[string]any{"location": location, "name": wt.Name, "path": wt.Path, "branch": wt.Branch},
	})
}
