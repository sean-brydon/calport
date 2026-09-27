package box

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

func fakeProc(t *testing.T) string {
	t.Helper()
	proc := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(proc, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("uptime", "3600.52 7000.00\n")
	write("loadavg", "0.50 0.75 1.25 2/300 4242\n")
	write("meminfo", "MemTotal:       16000000 kB\nMemFree:         1000000 kB\nMemAvailable:    6000000 kB\nSwapTotal:       2000000 kB\nSwapFree:        1500000 kB\n")
	agent := func(pid int, comm, cwd string) {
		dir := filepath.Join(proc, strconv.Itoa(pid))
		write(filepath.Join(strconv.Itoa(pid), "comm"), comm+"\n")
		os.Symlink(cwd, filepath.Join(dir, "cwd"))
	}
	agent(100, "claude", "/home/alex/work/cal-fix-login")
	agent(101, "codex", "/home/alex/work/cal (deleted)")
	agent(102, "zsh", "/home/alex")
	agent(103, "agent", "/home/alex")
	return proc
}

func TestStatsReadMemoryLoadAndAgents(t *testing.T) {
	s := collectStats(fakeProc(t))
	if s.Uptime != 3600 || len(s.Load) != 3 || s.Load[2] != 1.25 {
		t.Fatalf("uptime/load = %d %v", s.Uptime, s.Load)
	}
	if s.Memory.Total != 16000000*1024 || s.Memory.Used != 10000000*1024 {
		t.Fatalf("memory = %+v; used must count caches as free", s.Memory)
	}
	if s.Swap.Used != 500000*1024 {
		t.Fatalf("swap = %+v", s.Swap)
	}
	if len(s.Agents) != 2 || s.Agents[0].Tool != "claude" || s.Agents[1].Tool != "codex" || s.Agents[1].Path != "/home/alex/work/cal" {
		t.Fatalf("agents = %+v", s.Agents)
	}
	if len(s.Disks) == 0 || s.Disks[0].Mount != "/" || s.Disks[0].Total == 0 {
		t.Fatalf("disks = %+v", s.Disks)
	}
}

func TestAgentStatesFollowHookEvents(t *testing.T) {
	var a AgentStates
	a.observe(events.Event{Type: "agent.waiting", Time: time.Now(), Data: map[string]any{"path": "/w/a/"}})
	a.observe(events.Event{Type: "agent.finished", Data: map[string]any{"path": "/w/b"}})
	a.observe(events.Event{Type: "worktree.created", Data: map[string]any{"path": "/w/c"}})
	if st, ok := a.get("/w/a"); !ok || st.state != "waiting" {
		t.Fatalf("/w/a = %+v %v", st, ok)
	}
	if st, _ := a.get("/w/b"); st.state != "finished" {
		t.Fatalf("/w/b = %+v", st)
	}
	if _, ok := a.get("/w/c"); ok {
		t.Fatal("a non-agent event set an agent state")
	}
	a.observe(events.Event{Type: "agent.started", Data: map[string]any{"path": "/w/a"}})
	if st, _ := a.get("/w/a"); st.state != "running" {
		t.Fatalf("a new session did not clear waiting: %+v", st)
	}
}

func TestWorktreeForPicksTheDeepest(t *testing.T) {
	locs := []Location{{Name: "cal", Worktrees: []Worktree{{Name: "cal", Path: "/w/cal"}, {Name: "fix", Path: "/w/cal/.worktrees/fix"}}}}
	if l, w := worktreeFor(locs, "/w/cal/.worktrees/fix/apps/web"); l != "cal" || w != "fix" {
		t.Fatalf("got %s/%s", l, w)
	}
	if l, w := worktreeFor(locs, "/w/calendar"); l != "" || w != "" {
		t.Fatalf("a sibling directory matched: %s/%s", l, w)
	}
}
