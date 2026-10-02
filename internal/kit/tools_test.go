package kit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// checkout is a real git repository with a fresh home directory, so the
// tests see what git sees.
func checkout(t *testing.T) (*Installer, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	root := filepath.Join(home, "work", "cal")
	must(t, os.MkdirAll(root, 0o755))
	gitRun(t, root, "init", "-q")
	return &Installer{Home: home}, root
}

func gitRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func states(statuses []ToolStatus) map[string]ToolState {
	m := map[string]ToolState{}
	for _, s := range statuses {
		m[s.Tool] = s.State
	}
	return m
}

func TestSetUpToolsWritesEachFileOnceAndKeepsThemOutOfGit(t *testing.T) {
	in, root := checkout(t)
	ctx := context.Background()
	must(t, os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("# mine\n*.local"), 0o644))

	before := states(in.Tools(ctx, root, OrcaHooks{}))
	for _, tool := range []string{"orca", "cursor", "codex", "superset"} {
		if before[tool] != ToolMissing {
			t.Errorf("%s = %s before setup, want missing", tool, before[tool])
		}
	}
	if before["conductor"] != ToolSkipped || before["claude"] != ToolSkipped {
		t.Errorf("conductor and claude should be skipped: %v", before)
	}

	written, err := in.SetUpTools(ctx, root, nil, OrcaHooks{})
	must(t, err)
	want := []string{"orca.yaml", ".cursor/worktrees.json", ".codex/environments/environment.toml", ".superset/config.json"}
	if !slices.Equal(written, want) {
		t.Fatalf("wrote %v, want %v", written, want)
	}
	if again, err := in.SetUpTools(ctx, root, nil, OrcaHooks{}); err != nil || len(again) != 0 {
		t.Fatalf("second setup wrote %v (%v), want nothing", again, err)
	}
	for tool, state := range states(in.Tools(ctx, root, OrcaHooks{})) {
		if slices.Contains([]string{"orca", "cursor", "codex", "superset"}, tool) && state != ToolConfigured {
			t.Errorf("%s = %s after setup, want configured", tool, state)
		}
	}

	exclude, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if !strings.HasPrefix(string(exclude), "# mine\n*.local\n") {
		t.Errorf("existing exclude lines changed:\n%s", exclude)
	}
	for _, f := range want {
		if n := strings.Count(string(exclude), "/"+f+"\n"); n != 1 {
			t.Errorf("/%s is in the exclude file %d times", f, n)
		}
	}
	if status := gitRun(t, root, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Errorf("git sees the tools' files:\n%s", status)
	}
	if !in.RunsHooks(ctx, "orca", root) || in.RunsHooks(ctx, "herdr", root) {
		t.Error("RunsHooks should be true for the written orca.yaml and false for Herdr")
	}
}

func TestToolFilesParseAndRunTheKitHooks(t *testing.T) {
	for _, tool := range repoTools {
		switch filepath.Ext(tool.file) {
		case ".json":
			var v map[string][]string
			must(t, json.Unmarshal([]byte(tool.content), &v))
			if !slices.Contains(slices.Concat(v["setup-worktree"], v["setup"]), SetupHook) {
				t.Errorf("%s does not run %s: %v", tool.file, SetupHook, v)
			}
		case ".toml":
			if _, err := exec.LookPath("python3"); err != nil {
				continue
			}
			out, err := exec.Command("python3", "-c", "import sys,tomllib,json; print(json.dumps(tomllib.loads(sys.argv[1])))", tool.content).Output()
			if err != nil {
				continue // python3 before 3.11 has no tomllib
			}
			var v struct {
				Setup, Cleanup struct{ Script string }
			}
			must(t, json.Unmarshal(out, &v))
			if v.Setup.Script != SetupHook || v.Cleanup.Script != ArchiveHook {
				t.Errorf("%s parses to %+v", tool.file, v)
			}
		}
	}
}

func TestSetUpToolsLeavesTrackedAndForeignFilesAlone(t *testing.T) {
	in, root := checkout(t)
	ctx := context.Background()
	tracked := filepath.Join(root, ".cursor", "worktrees.json")
	must(t, os.MkdirAll(filepath.Dir(tracked), 0o755))
	must(t, os.WriteFile(tracked, []byte(`{"setup-worktree":["npm ci"]}`), 0o644))
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "cursor")
	foreign := filepath.Join(root, ".superset", "config.json")
	must(t, os.MkdirAll(filepath.Dir(foreign), 0o755))
	must(t, os.WriteFile(foreign, []byte(`{"setup":["make"]}`), 0o644))

	got := states(in.Tools(ctx, root, OrcaHooks{}))
	if got["cursor"] != ToolTracked || got["superset"] != ToolSkipped {
		t.Fatalf("cursor = %s, superset = %s; want tracked and skipped", got["cursor"], got["superset"])
	}
	written, err := in.SetUpTools(ctx, root, []string{"cursor", "superset"}, OrcaHooks{})
	must(t, err)
	if len(written) != 0 {
		t.Errorf("wrote %v over files calport did not write", written)
	}
	if b, _ := os.ReadFile(foreign); string(b) != `{"setup":["make"]}` {
		t.Errorf("foreign file changed: %s", b)
	}
	if _, err := in.SetUpTools(ctx, root, []string{"vscode"}, OrcaHooks{}); err == nil {
		t.Error("an unknown tool should be an error")
	}
}

