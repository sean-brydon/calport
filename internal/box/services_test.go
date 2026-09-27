package box

import "testing"

func TestServicesBelongToTheDeepestWorktreeContainingTheProcess(t *testing.T) {
	locations := []Location{
		{Name: "cal", Path: "/w/cal", Repo: true, Worktrees: []Worktree{
			{Name: "cal", Path: "/w/cal", Main: true},
			{Name: "billing", Path: "/w/cal-billing"},
			{Name: "fix", Path: "/w/orca/cal/fix"},
		}},
		{Name: "scratch", Path: "/w/scratch"},
	}
	ports := []Port{
		{Port: 3000, Dir: "/w/cal/apps/web", Command: "next dev"},
		{Port: 3100, Dir: "/w/cal-billing/apps/web"},
		{Port: 3200, Dir: "/w/orca/cal/fix"},
		{Port: 4000, Dir: "/w/scratch/tool"},
		{Port: 5432, Dir: "/var/lib/postgresql"},
		{Port: 6000},
	}
	got := Services(ports, locations)
	want := []Service{
		{Location: "cal", Worktree: "cal", Path: "/w/cal", Port: 3000, Process: "next dev", Main: true},
		{Location: "cal", Worktree: "billing", Path: "/w/cal-billing", Port: 3100},
		{Location: "cal", Worktree: "fix", Path: "/w/orca/cal/fix", Port: 3200},
		{Location: "scratch", Worktree: "scratch", Path: "/w/scratch", Port: 4000, Main: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("service %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAPrefixThatIsNotAPathBoundaryDoesNotMatch(t *testing.T) {
	// /w/cal must not claim /w/cal-billing just because the string matches.
	got := Services([]Port{{Port: 3100, Dir: "/w/cal-billing"}}, []Location{{Name: "cal", Path: "/w/cal", Repo: true, Worktrees: []Worktree{{Name: "cal", Path: "/w/cal", Main: true}}}})
	if len(got) != 0 {
		t.Fatalf("a sibling directory was claimed: %+v", got)
	}
}

func TestOpenInRejectsUnknownToolsAndAgents(t *testing.T) {
	if err := OpenIn(t.Context(), OpenRequest{Tool: "vim"}, "/r", "/r", "x"); err == nil {
		t.Fatal("unknown tool accepted")
	}
	if err := OpenIn(t.Context(), OpenRequest{Tool: "orca", Agent: "rm -rf"}, "/r", "/r", "x"); err == nil {
		t.Fatal("arbitrary agent command accepted")
	}
}
