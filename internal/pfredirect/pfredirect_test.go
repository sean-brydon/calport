package pfredirect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRulesRedirectOnlyLoopbackPort80(t *testing.T) {
	both := string(Rules(1355, true))
	for _, want := range []string{
		"rdr pass on lo0 inet6 proto tcp from any to ::1 port 80 -> ::1 port 1355\n",
		"rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 80 -> 127.0.0.1 port 1355\n",
	} {
		if !strings.Contains(both, want) {
			t.Errorf("rules missing %q:\n%s", want, both)
		}
	}
	if strings.Contains(both, " on en") || strings.Contains(both, "from any to any") {
		t.Fatalf("rules reach beyond loopback:\n%s", both)
	}
	v6 := string(Rules(1355, false))
	if strings.Contains(v6, "127.0.0.1") {
		t.Fatalf("IPv4 redirected although another program owns 127.0.0.1:80:\n%s", v6)
	}
}

func TestBootJobLoadsAppleAnchorsOnlyWhenPfIsEmpty(t *testing.T) {
	p := string(Plist())
	if !strings.Contains(p, "<string>/bin/sh</string><string>-c</string>") {
		t.Fatalf("boot job is not a fixed shell script:\n%s", p)
	}
	script := bootScript()
	for _, want := range []string{
		"p=/sbin/pfctl;",
		`grep -q 'com.apple/\*'`,
		`[ -z "$($p -s nat 2>/dev/null)$($p -s rules 2>/dev/null)" ]`,
		"then $p -f /etc/pf.conf; fi;",
		"$p -E -a com.apple/calport -f /etc/pf.anchors/calport",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("boot script missing %q:\n%s", want, script)
		}
	}
	// The script is XML-escaped inside the plist, so its quotes and
	// ampersands cannot break the property list.
	if strings.Contains(p, "2>/dev/null)$(") && !strings.Contains(p, "&gt;") {
		t.Fatal("boot script was not escaped for the plist")
	}
}

func TestInstalledMatchesThePort(t *testing.T) {
	RulesPath = filepath.Join(t.TempDir(), "calport")
	t.Cleanup(func() { RulesPath = "/etc/pf.anchors/calport" })
	if Installed(1355) {
		t.Fatal("reported installed with no rule file")
	}
	os.WriteFile(RulesPath, Rules(1355, false), 0o644)
	if !Installed(1355) || Installed(8080) {
		t.Fatal("Installed does not match the redirected port")
	}
	if CoversIPv4() {
		t.Fatal("reported IPv4 coverage for an IPv6-only rule")
	}
	os.WriteFile(RulesPath, Rules(1355, true), 0o644)
	if !CoversIPv4() {
		t.Fatal("IPv4 coverage not detected")
	}
}

func TestInstallRefusesWithoutRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	if _, err := Install(1355); err == nil {
		t.Fatal("installed without root")
	}
	if err := Remove(); err == nil {
		t.Fatal("removed without root")
	}
}

func TestBootJobIsAValidPropertyList(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil is macOS only")
	}
	path := filepath.Join(t.TempDir(), "job.plist")
	os.WriteFile(path, Plist(), 0o644)
	if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil rejected the boot job: %s", out)
	}
	out, err := exec.Command(plutil, "-extract", "ProgramArguments.2", "raw", path).Output()
	if err != nil || strings.TrimSpace(string(out)) != bootScript() {
		t.Fatalf("the script launchd will run differs from bootScript():\n%s", out)
	}
}
