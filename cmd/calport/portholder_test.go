package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plist writes a launchd job definition listening on listen, the shape
// portHolder has to recognise.
func plist(t *testing.T, dir, label, listen string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>` + label + `</string>
<key>ProgramArguments</key><array><string>/opt/example/proxy</string><string>--listen</string><string>` + listen + `</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
</dict></plist>
`
	if err := os.WriteFile(filepath.Join(dir, label+".plist"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirs points portHolder at temporary directories standing in for the system
// and user launchd locations.
func dirs(t *testing.T) (system, user string) {
	t.Helper()
	root := t.TempDir()
	system, user = filepath.Join(root, "LaunchDaemons"), filepath.Join(root, "LaunchAgents")
	old := jobDirs
	jobDirs = func() []jobDir {
		return []jobDir{{path: system, system: true}, {path: user}}
	}
	t.Cleanup(func() { jobDirs = old })
	return system, user
}

func TestPortHolderNamesASystemJobAndItsScope(t *testing.T) {
	system, _ := dirs(t)
	plist(t, system, "io.tailmux.cal-worktrees", "127.0.0.1:80")

	got, ok := portHolder(80)
	if !ok {
		t.Fatal("portHolder found nothing; want the system job")
	}
	if got.Label != "io.tailmux.cal-worktrees" || !got.System {
		t.Fatalf("portHolder = %+v; want io.tailmux.cal-worktrees as a system job", got)
	}
}

func TestPortHolderNamesAUserJob(t *testing.T) {
	_, user := dirs(t)
	plist(t, user, "com.example.proxy", "127.0.0.1:80")

	got, ok := portHolder(80)
	if !ok || got.Label != "com.example.proxy" || got.System {
		t.Fatalf("portHolder = %+v, %v; want com.example.proxy as a user job", got, ok)
	}
}

func TestPortHolderIgnoresAJobOnAnotherPort(t *testing.T) {
	system, _ := dirs(t)
	plist(t, system, "com.example.elsewhere", "127.0.0.1:8080")

	if got, ok := portHolder(80); ok {
		t.Fatalf("portHolder = %+v; want no match for a job on port 8080", got)
	}
}

// A bare port argument is not a port 80 listener just because "80" appears in
// it; 8080 and 1080 must not match.
func TestPortHolderDoesNotMatchAPortThatMerelyContainsTheDigits(t *testing.T) {
	system, _ := dirs(t)
	for _, listen := range []string{"127.0.0.1:8080", "127.0.0.1:1080", "0.0.0.0:800"} {
		os.RemoveAll(system)
		plist(t, system, "com.example.proxy", listen)
		if got, ok := portHolder(80); ok {
			t.Fatalf("portHolder matched %q as port 80: %+v", listen, got)
		}
	}
}

func TestPortHolderPrefersTheSystemJobWhenBothExist(t *testing.T) {
	system, user := dirs(t)
	plist(t, system, "com.example.system", "127.0.0.1:80")
	plist(t, user, "com.example.user", "127.0.0.1:80")

	got, ok := portHolder(80)
	if !ok || !got.System {
		t.Fatalf("portHolder = %+v, %v; want the system job, which is the one needing root", got, ok)
	}
}

// A job definition says what would listen, not what does: one left on disk
// after its job was booted out still matches, so the wording must not claim
// the named job is the one answering.
func TestHolderDetailDoesNotClaimTheJobIsAnswering(t *testing.T) {
	detail := holderDetail(holder{Label: "io.tailmux.cal-worktrees", System: true})
	if strings.Contains(detail, "held by") {
		t.Fatalf("detail = %q; a plist cannot tell us what is actually answering", detail)
	}
	for _, want := range []string{"io.tailmux.cal-worktrees", "a root LaunchDaemon", "configured to listen"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail = %q; want it to contain %q", detail, want)
		}
	}
}

func TestSystemJobFixNeedsRootAndSurvivesReboot(t *testing.T) {
	fix := holderFix(holder{Label: "io.tailmux.cal-worktrees", System: true})
	for _, want := range []string{
		"sudo launchctl bootout system/io.tailmux.cal-worktrees",
		"sudo launchctl disable system/io.tailmux.cal-worktrees",
		"calport setup port80",
	} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix = %q; want it to contain %q", fix, want)
		}
	}
}

func TestUserJobFixDoesNotAskForRoot(t *testing.T) {
	fix := holderFix(holder{Label: "com.example.proxy"})
	if strings.Contains(fix, "sudo") {
		t.Fatalf("fix = %q; a user job needs no root", fix)
	}
	for _, want := range []string{
		"launchctl bootout gui/$(id -u)/com.example.proxy",
		"launchctl disable gui/$(id -u)/com.example.proxy",
		"calport setup port80",
	} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix = %q; want it to contain %q", fix, want)
		}
	}
}
