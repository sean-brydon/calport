package kit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-brydon/calport/internal/service"
)

// fakeBox is a home directory with a Cal.com checkout and a node whose
// install ships corepack, as fnm and the official tarballs do.
func fakeBox(t *testing.T) (in *Installer, root string, units *[]service.Spec) {
	t.Helper()
	home := t.TempDir()
	root = filepath.Join(home, "work", "cal")
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"calcom-monorepo"}`), 0o644))
	must(t, os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL=\"postgresql://u:p@localhost:5450/calendso\"\n"), 0o600))
	nodeDir := filepath.Join(home, "node", "bin")
	must(t, os.MkdirAll(nodeDir, 0o755))
	must(t, os.WriteFile(filepath.Join(nodeDir, "node"), []byte("#!/bin/sh\n"), 0o755))
	corepack := filepath.Join(home, "node", "lib", "node_modules", "corepack", "dist")
	must(t, os.MkdirAll(corepack, 0o755))
	must(t, os.WriteFile(filepath.Join(corepack, "yarn.js"), nil, 0o644))

	units = &[]service.Spec{}
	in = &Installer{
		Home:     home,
		Calportd: "/home/alex/.local/bin/calportd",
		Find: func(name string) string {
			switch name {
			case "node":
				return filepath.Join(nodeDir, "node")
			case "python3", "git", "systemctl", "docker", "orca":
				return "/usr/bin/" + name
			}
			return ""
		},
		Units: func(s service.Spec) error { *units = append(*units, s); return nil },
		Command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "publish=5450") {
				return []byte("database-postgres-1\n"), nil
			}
			return nil, nil
		},
	}
	return in, root, units
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallWritesTheKitAndStartsItsServices(t *testing.T) {
	in, root, units := fakeBox(t)
	res, err := in.Install(context.Background(), root, "devl")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed || res.Pattern != "*.devl.cal.localhost" {
		t.Fatalf("status = %+v", res.Status)
	}
	cfg := res.Config
	if cfg.Root != root || cfg.Orca != "/usr/bin/orca" || cfg.PostgresContainer != "database-postgres-1" || !strings.HasSuffix(cfg.Node, "/node") {
		t.Fatalf("config = %+v", cfg)
	}
	for _, name := range []string{"cal-worktree", "cal-archive", "cal-setup", "reconcile.py"} {
		st, err := os.Stat(filepath.Join(in.Dir(), name))
		if err != nil || st.Mode()&0o111 == 0 {
			t.Fatalf("%s missing or not executable: %v", name, err)
		}
		if name != "reconcile.py" {
			if target, err := os.Readlink(filepath.Join(in.Home, ".local", "bin", name)); err != nil || target != filepath.Join(in.Dir(), name) {
				t.Fatalf("~/.local/bin/%s -> %q, %v", name, target, err)
			}
		}
	}
	yarn, _ := os.ReadFile(filepath.Join(in.Dir(), "bin", "yarn"))
	if !strings.Contains(string(yarn), "corepack/dist/yarn.js") {
		t.Fatalf("yarn wrapper does not use corepack:\n%s", yarn)
	}
	if len(*units) != 2 || (*units)[0].Name != "cal-worktree-proxy" || (*units)[0].Program != in.Calportd ||
		strings.Join((*units)[0].Args, " ") != "kit router --dir "+in.Dir() || (*units)[1].Name != "cal-worktree-lifecycle" {
		t.Fatalf("units = %+v", *units)
	}
}

// Reinstalling over an earlier kit keeps its URL label, so URLs in use, and
// the laptop route for them, keep working. Its own scripts are replaced, and
// a hand-written one of the same name is kept aside.
func TestReinstallKeepsTheURLLabelAndBacksUpForeignScripts(t *testing.T) {
	in, root, _ := fakeBox(t)
	must(t, os.MkdirAll(in.Dir(), 0o700))
	must(t, os.WriteFile(filepath.Join(in.Dir(), "config.json"), []byte(`{"host":"personal","root":"`+root+`"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(in.Dir(), "cal-worktree"), []byte("old"), 0o755))
	must(t, os.MkdirAll(filepath.Join(in.Home, ".local", "bin"), 0o755))
	archive := filepath.Join(in.Home, ".local", "bin", "cal-archive")
	must(t, os.WriteFile(archive, []byte("#!/bin/sh\necho mine\n"), 0o755))

	res, err := in.Install(context.Background(), root, "devl")
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Host != "personal" || res.Pattern != "*.personal.cal.localhost" {
		t.Fatalf("label changed: %+v", res.Config)
	}
	if b, _ := os.ReadFile(filepath.Join(in.Dir(), "cal-worktree")); string(b) == "old" {
		t.Fatal("the kit's own script was not updated")
	}
	if b, err := os.ReadFile(archive + ".calport-backup"); err != nil || !strings.Contains(string(b), "mine") {
		t.Fatalf("hand-written cal-archive was not kept: %v", err)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "cal-archive.calport-backup") {
		t.Fatalf("notes do not mention the backup: %q", res.Notes)
	}
	saved, _ := filepath.Glob(filepath.Join(in.Dir(), "backup-*", "cal-worktree"))
	if len(saved) != 1 {
		t.Fatalf("the previous kit's scripts were not saved: %v", saved)
	}
	if b, _ := os.ReadFile(saved[0]); string(b) != "old" {
		t.Fatalf("saved cal-worktree = %q, want the previous one", b)
	}
	// Reinstalling calport's own kit makes no further backups.
	if _, err := in.Install(context.Background(), root, "devl"); err != nil {
		t.Fatal(err)
	}
	if again, _ := filepath.Glob(filepath.Join(in.Dir(), "backup-*")); len(again) != 1 {
		t.Fatalf("a reinstall of calport's kit backed up again: %v", again)
	}
}

