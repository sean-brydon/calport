package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintGroupsByAreaAndShowsFixesOnlyForProblems(t *testing.T) {
	var buf bytes.Buffer
	problems := Print(&buf, []Check{
		{Area: "Daemon", Name: "calportd service", Status: OK, Detail: "active", Fix: "never shown"},
		{Area: "Daemon", Name: "listen address", Status: Warn, Detail: "0.0.0.0:7443 is public", Fix: "calportd install"},
		{Area: "Tools", Name: "tmux", Status: Fail, Detail: "not installed", Fix: "sudo apt install tmux"},
		{Area: "Tools", Name: "herdr", Status: Info, Detail: "not installed", Fix: "optional"},
	})
	out := buf.String()
	if problems != 2 {
		t.Fatalf("problems = %d, want 2 (info is not a problem)", problems)
	}
	for _, want := range []string{"Daemon\n", "  ✓ calportd service  active\n", "  ! listen address", "      → calportd install\n", "\nTools\n", "  ✗ tmux", "  · herdr"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "never shown") {
		t.Fatal("a fix was shown for a passing check")
	}
}

func TestToolCheckMarksMissingRequiredToolsAsFailures(t *testing.T) {
	if c := ToolCheck("Tools", "sh", "shells", "", true); c.Status != OK {
		t.Fatalf("sh: %+v", c)
	}
	missing := ToolCheck("Tools", "definitely-not-a-real-tool-xyz", "testing", "install it", true)
	if missing.Status != Fail || missing.Fix != "install it" {
		t.Fatalf("missing required tool: %+v", missing)
	}
	if c := ToolCheck("Tools", "definitely-not-a-real-tool-xyz", "testing", "", false); c.Status != Info {
		t.Fatalf("missing optional tool: %+v", c)
	}
}

func TestToolFindsBinariesOutsidePath(t *testing.T) {
	brew := t.TempDir()
	if err := os.WriteFile(filepath.Join(brew, "faketool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDirs := extraToolDirs
	extraToolDirs = []string{brew}
	t.Cleanup(func() { extraToolDirs = oldDirs })
	t.Setenv("PATH", t.TempDir())

	path, ok := Tool("faketool")
	if !ok || path != filepath.Join(brew, "faketool") {
		t.Fatalf("Tool(faketool) = %q, %v; want the copy in the extra directory", path, ok)
	}
}

func TestToolReportsMissingToolAsAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	oldDirs := extraToolDirs
	extraToolDirs = []string{t.TempDir()}
	t.Cleanup(func() { extraToolDirs = oldDirs })

	if path, ok := Tool("definitely-not-installed"); ok {
		t.Fatalf("Tool = %q, true; want absent", path)
	}
}

func TestToolFindsAnNpmGlobalUnderAVersionManager(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	// Both take precedence by design, so a real manager on this machine would
	// otherwise answer for the fake home.
	t.Setenv("FNM_DIR", filepath.Join(home, ".local", "share", "fnm"))
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	oldDirs := extraToolDirs
	extraToolDirs = []string{t.TempDir()}
	t.Cleanup(func() { extraToolDirs = oldDirs })

	// fnm's default alias, which is what the box actually uses.
	fnmBin := filepath.Join(home, ".local", "share", "fnm", "aliases", "default", "bin")
	if err := os.MkdirAll(fnmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fnmBin, "paseo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if path, ok := Tool("paseo"); !ok || path != filepath.Join(fnmBin, "paseo") {
		t.Fatalf("Tool(paseo) = %q, %v; want the fnm default alias copy", path, ok)
	}

	// nvm names its default version in a file; only that version counts.
	versions := filepath.Join(home, ".nvm", "versions", "node")
	for _, v := range []string{"v18.20.0", "v20.19.0"} {
		if err := os.MkdirAll(filepath.Join(versions, v, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(versions, v, "bin", "herdr"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".nvm", "alias"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".nvm", "alias", "default"), []byte("20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, ok := Tool("herdr"); !ok || path != filepath.Join(versions, "v20.19.0", "bin", "herdr") {
		t.Fatalf("Tool(herdr) = %q, %v; want the version nvm defaults to", path, ok)
	}
}

func TestToolIgnoresVersionsAVersionManagerHasNotSelected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	// Both take precedence by design, so a real manager on this machine would
	// otherwise answer for the fake home.
	t.Setenv("FNM_DIR", filepath.Join(home, ".local", "share", "fnm"))
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	oldDirs := extraToolDirs
	extraToolDirs = []string{t.TempDir()}
	t.Cleanup(func() { extraToolDirs = oldDirs })

	// An installed version with no default alias naming it: not ours to run.
	bin := filepath.Join(home, ".nvm", "versions", "node", "v18.20.0", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "paseo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if path, ok := Tool("paseo"); ok {
		t.Fatalf("Tool(paseo) = %q, true; want absent until nvm names a default", path)
	}
}

// A hand-rolled Node prefix matches no version manager layout, but npm still
// puts globals beside node, and a symlink in ~/.local/bin is enough to find it.
func TestToolFindsAnNpmGlobalBesideNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("FNM_DIR", filepath.Join(home, ".local", "share", "fnm"))
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	oldDirs := extraToolDirs
	extraToolDirs = []string{t.TempDir()}
	t.Cleanup(func() { extraToolDirs = oldDirs })

	prefix := filepath.Join(home, ".local", "share", "node-v24.18.0", "installation", "bin")
	must(t, os.MkdirAll(prefix, 0o755))
	must(t, os.WriteFile(filepath.Join(prefix, "node"), []byte("#!/bin/sh\n"), 0o755))
	must(t, os.WriteFile(filepath.Join(prefix, "paseo"), []byte("#!/bin/sh\n"), 0o755))

	// paseo is only reachable through node's directory, and node only through
	// the symlink: neither is on PATH.
	localBin := filepath.Join(home, ".local", "bin")
	must(t, os.MkdirAll(localBin, 0o755))
	must(t, os.Symlink(filepath.Join(prefix, "node"), filepath.Join(localBin, "node")))

	// node's directory is reached through EvalSymlinks, and on macOS a temp
	// dir resolves under /private, so compare against the resolved prefix.
	resolved, err := filepath.EvalSymlinks(prefix)
	must(t, err)
	path, ok := Tool("paseo")
	if !ok || path != filepath.Join(resolved, "paseo") {
		t.Fatalf("Tool(paseo) = %q, %v; want the copy beside node", path, ok)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
