package box

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

func drain(ch <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-time.After(100 * time.Millisecond):
			return out
		}
	}
}

func TestWatcherAnnouncesWorktreesMadeOutsideCalportOnce(t *testing.T) {
	repo := gitRepo(t)
	ctx := context.Background()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	if _, err := l.Add(ctx, "cal", repo); err != nil {
		t.Fatal(err)
	}
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	w := &Watcher{Locations: l, Events: bus, Box: "devl"}
	w.Scan(ctx, false)

	// Another tool (here, plain git) makes a worktree.
	outside := filepath.Join(filepath.Dir(repo), "cal-from-orca")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", "from-orca", outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %s", out)
	}
	w.Scan(ctx, true)
	got := drain(ch)
	if len(got) != 1 || got[0].Type != "worktree.created" || got[0].Origin != "detected" || got[0].Data["path"] != outside || got[0].Box != "devl" {
		t.Fatalf("after an outside worktree appeared: %+v", got)
	}

	// calport's own worktree was already announced by the API.
	wt, err := l.CreateWorktree(ctx, "cal", "own", "", "")
	if err != nil {
		t.Fatal(err)
	}
	w.Own(wt.Path)
	w.Scan(ctx, true)
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("calport's own worktree was announced again: %+v", got)
	}

	if out, err := exec.Command("git", "-C", repo, "worktree", "remove", outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %s", out)
	}
	w.Scan(ctx, true)
	got = drain(ch)
	if len(got) != 1 || got[0].Type != "worktree.removed" || got[0].Data["path"] != outside {
		t.Fatalf("after the outside worktree was removed: %+v", got)
	}
	w.Scan(ctx, true)
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("a quiet scan announced %+v", got)
	}
}

func TestANewlyAddedLocationIsABaselineNotNews(t *testing.T) {
	repo := gitRepo(t)
	ctx := context.Background()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	w := &Watcher{Locations: l, Events: bus}
	w.Scan(ctx, false)
	if _, err := l.Add(ctx, "cal", repo); err != nil {
		t.Fatal(err)
	}
	w.Scan(ctx, true)
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("registering a location announced its existing worktrees: %+v", got)
	}
}