func TestInstallRefusesWhatItCannotServe(t *testing.T) {
	in, root, _ := fakeBox(t)
	if _, err := in.Install(context.Background(), t.TempDir(), "devl"); err == nil || !strings.Contains(err.Error(), "not a Cal.com checkout") {
		t.Fatalf("installed for a non-Cal directory: %v", err)
	}
	if _, err := in.Install(context.Background(), root, "Dev L"); err == nil {
		t.Fatal("accepted a URL label with a space")
	}
	other := filepath.Join(filepath.Dir(root), "cal2")
	must(t, os.MkdirAll(filepath.Join(other, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(other, "package.json"), []byte(`{"name":"calcom-monorepo"}`), 0o644))
	if _, err := in.Install(context.Background(), root, "devl"); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Install(context.Background(), other, "devl"); err == nil || !strings.Contains(err.Error(), "one Cal.com checkout") {
		t.Fatalf("a second checkout replaced the first: %v", err)
	}
	find := in.Find
	in.Find = func(name string) string {
		if name == "node" {
			return ""
		}
		return find(name)
	}
	if _, err := in.Install(context.Background(), root, "devl"); err == nil || !strings.Contains(err.Error(), "Node.js") {
		t.Fatalf("installed without node: %v", err)
	}
}

func TestEmbeddedScriptsParse(t *testing.T) {
	dir := t.TempDir()
	in, root, _ := fakeBox(t)
	in.Home = dir
	os.MkdirAll(filepath.Join(dir, "work"), 0o755)
	if _, err := in.Install(context.Background(), root, "devl"); err != nil {
		t.Fatal(err)
	}
	check := map[string][]string{
		"python3": {"-c", "import ast,sys\nfor f in sys.argv[1:]: ast.parse(open(f).read(), f)", "cal-worktree", "reconcile.py", "orca-logs.py", "color-logs.py"},
		"node":    {"--check", "database.cjs"},
		"bash":    {"-n", "cal-archive"},
	}
	for tool, args := range check {
		if _, err := exec.LookPath(tool); err != nil {
			t.Logf("skipping %s: not installed", tool)
			continue
		}
		cmd := exec.Command(tool, args...)
		cmd.Dir = in.Dir()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", tool, args, err, out)
		}
	}
}

func TestStatusReadsConfig(t *testing.T) {
	in, _, _ := fakeBox(t)
	if in.Status().Installed {
		t.Fatal("reported installed on an empty box")
	}
	must(t, os.MkdirAll(in.Dir(), 0o700))
	b, _ := json.Marshal(Config{Host: "devl", Root: "/r"})
	must(t, os.WriteFile(filepath.Join(in.Dir(), "config.json"), b, 0o600))
	must(t, os.WriteFile(filepath.Join(in.Dir(), "cal-worktree"), nil, 0o755))
	must(t, os.MkdirAll(filepath.Join(in.Dir(), "routes"), 0o700))
	must(t, os.WriteFile(filepath.Join(in.Dir(), "routes", "aaaaaaaaaaaa.json"), []byte(`{"host":"a-aaaaaa.devl.cal.localhost","path":"/r/a","port":3100,"active":true}`), 0o600))
	s := in.Status()
	if !s.Installed || s.Pattern != "*.devl.cal.localhost" || len(s.Worktrees) != 1 || s.Worktrees[0].Path != "/r/a" || !s.Worktrees[0].Active {
		t.Fatalf("status = %+v", s)
	}
}

func TestOnlyLastingPathsAreRecorded(t *testing.T) {
	for path, want := range map[string]bool{
		"/home/alex/.local/bin/node":                         true,
		"/usr/bin/python3":                                   true,
		"/run/user/1000/fnm_multishells/5269_17905/bin/node": false,
		"/tmp/xyz/node":                                      false,
		"node":                                               false,
		"":                                                   false,
	} {
		if got := lasting(path); got != want {
			t.Errorf("lasting(%q) = %v, want %v", path, got, want)
		}
	}
}
