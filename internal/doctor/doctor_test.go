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
