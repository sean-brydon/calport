package box

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

// Shapes captured from orca and herdr on a real box.
func TestParseProviderOutput(t *testing.T) {
	orca := []byte(`{"id":"f2db","ok":true,"result":{"worktree":{"id":"74d0::/home/alex/orca/workspaces/cal/calport-probe","path":"/home/alex/orca/workspaces/cal/calport-probe","branch":"refs/heads/alex/calport-probe"}}}`)
	if got, err := parseOrcaCreate(orca); err != nil || got != "/home/alex/orca/workspaces/cal/calport-probe" {
		t.Fatalf("orca path = %q, %v", got, err)
	}
	if _, err := parseOrcaCreate([]byte(`{"ok":false,"error":{"message":"repo not found"}}`)); err == nil {
		t.Fatal("orca failure accepted")
	}
	herdr := []byte(`{"id":"cli:worktree:create","result":{"root_pane":{"cwd":"/home/alex/work/cal-calport-herdr-probe","pane_id":"w5:p1"},"type":"worktree_created","workspace":{"workspace_id":"w5"}}}`)
	if got, err := parseHerdrCreate(herdr); err != nil || got != "/home/alex/work/cal-calport-herdr-probe" {
		t.Fatalf("herdr path = %q, %v", got, err)
	}
	if _, err := parseHerdrCreate([]byte(`not json`)); err == nil {
		t.Fatal("garbage herdr output accepted")
	}
}

func TestOrcaKnowsRepo(t *testing.T) {
	list := []byte(`{"ok":true,"result":{"repos":[{"id":"74d0","path": "/home/alex/work/cal","displayName":"cal"}]}}`)
	if !orcaKnowsRepo(list, "/home/alex/work/cal") {
		t.Fatal("a registered repo was not found")
	}
	if orcaKnowsRepo(list, "/home/alex/work/ca") || orcaKnowsRepo(list, "/home/alex/work/other") {
		t.Fatal("an unregistered repo was reported as known")
	}
}

func TestParseOrcaRepoPathsAndNames(t *testing.T) {
	paths, err := parseOrcaRepoPaths([]byte(`{"ok":true,"result":{"repos":[{"id":"1","path":"/home/alex/work/cal"},{"id":"2","path":"/home/alex/orca/projects/QaiTai App"}]}}`))
	if err != nil || len(paths) != 2 || paths[1] != "/home/alex/orca/projects/QaiTai App" {
		t.Fatalf("paths = %v, %v", paths, err)
	}
	if got := locationName("QaiTai App"); got != "qaitai-app" {
		t.Fatalf("locationName = %q", got)
	}
}

// Orca waits for a repository's setup hook before it answers when the repo
// says to, which takes minutes; the create must answer once the worktree
// exists and report the outcome later, and a caller leaving must not stop it.
func TestOrcaCreateAnswersBeforeSetupFinishes(t *testing.T) {
	repo := gitRepo(t)
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "finished")
	script := `#!/bin/sh
case "$1 $2" in
"repo list") echo '{"ok":true,"result":{"repos":[{"path":"` + repo + `"}]}}'; exit 0;;
esac
dir="` + repo + `-wt/calport-probe"
git -C "` + repo + `" worktree add -q -b calport-probe "$dir" >/dev/null 2>&1
sleep 3
touch "` + marker + `"
echo '{"ok":true,"result":{"worktree":{"path":"'"$dir"'"}}}'
`
	if err := os.WriteFile(filepath.Join(bin, "orca"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	settled := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	path, running, err := orcaCreate(ctx, Location{Name: "cal", Path: repo}, WorktreeRequest{
		Name:    "calport-probe",
		Settled: func(path string, err error) { settled <- fmt.Sprint(path, err) },
	})
	cancel() // the laptop's request ends here
	if err != nil || !running || filepath.Base(path) != "calport-probe" {
		t.Fatalf("orcaCreate = %q, %v, %v", path, running, err)
	}
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("answered after %v; it waited for setup", time.Since(start))
	}
	select {
	case got := <-settled:
		if got != path+"<nil>" {
			t.Fatalf("settled with %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the outcome was never reported")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("Orca was stopped when the caller left")
	}
}

func TestAddWorktreeThroughOrcaAnswersAndAnnouncesSetup(t *testing.T) {
	repo := gitRepo(t)
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
"repo list") echo '{"ok":true,"result":{"repos":[{"path":"` + repo + `"}]}}'; exit 0;;
esac
dir="` + repo + `-wt/calport-probe"
git -C "` + repo + `" worktree add -q -b calport-probe "$dir" >/dev/null 2>&1
sleep 2
echo '{"ok":true,"result":{"worktree":{"path":"'"$dir"'"}}}'
`
	os.WriteFile(filepath.Join(bin, "orca"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	b := &Box{Name: "devl", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}}
	if _, err := b.Locations.Add(context.Background(), "cal", repo); err != nil {
		t.Fatal(err)
	}
	ch, stop := b.Events.Subscribe()
	defer stop()

	req := httptest.NewRequest("POST", "/v1/locations/cal/worktrees", strings.NewReader(`{"name":"calport-probe","provider":"orca"}`))
	req.SetPathValue("name", "cal")
	rec := httptest.NewRecorder()
	if err := b.addWorktree(rec, req); err != nil {
		t.Fatal(err)
	}
	var wt Worktree
	json.Unmarshal(rec.Body.Bytes(), &wt)
	if !wt.SettingUp || filepath.Base(wt.Path) != "calport-probe" {
		t.Fatalf("reply = %s", rec.Body.String())
	}
	seen := map[string]bool{}
	timeout := time.After(10 * time.Second)
	for !seen["worktree.setup.finished"] {
		select {
		case e := <-ch:
			seen[e.Type] = true
			if e.Type == "worktree.setup.finished" && e.Data["path"] != wt.Path {
				t.Fatalf("finished event path = %v", e.Data["path"])
			}
		case <-timeout:
			t.Fatalf("events seen: %v", seen)
		}
	}
	if !seen["worktree.created"] || !seen["worktree.setup.started"] {
		t.Fatalf("events seen: %v", seen)
	}
}

func TestOrcaCreateTrustsTheWorktreeOverAnErrorAfterIt(t *testing.T) {
	repo := gitRepo(t)
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
"repo list") echo '{"ok":true,"result":{"repos":[{"path":"` + repo + `"}]}}'; exit 0;;
esac
git -C "` + repo + `" worktree add -q -b calport-probe "` + repo + `-wt/calport-probe" >/dev/null 2>&1
echo '{"ok":false,"error":{"code":"runtime_unavailable","message":"The Orca runtime closed the connection before responding."}}'
exit 1
`
	os.WriteFile(filepath.Join(bin, "orca"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	path, _, err := orcaCreate(context.Background(), Location{Name: "cal", Path: repo}, WorktreeRequest{Name: "calport-probe"})
	if err != nil || filepath.Base(path) != "calport-probe" {
		t.Fatalf("orcaCreate = %q, %v; the worktree exists, so it was created", path, err)
	}
}
