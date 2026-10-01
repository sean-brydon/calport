package box

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr installs a herdr that answers from files in its own directory:
// version, running (one running session per line), and agents-<session>
// (that session's agent count). It logs every stop and update to calls, and
// an update moves version to 0.9.3 unless decline exists.
func fakeHerdr(t *testing.T, version string, running []string, agents map[string]int) string {
	t.Helper()
	bin := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("version", version)
	write("running", strings.Join(running, "\n")+"\n")
	for s, n := range agents {
		write("agents-"+s, strings.Repeat(`{"agent":"claude"},`, n))
	}
	script := `#!/bin/sh
d="$(dirname "$0")"
case "$1 $2" in
"--version ") echo "herdr $(cat "$d/version")" ;;
"session list")
  printf '{"sessions":[{"name":"default","running":false}'
  while read -r s; do [ -n "$s" ] && printf ',{"name":"%s","running":true}' "$s"; done < "$d/running"
  echo ']}' ;;
"agent list") a="$(cat "$d/agents-$HERDR_SESSION" 2>/dev/null)"; echo "{\"result\":{\"agents\":[${a%,}]}}" ;;
"session stop") echo "stop $3" >> "$d/calls"; grep -vx "$3" "$d/running" > "$d/running.new"; mv "$d/running.new" "$d/running" ;;
"update ")
  echo update >> "$d/calls"
  if [ -f "$d/decline" ]; then echo "Herdr was not updated."; else echo 0.9.3 > "$d/version"; echo "installed 0.9.3"; fi ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func calls(bin string) string {
	b, _ := os.ReadFile(filepath.Join(bin, "calls"))
	return strings.TrimSpace(string(b))
}

func TestHerdrStatusCountsAgentsInRunningSessions(t *testing.T) {
	fakeHerdr(t, "0.8.2", []string{"agents"}, map[string]int{"agents": 2})
	s, err := herdrStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Installed || s.Version != "0.8.2" || !s.Updatable || s.User == "" {
		t.Fatalf("status = %+v", s)
	}
	want := []HerdrSession{{Name: "default"}, {Name: "agents", Running: true, Agents: 2}}
	if len(s.Sessions) != 2 || s.Sessions[0] != want[0] || s.Sessions[1] != want[1] {
		t.Fatalf("sessions = %+v, want %+v", s.Sessions, want)
	}
}

func TestHerdrUpdateStopsIdleSessionsThenUpdates(t *testing.T) {
	bin := fakeHerdr(t, "0.8.2", []string{"agents", "scratch"}, nil)
	u, err := updateHerdr(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := calls(bin); got != "stop agents\nstop scratch\nupdate" {
		t.Fatalf("calls = %q", got)
	}
	if u.From != "0.8.2" || u.Status.Version != "0.9.3" || strings.Join(u.Stopped, ",") != "agents,scratch" {
		t.Fatalf("update = %+v", u)
	}
}

// Stopping a session ends whatever runs in it, so an agent anywhere means no
// session is stopped and nothing is updated.
func TestHerdrUpdateRefusesWhileAnAgentRuns(t *testing.T) {
	bin := fakeHerdr(t, "0.8.2", []string{"scratch", "agents"}, map[string]int{"agents": 1})
	_, err := updateHerdr(context.Background())
	var he httpError
	if !errors.As(err, &he) || !strings.Contains(err.Error(), "agents (1)") {
		t.Fatalf("err = %v, want a conflict naming the busy session", err)
	}
	if got := calls(bin); got != "" {
		t.Fatalf("it acted anyway: %q", got)
	}
}

// herdr exits 0 when it declines to update; that must not read as success.
func TestHerdrUpdateReportsADeclinedUpdate(t *testing.T) {
	bin := fakeHerdr(t, "0.8.2", nil, nil)
	os.WriteFile(filepath.Join(bin, "decline"), nil, 0o644)
	if _, err := updateHerdr(context.Background()); err == nil || !strings.Contains(err.Error(), "not updated") {
		t.Fatalf("err = %v", err)
	}
}

func TestHerdrUpdateLeavesAPackageManagersHerdrAlone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	bin := fakeHerdr(t, "0.8.2", []string{"agents"}, nil)
	os.Chmod(bin, 0o555)
	t.Cleanup(func() { os.Chmod(bin, 0o755) })
	_, err := updateHerdr(context.Background())
	if err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("err = %v", err)
	}
	if got := calls(bin); got != "" {
		t.Fatalf("it acted anyway: %q", got)
	}
}

func TestHerdrStatusWithoutHerdr(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	s, err := herdrStatus(context.Background())
	if err != nil || s.Installed || s.Sessions == nil {
		t.Fatalf("status = %+v, %v", s, err)
	}
}
