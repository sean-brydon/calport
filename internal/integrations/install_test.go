package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An existing Cursor hooks file, as Orca writes it.
const orcaCursorHooks = `{
  "hooks": {
    "stop": [{"command": "/bin/sh '/Users/alex/.orca/agent-hooks/cursor-hook.sh'", "timeout": 10}],
    "beforeShellExecution": [{"command": "/bin/sh orca-allow", "timeout": 10}]
  }
}`

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCursorHooksAreAppendedAfterOrcasAndOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	os.WriteFile(path, []byte(orcaCursorHooks), 0o600)
	changed, err := InstallCursorHooks(path, "/Users/alex/bin/calport")
	if err != nil || !changed {
		t.Fatalf("install: %v changed=%v", err, changed)
	}
	root := readJSON(t, path)
	hooks := root["hooks"].(map[string]any)
	stop := hooks["stop"].([]any)
	if len(stop) != 2 || !strings.Contains(stop[0].(map[string]any)["command"].(string), "orca") ||
		stop[1].(map[string]any)["command"] != "/Users/alex/bin/calport hook cursor stop" {
		t.Fatalf("stop hooks = %v", stop)
	}
	if len(hooks["beforeShellExecution"].([]any)) != 1 {
		t.Fatal("an unrelated hook was changed")
	}
	if changed, _ := InstallCursorHooks(path, "/Users/alex/bin/calport"); changed {
		t.Fatal("a second install changed the file again")
	}
	backup, err := os.ReadFile(path + ".calport-backup")
	if err != nil || string(backup) != orcaCursorHooks {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode changed to %v", info.Mode().Perm())
	}
}

func TestClaudeHooksKeepExistingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o644)
	if _, err := InstallClaudeHooks(path, "/home/alex/.local/bin/calportd"); err != nil {
		t.Fatal(err)
	}
	root := readJSON(t, path)
	if root["model"] != "opus" {
		t.Fatal("an existing setting was lost")
	}
	hooks := root["hooks"].(map[string]any)
	if stop := hooks["Stop"].([]any); len(stop) != 2 {
		t.Fatalf("Stop hooks = %v", stop)
	}
	if len(hooks["Notification"].([]any)) != 1 {
		t.Fatal("Notification hook missing")
	}
	if changed, _ := InstallClaudeHooks(path, "/home/alex/.local/bin/calportd"); changed {
		t.Fatal("a second install changed the file again")
	}
}

func TestInstallCreatesMissingFilesAndRefusesBrokenOnes(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallClaudeHooks(filepath.Join(dir, "new", "settings.json"), "calport"); err != nil {
		t.Fatalf("missing settings file: %v", err)
	}
	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte("{ not json"), 0o644)
	if _, err := InstallCursorHooks(broken, "calport"); err == nil {
		t.Fatal("rewrote a file that is not JSON")
	}
	if b, _ := os.ReadFile(broken); string(b) != "{ not json" {
		t.Fatal("a broken file was modified")
	}
}

func TestSkillInstalls(t *testing.T) {
	path, err := InstallSkill(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "---\nname: calport\n") {
		t.Fatalf("skill file starts with %q", string(b)[:30])
	}
}

func TestQuotedBinaryPaths(t *testing.T) {
	if got := hookCommand("/Users/alex/Application Support/calport", "cursor", "stop"); got != "'/Users/alex/Application Support/calport' hook cursor stop" {
		t.Fatalf("hookCommand = %q", got)
	}
}