func TestOrcaSettingsTakePrecedenceOverOrcaYAML(t *testing.T) {
	in, root := checkout(t)
	ctx := context.Background()
	for hooks, want := range map[OrcaHooks]ToolState{
		{Setup: SetupHook, Archive: ArchiveHook}: ToolConfigured,
		{Setup: "make setup"}:                    ToolSkipped,
	} {
		if got := states(in.Tools(ctx, root, hooks))["orca"]; got != want {
			t.Errorf("with Orca running %+v, orca = %s, want %s", hooks, got, want)
		}
		written, err := in.SetUpTools(ctx, root, []string{"orca"}, hooks)
		if err != nil || len(written) != 0 {
			t.Errorf("wrote %v (%v) though Orca's settings run hooks", written, err)
		}
	}
}

func TestHerdrPluginIsOptInAndLinkedOnce(t *testing.T) {
	in, root := checkout(t)
	ctx := context.Background()
	bin := filepath.Join(in.Home, ".local", "bin")
	must(t, os.MkdirAll(bin, 0o755))
	// A fake herdr that remembers what was linked and counts link calls. Like
	// herdr 0.8.2, it wants the path before any flag: `link --enabled PATH` fails.
	must(t, os.WriteFile(filepath.Join(bin, "herdr"), []byte(`#!/bin/sh
state="$(dirname "$0")/linked"
case "$2" in
list) if [ -f "$state" ]; then echo '{"result":{"plugins":[{"id":"`+herdrPluginID+`"}]}}'; else echo '{"result":{"plugins":[]}}'; fi ;;
link) case "$3" in -*) echo "unknown option: $4" >&2; exit 2 ;; esac; echo "$3" >> "$state" ;;
esac
`), 0o755))

	if s := in.herdrStatus(ctx); s.State != ToolMissing || !s.OptIn || !s.Installed {
		t.Fatalf("herdr before setup = %+v", s)
	}
	written, err := in.SetUpTools(ctx, root, nil, OrcaHooks{})
	must(t, err)
	if slices.Contains(written, in.HerdrPluginDir()) {
		t.Fatal("setting up every tool installed the opt-in Herdr plugin")
	}
	for range 2 {
		_, err = in.SetUpTools(ctx, root, []string{"herdr"}, OrcaHooks{})
		must(t, err)
	}
	if s := in.herdrStatus(ctx); s.State != ToolConfigured {
		t.Fatalf("herdr after setup = %+v", s)
	}
	linked, _ := os.ReadFile(filepath.Join(bin, "linked"))
	if string(linked) != in.HerdrPluginDir()+"\n" {
		t.Errorf("herdr plugin link calls: %q", linked)
	}
	manifest, _ := os.ReadFile(filepath.Join(in.HerdrPluginDir(), "herdr-plugin.toml"))
	for _, want := range []string{`on = "worktree.created"`, `on = "worktree.removed"`, filepath.Join(in.HerdrPluginDir(), "hook")} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("manifest lacks %s:\n%s", want, manifest)
		}
	}
	if !in.RunsHooks(ctx, "herdr", root) {
		t.Error("RunsHooks(herdr) should be true once the plugin is linked")
	}
}

// pythonFunc runs one top-level function from a Python file on each JSON
// argument list, without running the rest of the file.
func pythonFunc(t *testing.T, file, fn string, calls [][]any) []any {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	const driver = `
import ast, json, sys
tree = ast.parse(open(sys.argv[1]).read())
fn = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == sys.argv[2]][0]
ns = {}
exec(compile(ast.Module(body=[fn], type_ignores=[]), sys.argv[1], 'exec'), ns)
print(json.dumps([ns[sys.argv[2]](*args) for args in json.load(sys.stdin)]))
`
	in, _ := json.Marshal(calls)
	cmd := exec.Command("python3", "-c", driver, file, fn)
	cmd.Stdin = strings.NewReader(string(in))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python3: %v", err)
	}
	var results []any
	must(t, json.Unmarshal(out, &results))
	return results
}

