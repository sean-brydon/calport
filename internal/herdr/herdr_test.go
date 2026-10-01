package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostRoutesThroughTheBoxsNetwork(t *testing.T) {
	got := SSHHost{Box: "devl", Address: "100.89.46.46", User: "sean", Network: "personal", Calport: "/Applications/Calport.app/Contents/MacOS/calport"}.Render()
	for _, want := range []string{"Host calport-devl\n", "HostName 100.89.46.46\n", "User sean\n", "ProxyCommand '/Applications/Calport.app/Contents/MacOS/calport' network proxy 'personal' %h %p\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// On this computer's own network SSH needs no help from calport.
	if got := (SSHHost{Box: "cal", Address: "100.1.2.3"}).Render(); strings.Contains(got, "ProxyCommand") {
		t.Errorf("a box on this computer's network got a ProxyCommand:\n%s", got)
	}
}

func TestIncludeGoesFirstOnceAndKeepsABackup(t *testing.T) {
	c := Config{Dir: t.TempDir()}
	path := filepath.Join(c.Dir, "config")
	mine := "Host devl\n  User sean\n"
	os.WriteFile(path, []byte(mine), 0o600)
	for range 2 {
		if err := c.EnsureInclude(); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), includeLine) != 1 || !strings.HasSuffix(string(b), mine) || strings.Index(string(b), includeLine) > strings.Index(string(b), "Host devl") {
		t.Fatalf("config =\n%s", b)
	}
	if backup, _ := os.ReadFile(path + ".calport-backup"); string(backup) != mine {
		t.Fatalf("backup = %q", backup)
	}
	if !c.Included() {
		t.Fatal("Included() is false after EnsureInclude")
	}
}

func TestIncludeCreatesAMissingConfig(t *testing.T) {
	c := Config{Dir: filepath.Join(t.TempDir(), ".ssh")}
	if err := c.EnsureInclude(); err != nil {
		t.Fatal(err)
	}
	if !c.Included() {
		t.Fatal("no Include written")
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "config.calport-backup")); err == nil {
		t.Fatal("backed up a file that did not exist")
	}
}

func TestWriteKeepsOtherBoxes(t *testing.T) {
	c := Config{Dir: t.TempDir()}
	c.Write(SSHHost{Box: "cal", Address: "100.1.1.1"})
	c.Write(SSHHost{Box: "devl", Address: "100.2.2.2"})
	c.Write(SSHHost{Box: "cal", Address: "100.3.3.3"})
	cal, _ := os.ReadFile(filepath.Join(c.Dir, "calport", "cal.conf"))
	devl, _ := os.ReadFile(filepath.Join(c.Dir, "calport", "devl.conf"))
	if !strings.Contains(string(cal), "100.3.3.3") || !strings.Contains(string(devl), "100.2.2.2") {
		t.Fatalf("cal:\n%s\ndevl:\n%s", cal, devl)
	}
}

func TestOlder(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{{"0.8.2", true}, {"0.9.0", false}, {"0.9.3", false}, {"0.10.0", false}, {"1.0.0", false}, {"0.9", false}, {"0.8", true}, {"v0.9.1-preview.3", false}, {"", true}} {
		if got := Older(tc.v, MinVersion); got != tc.want {
			t.Errorf("Older(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
