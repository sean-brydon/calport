package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stub(t *testing.T, os_ string) (home string, calls *[]string) {
	t.Helper()
	home = t.TempDir()
	oldOS, oldHome, oldCmd := goos, homeDir, command
	calls = &[]string{}
	goos = os_
	homeDir = func() (string, error) { return home, nil }
	command = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Cleanup(func() { goos, homeDir, command = oldOS, oldHome, oldCmd })
	return home, calls
}

var spec = Spec{
	Name:        "calport-agent",
	Description: "calport agent",
	Program:     "/opt/calport/bin/calport",
	Args:        []string{"agent"},
	Env:         map[string]string{"CALPORT_HOME": "/Users/alex/Library/Application Support/calport"},
	LogPath:     "/Users/alex/Library/Logs/calport.log",
}

func TestUnitsRestartOnlyAfterFailure(t *testing.T) {
	stub(t, "darwin")
	plist, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>",
		"<key>RunAtLoad</key><true/>",
		"<string>/opt/calport/bin/calport</string>\n<string>agent</string>",
		"<key>CALPORT_HOME</key><string>/Users/alex/Library/Application Support/calport</string>",
	} {
		if !strings.Contains(string(plist), want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}
	stub(t, "linux")
	unit, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Restart=on-failure",
		"ExecStart=/opt/calport/bin/calport agent",
		`Environment="CALPORT_HOME=/Users/alex/Library/Application Support/calport"`,
		"WantedBy=default.target",
	} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(string(unit), "Restart=always") {
		t.Error("a clean stop would be restarted")
	}
}

func TestRenderEscapesValues(t *testing.T) {
	stub(t, "darwin")
	s := spec
	s.Env = map[string]string{"X": `a<b>&"c"`}
	plist, _ := Render(s)
	if strings.Contains(string(plist), "a<b>") {
		t.Fatalf("plist value not escaped:\n%s", plist)
	}
	stub(t, "linux")
	s.Args = []string{"serve", "--listen", "$HOME 100%"}
	unit, _ := Render(s)
	if !strings.Contains(string(unit), `"$$HOME 100%%"`) {
		t.Fatalf("systemd argument not quoted:\n%s", unit)
	}
}

func TestInstallIsIdempotentAndDetectsItself(t *testing.T) {
	for _, os_ := range []string{"darwin", "linux"} {
		_, calls := stub(t, os_)
		if Installed(spec) {
			t.Fatalf("%s: installed before install", os_)
		}
		path, err := Install(spec)
		if err != nil {
			t.Fatalf("%s: %v", os_, err)
		}
		if !Installed(spec) {
			t.Fatalf("%s: not installed after install", os_)
		}
		other := spec
		other.Env = map[string]string{"CALPORT_HOME": "/elsewhere"}
		if Installed(other) {
			t.Fatalf("%s: a unit for another home counted as installed", os_)
		}
		if _, err := Install(spec); err != nil {
			t.Fatalf("%s: reinstall: %v", os_, err)
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if len(entries) != 1 {
			t.Fatalf("%s: install left extra files: %v", os_, entries)
		}
		if len(*calls) == 0 {
			t.Fatalf("%s: install did not load the unit", os_)
		}
		if _, err := Uninstall(spec); err != nil {
			t.Fatal(err)
		}
		if Installed(spec) {
			t.Fatalf("%s: still installed after uninstall", os_)
		}
	}
}

func TestInstallRefusesTemporaryAndRelativeBinaries(t *testing.T) {
	stub(t, "darwin")
	for _, program := range []string{filepath.Join(os.TempDir(), "calport"), "/Users/alex/Library/Caches/go-build/ab/calport", "bin/calport"} {
		s := spec
		s.Program = program
		if _, err := Install(s); err == nil {
			t.Errorf("installed %s", program)
		}
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	stub(t, "windows")
	if _, err := Render(spec); err == nil {
		t.Fatal("rendered a unit for windows")
	}
	if Installed(spec) {
		t.Fatal("reported installed on windows")
	}
}

func TestKeepChildrenSurvivesServiceRestarts(t *testing.T) {
	s := spec
	stub(t, "linux")
	plain, _ := Render(s)
	if strings.Contains(string(plain), "KillMode") {
		t.Fatal("KillMode set without KeepChildren")
	}
	s.KeepChildren = true
	unit, _ := Render(s)
	if !strings.Contains(string(unit), "KillMode=process\n") {
		t.Fatalf("unit does not keep children:\n%s", unit)
	}
	stub(t, "darwin")
	plist, _ := Render(s)
	if !strings.Contains(string(plist), "<key>AbandonProcessGroup</key><true/>") {
		t.Fatalf("plist does not keep children:\n%s", plist)
	}
}

func TestLinuxUnitsWriteOutputToTheLogPath(t *testing.T) {
	stub(t, "linux")
	unit, err := Render(Spec{
		Name:        "calport-orca",
		Description: "Orca runtime",
		Program:     "/usr/bin/orca",
		Args:        []string{"serve"},
		LogPath:     "/home/alex/.config/calport/units/calport-orca.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"StandardOutput=append:/home/alex/.config/calport/units/calport-orca.log",
		"StandardError=append:/home/alex/.config/calport/units/calport-orca.log",
	} {
		if !strings.Contains(string(unit), want) {
			t.Fatalf("unit is missing %q:\n%s", want, unit)
		}
	}
}

func TestLinuxUnitsWithoutALogPathRedirectNothing(t *testing.T) {
	stub(t, "linux")
	unit, err := Render(Spec{Name: "calport-agent", Program: "/usr/bin/calport", Args: []string{"agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "StandardOutput=") {
		t.Fatalf("unit redirects output with no LogPath set:\n%s", unit)
	}
}
