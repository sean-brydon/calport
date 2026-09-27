package box

import "testing"

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
