package box

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseWorktrees(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "cal")
	billing := filepath.Join(dir, "cal-billing")
	other := filepath.Join(dir, "tailmux-smoke")
	for _, d := range []string{repo, billing, other} {
		os.MkdirAll(d, 0o755)
	}
	out := []byte("worktree " + repo + "\nHEAD 5fdc8af8dd0123456789\nbranch refs/heads/main\n\n" +
		"worktree " + billing + "\nHEAD 62d41c1302abcdef\nbranch refs/heads/billing/4-customer-credit\n\n" +
		"worktree " + other + "\nHEAD 0d0ca9d73f000000\ndetached\n\n" +
		"worktree /tmp/gone-for-good\nHEAD 2301306497000000\ndetached\nprunable gitdir file points to non-existent location\n\n")
	got := parseWorktrees(out, repo)
	want := []Worktree{
		{Name: "cal", Path: repo, Branch: "main", Head: "5fdc8af8dd", Main: true},
		{Name: "billing", Path: billing, Branch: "billing/4-customer-credit", Head: "62d41c1302"},
		{Name: "tailmux-smoke", Path: other, Head: "0d0ca9d73f"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d worktrees, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("worktree %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "cal")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README"), []byte("hi"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return repo
}

func TestLocationsWithWorktreesEndToEnd(t *testing.T) {
	repo := gitRepo(t)
	ctx := context.Background()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	loc, err := l.Add(ctx, "cal", repo)
	if err != nil {
		t.Fatal(err)
	}
	if !loc.Repo || len(loc.Worktrees) != 1 || !loc.Worktrees[0].Main {
		t.Fatalf("new repo location = %+v", loc)
	}
	wt, err := l.CreateWorktree(ctx, "cal", "billing", "alex/billing", "main")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(filepath.Dir(repo), "cal-billing")
	if wt.Name != "billing" || wt.Branch != "alex/billing" || wt.Path != wantPath {
		t.Fatalf("worktree = %+v, want billing on alex/billing at %s", wt, wantPath)
	}
	if dir, err := l.Dir(ctx, "cal/billing"); err != nil || dir != wantPath {
		t.Fatalf("Dir(cal/billing) = %q, %v", dir, err)
	}
	if dir, err := l.Dir(ctx, "cal"); err != nil || dir != repo {
		t.Fatalf("Dir(cal) = %q, %v", dir, err)
	}
	// Uncommitted work is protected unless the caller insists.
	os.WriteFile(filepath.Join(wantPath, "wip.txt"), []byte("unsaved"), 0o644)
	if err := l.RemoveWorktree(ctx, "cal", "billing", false); err == nil {
		t.Fatal("removed a worktree with uncommitted changes")
	}
	if err := l.RemoveWorktree(ctx, "cal", "billing", true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Dir(ctx, "cal/billing"); !errors.Is(err, ErrUnknownWorktree) {
		t.Fatalf("removed worktree still resolves: %v", err)
	}
	if err := l.RemoveWorktree(ctx, "cal", "cal", true); err == nil {
		t.Fatal("removed the main checkout")
	}
}

func TestPlainDirectoriesAreLocationsToo(t *testing.T) {
	dir := t.TempDir()
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	loc, err := l.Add(context.Background(), "scratch", dir)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Repo || len(loc.Worktrees) != 0 {
		t.Fatalf("plain directory reported as a repo: %+v", loc)
	}
	if _, err := l.CreateWorktree(context.Background(), "scratch", "x", "", ""); err == nil {
		t.Fatal("created a worktree in a non-repository")
	}
}

func TestAddRejectsMissingPathsAndBadNames(t *testing.T) {
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	if _, err := l.Add(context.Background(), "gone", "/definitely/not/here"); err == nil {
		t.Fatal("added a missing directory")
	}
	if _, err := l.Add(context.Background(), "bad name", t.TempDir()); err == nil {
		t.Fatal("added an invalid name")
	}
	if err := l.Remove("nope"); !errors.Is(err, ErrUnknownLocation) {
		t.Fatalf("removing unknown: %v", err)
	}
}
