package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

// fakeOrca points Orca's settings at a file shaped like the real one.
func fakeOrca(t *testing.T, repo, setup, archive string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "orca-data.json")
	os.WriteFile(file, []byte(`{"schemaVersion":1,"repos":[{"id":"1","path":"`+repo+`","hookSettings":{"setupRunPolicy":"run-by-default","scripts":{"setup":"`+setup+`","archive":"`+archive+`"}}},{"id":"2","path":"/elsewhere"}]}`), 0o600)
	old := orcaDataFiles
	orcaDataFiles = func() []string { return []string{file} }
	t.Cleanup(func() { orcaDataFiles = old })
}

func TestScriptsComeFromOrcaUnlessTheLocationSetsItsOwn(t *testing.T) {
	repo := gitRepo(t)
	fakeOrca(t, repo, "echo orca-setup", "echo orca-archive")
	ctx := context.Background()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	loc, err := l.Add(ctx, "cal", repo)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scripts != (Scripts{Setup: "echo orca-setup", Archive: "echo orca-archive", From: "orca"}) {
		t.Fatalf("scripts = %+v, want Orca's", loc.Scripts)
	}
	if err := l.SetScripts("cal", "echo own", ""); err != nil {
		t.Fatal(err)
	}
	loc, _ = l.Get(ctx, "cal")
	if loc.Scripts != (Scripts{Setup: "echo own", From: "calport"}) {
		t.Fatalf("scripts = %+v, want the location's own", loc.Scripts)
	}
	l.SetScripts("cal", "", "")
	loc, _ = l.Get(ctx, "cal")
	if loc.Scripts.From != "orca" {
		t.Fatalf("clearing did not fall back to Orca: %+v", loc.Scripts)
	}
}

func waitFor(t *testing.T, ch <-chan events.Event, typ string) events.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == typ {
				return e
			}
		case <-deadline:
			t.Fatalf("never saw %s", typ)
		}
	}
}

func TestSetupRunsInTheNewWorktreeWithOrcaCompatibleEnvironment(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	fakeOrca(t, repo, `echo \"$ORCA_ROOT_PATH|$ORCA_WORKTREE_PATH|$ORCA_WORKSPACE_NAME\" > setup-ran`, "")
	ctx := context.Background()
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: bus, LogDir: t.TempDir()}
	loc, _ := b.Locations.Add(ctx, "cal", repo)
	wt, err := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	go b.lifecycle("calport", "setup", loc, wt.Path, wt.Name, loc.Scripts.Setup, nil)
	waitFor(t, ch, "worktree.setup.finished")
	got, err := os.ReadFile(filepath.Join(wt.Path, "setup-ran"))
	if err != nil {
		t.Fatal(err)
	}
	if want := repo + "|" + wt.Path + "|billing"; strings.TrimSpace(string(got)) != want {
		t.Fatalf("setup saw %q, want %q", got, want)
	}
}

func TestArchiveRunsBeforeRemovalAndAFailureKeepsTheWorktree(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	ctx := context.Background()
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: bus, LogDir: t.TempDir()}
	loc, _ := b.Locations.Add(ctx, "cal", repo)
	wt, _ := b.Locations.CreateWorktree(ctx, "cal", "billing", "", "")

	b.lifecycle("calport", "archive", loc, wt.Path, wt.Name, "exit 3", func() error {
		return b.Locations.RemoveWorktree(ctx, "cal", "billing", true)
	})
	failed := waitFor(t, ch, "worktree.archive.failed")
	if !strings.Contains(failed.Error, "log:") {
		t.Fatalf("failure does not point at the log: %q", failed.Error)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("a failed archive still removed the worktree")
	}

	b.lifecycle("calport", "archive", loc, wt.Path, wt.Name, "true", func() error {
		return b.Locations.RemoveWorktree(ctx, "cal", "billing", true)
	})
	waitFor(t, ch, "worktree.archive.finished")
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("a successful archive did not remove the worktree")
	}
}
