package box

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sean-brydon/calport/internal/kit"
)

func TestCalportSkipsTheKitHooksATool(t *testing.T) {
	repo := t.TempDir()
	b := &Box{Kit: &kit.Installer{Home: t.TempDir()}}
	ctx := context.Background()
	kitScripts := Location{Path: repo, Scripts: Scripts{Setup: kit.SetupHook, Archive: kit.ArchiveHook, From: "calport"}}

	if b.toolRunsKitHooks(ctx, "orca", kitScripts) {
		t.Error("with no orca.yaml, calport must run the hooks for Orca worktrees")
	}
	if !b.toolRunsKitHooks(ctx, "orca", Location{Path: repo, Scripts: Scripts{Setup: "x", From: "orca"}}) {
		t.Error("Orca runs the hooks from its own settings")
	}
	if err := os.WriteFile(filepath.Join(repo, "orca.yaml"), []byte("scripts:\n  setup: '"+kit.SetupHook+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !b.toolRunsKitHooks(ctx, "orca", kitScripts) {
		t.Error("an orca.yaml that runs cal-worktree means Orca runs the hooks")
	}
	if b.toolRunsKitHooks(ctx, "git", kitScripts) {
		t.Error("git worktrees always get calport's hooks")
	}
	custom := Location{Path: repo, Scripts: Scripts{Setup: "make setup", From: "calport"}}
	if b.toolRunsKitHooks(ctx, "orca", custom) {
		t.Error("a location with its own scripts still runs them")
	}
}
