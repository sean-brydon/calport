package kit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// reclaimBox is a kit directory with the real cal-worktree script, and fake
// node and systemctl that log how they were called.
func reclaimBox(t *testing.T) (home, base, calls string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	home = t.TempDir()
	base = filepath.Join(home, ".local", "share", "cal-worktrees")
	bin := filepath.Join(home, "fakebin")
	calls = filepath.Join(home, "calls")
	must(t, os.MkdirAll(filepath.Join(base, "routes"), 0o700))
	must(t, os.MkdirAll(bin, 0o755))
	script, _ := files.ReadFile("cal/cal-worktree")
	must(t, os.WriteFile(filepath.Join(base, "cal-worktree"), script, 0o755))
	fake := `#!/bin/sh
echo "$(basename "$0") $*" >> "` + calls + `"
case "$2" in
sizes) echo '{"calwt_aaaaaaaaaaaa":1073741824,"calwt_bbbbbbbbbbbb":2147483648}';;
prune-templates) echo '{"templates":[{"name":"caltpl_old","bytes":536870912}]}';;
*) echo '{}';;
esac
`
	for _, name := range []string{"node", "systemctl"} {
		must(t, os.WriteFile(filepath.Join(bin, name), []byte(fake), 0o755))
	}
	cfg, _ := json.Marshal(Config{Host: "devl", Root: filepath.Join(home, "cal"), Node: filepath.Join(bin, "node")})
	must(t, os.WriteFile(filepath.Join(base, "config.json"), cfg, 0o600))
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home, base, calls
}

func record(t *testing.T, base, key, path string, extra string) {
	t.Helper()
	data := `{"host":"` + key[:4] + `.devl.cal.localhost","port":3100,"path":"` + path + `","root":"/r","database":"calwt_` + key + `","active":false` + extra + `}`
	must(t, os.WriteFile(filepath.Join(base, "routes", key+".json"), []byte(data), 0o600))
	must(t, os.WriteFile(filepath.Join(base, key+"-setup.log"), []byte("log"), 0o600))
}

func runReclaim(t *testing.T, base string, args ...string) ReclaimReport {
	t.Helper()
	out, err := exec.Command(filepath.Join(base, "cal-worktree"), append([]string{"reclaim", "--json"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("reclaim: %v\n%s", err, out)
	}
	var r ReclaimReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("reclaim printed %s", out)
	}
	return r
}

func TestReclaimFreesRemovedWorktreesAndWaitsForVanishedOnes(t *testing.T) {
	home, base, calls := reclaimBox(t)
	live := filepath.Join(home, "live")
	must(t, os.MkdirAll(live, 0o755))
	removed := filepath.Join(home, "removed")   // removed through calport
	vanished := filepath.Join(home, "vanished") // gone some other way, e.g. Orca
	record(t, base, "aaaaaaaaaaaa", removed, "")
	record(t, base, "bbbbbbbbbbbb", vanished, "")
	record(t, base, "cccccccccccc", live, `,"gone_since":1`)

	dry := runReclaim(t, base, "--dry-run", "--path", removed)
	if len(dry.Reclaimed) != 1 || dry.Reclaimed[0].Path != removed || !dry.DryRun {
		t.Fatalf("dry run = %+v", dry)
	}
	if _, err := os.Stat(filepath.Join(base, "routes", "aaaaaaaaaaaa.json")); err != nil {
		t.Fatal("a dry run removed a record")
	}

	r := runReclaim(t, base, "--path", removed)
	if len(r.Reclaimed) != 1 || r.Reclaimed[0].Database != "calwt_aaaaaaaaaaaa" || r.Reclaimed[0].Bytes != 1<<30 {
		t.Fatalf("reclaimed = %+v", r.Reclaimed)
	}
	if len(r.Waiting) != 1 || r.Waiting[0].Path != vanished {
		t.Fatalf("waiting = %+v; the vanished worktree must wait out its grace", r.Waiting)
	}
	if r.Bytes != 1<<30+512<<20 {
		t.Fatalf("bytes = %d", r.Bytes)
	}
	for _, gone := range []string{"routes/aaaaaaaaaaaa.json", "aaaaaaaaaaaa-setup.log"} {
		if _, err := os.Stat(filepath.Join(base, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived", gone)
		}
	}
	log, _ := os.ReadFile(calls)
	drop := "drop " + filepath.Join(home, "cal") + " calwt_"
	if !strings.Contains(string(log), drop+"aaaaaaaaaaaa") || strings.Contains(string(log), drop+"bbbbbbbbbbbb") {
		t.Fatalf("only the removed worktree's database may be dropped; calls:\n%s", log)
	}
	var v, l map[string]any
	b, _ := os.ReadFile(filepath.Join(base, "routes", "bbbbbbbbbbbb.json"))
	json.Unmarshal(b, &v)
	if v["gone_since"] == nil {
		t.Fatal("the vanished worktree's grace period was not started")
	}
	b, _ = os.ReadFile(filepath.Join(base, "routes", "cccccccccccc.json"))
	json.Unmarshal(b, &l)
	if l["gone_since"] != nil {
		t.Fatal("a worktree whose folder is back kept its countdown")
	}

	all := runReclaim(t, base, "--retention-days", "0")
	if len(all.Reclaimed) != 1 || all.Reclaimed[0].Path != vanished {
		t.Fatalf("--retention-days 0 reclaimed %+v", all.Reclaimed)
	}
	if _, err := os.Stat(filepath.Join(base, "routes", "cccccccccccc.json")); err != nil {
		t.Fatal("a live worktree was reclaimed")
	}
}