func TestCalWorktreeFindsRootAndWorktreeFromEachTool(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want []any
	}{
		{"no tool: the kit's root and the current directory", nil, nil, []any{nil, "/cwd", nil}},
		{"Orca", map[string]string{"ORCA_ROOT_PATH": "/r", "ORCA_WORKTREE_PATH": "/w", "ORCA_WORKSPACE_NAME": "fix"}, nil, []any{"/r", "/w", "fix"}},
		{"Orca without a worktree path uses the current directory", map[string]string{"ORCA_ROOT_PATH": "/r"}, nil, []any{"/r", "/cwd", nil}},
		{"Orca wins over the others", map[string]string{"ORCA_WORKTREE_PATH": "/w", "SUPERSET_ROOT_PATH": "/s", "ROOT_WORKTREE_PATH": "/c"}, nil, []any{nil, "/w", nil}},
		{"Conductor", map[string]string{"CONDUCTOR_ROOT_PATH": "/r", "CONDUCTOR_WORKSPACE_PATH": "/w", "CONDUCTOR_WORKSPACE_NAME": "n"}, nil, []any{"/r", "/w", "n"}},
		{"Superset", map[string]string{"SUPERSET_ROOT_PATH": "/r", "SUPERSET_WORKSPACE_PATH": "/w", "SUPERSET_WORKSPACE_NAME": "n"}, nil, []any{"/r", "/w", "n"}},
		{"Codex", map[string]string{"CODEX_SOURCE_TREE_PATH": "/r", "CODEX_WORKTREE_PATH": "/w"}, nil, []any{"/r", "/w", nil}},
		{"Cursor with $1", map[string]string{"ROOT_WORKTREE_PATH": "/r"}, []string{"/w"}, []any{"/r", "/w", nil}},
		{"Cursor in the worktree", map[string]string{"ROOT_WORKTREE_PATH": "/r"}, nil, []any{"/r", "/cwd", nil}},
		{"empty values are unset", map[string]string{"ORCA_ROOT_PATH": "", "CODEX_WORKTREE_PATH": "/w"}, nil, []any{nil, "/w", nil}},
	}
	var calls [][]any
	for _, c := range cases {
		env := c.env
		if env == nil {
			env = map[string]string{}
		}
		args := c.args
		if args == nil {
			args = []string{}
		}
		calls = append(calls, []any{env, args, "/cwd"})
	}
	got := pythonFunc(t, filepath.Join("cal", "cal-worktree"), "locate", calls)
	for i, c := range cases {
		if g, _ := json.Marshal(got[i]); string(g) != mustJSON(c.want) {
			t.Errorf("%s: locate = %s, want %s", c.name, g, mustJSON(c.want))
		}
	}
}

func TestHerdrHookFindsTheEventsWorktree(t *testing.T) {
	wt := map[string]any{"path": "/w", "branch": "fix", "is_bare": false}
	provenance := map[string]any{"worktree": map[string]any{"path": "/main"}}
	got := pythonFunc(t, filepath.Join("herdr-plugin", "hook"), "worktree_path", [][]any{
		{map[string]any{"type": "worktree_created", "workspace": provenance, "worktree": wt}},
		{map[string]any{"event": "worktree.removed", "data": map[string]any{"workspace": nil, "worktree": map[string]any{"path": "/w", "label": "l"}, "forced": false}}},
		{map[string]any{"type": "workspace_created"}},
	})
	for i, want := range []string{`["/w","fix"]`, `["/w","l"]`, `[null,""]`} {
		if g, _ := json.Marshal(got[i]); string(g) != want {
			t.Errorf("event %d: worktree_path = %s, want %s", i, g, want)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Paseo is opt-in and only offered where Paseo is installed, the same way Herdr
// is. A box without it must report skipped rather than missing, so nobody is
// told to set up a tool that is not there.
func TestPaseoIsReportedPerBoxAndIsOptIn(t *testing.T) {
	in, root := checkout(t)
	var paseo *ToolStatus
	for _, s := range in.Tools(context.Background(), root, OrcaHooks{}) {
		if s.Tool == "paseo" {
			paseo = &s
		}
	}
	if paseo == nil {
		t.Fatal("Tools() never reported paseo; it must appear beside the other worktree tools")
	}
	if !paseo.OptIn {
		t.Fatal("paseo must be opt-in: installing a plugin into someone's daemon is not a default")
	}
	if paseo.Installed && paseo.State == ToolSkipped {
		t.Fatal("paseo is installed here, so it must not be skipped")
	}
	if !paseo.Installed && paseo.State != ToolSkipped {
		t.Fatalf("paseo is not installed, so state must be skipped, got %q", paseo.State)
	}
}
