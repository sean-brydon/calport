package doctor

import (
	"bytes"
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
