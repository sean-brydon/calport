package hooks

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

func TestMatches(t *testing.T) {
	created := events.Event{Type: "worktree.created", Origin: "calport"}
	fromOrca := events.Event{Type: "worktree.created", Origin: "orca"}
	for _, tc := range []struct {
		hook Hook
		e    events.Event
		want bool
	}{
		{Hook{On: "worktree.created", Run: "x"}, created, true},
		{Hook{On: "worktree.*", Run: "x"}, created, true},
		{Hook{On: "*", Run: "x"}, created, true},
		{Hook{On: "session.*", Run: "x"}, created, false},
		{Hook{On: "worktree", Run: "x"}, created, false},
		{Hook{On: "worktree.created"}, created, false},
		// The loop guard: a hook never reacts to its own tool's changes.
		{Hook{On: "worktree.created", Run: "x", Tool: "orca"}, fromOrca, false},
		{Hook{On: "worktree.created", Run: "x", Tool: "orca"}, created, true},
		{Hook{On: "worktree.created", Run: "x", Tool: "herdr"}, fromOrca, true},
	} {
		if got := Matches(tc.hook, tc.e); got != tc.want {
			t.Errorf("Matches(%+v, %s from %s) = %v, want %v", tc.hook, tc.e.Type, tc.e.Origin, got, tc.want)
		}
	}
}

func TestEnvDescribesTheEventAndStampsTheOrigin(t *testing.T) {
	e := events.Event{Type: "worktree.created", Box: "devl", Origin: "calport", Data: map[string]any{"path": "/home/alex/work/cal-x", "location": "cal", "first-port": 3000}}
	env := Env(e, "orca")
	for _, want := range []string{
		"CALPORT_EVENT=worktree.created", "CALPORT_EVENT_BOX=devl", "CALPORT_EVENT_ORIGIN=calport",
		"CALPORT_ORIGIN=orca", "CALPORT_PATH=/home/alex/work/cal-x", "CALPORT_LOCATION=cal", "CALPORT_FIRST_PORT=3000",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("env missing %s: %v", want, env)
		}
	}
	if !slices.Contains(Env(e, ""), "CALPORT_ORIGIN=hook") {
		t.Error("a hook without a tool must still stamp an origin")
	}
}

func TestRunnerRunsMatchingHooksWithTheEventOnStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	cfg := Config{Hooks: []Hook{
		{On: "worktree.*", Run: "cat > " + out + "; echo \" $CALPORT_ORIGIN\" >> " + out, Tool: "herdr"},
		{On: "session.started", Run: "echo should-not-run >> " + out},
	}}
	b, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "hooks.json")
	os.WriteFile(path, b, 0o600)

	var bus events.Bus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runner{Path: path, Log: log.New(io.Discard, "", 0)}
	go r.Run(ctx, &bus)
	time.Sleep(50 * time.Millisecond)
	bus.Publish(events.Event{Type: "worktree.created", Box: "devl", Data: map[string]any{"name": "billing"}})

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := os.ReadFile(out)
		if strings.Contains(string(got), `"name":"billing"`) && strings.Contains(string(got), " herdr") {
			if strings.Contains(string(got), "should-not-run") {
				t.Fatal("a non-matching hook ran")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hook output = %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestABrokenConfigRunsNothingAndDoesNotCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	r := &Runner{Path: path, Log: log.New(io.Discard, "", 0)}
	r.handle(context.Background(), events.Event{Type: "worktree.created"})
}
