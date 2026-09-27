package box

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseSessions(t *testing.T) {
	out := []byte("cal-claude\t1759000000\t1\tcal/billing\tclaude\t0\t/home/alex/work/cal-billing\nold\t1759000100\t0\t\t\t1\t/tmp\n")
	got := parseSessions(out)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "cal-claude" || got[0].Location != "cal/billing" || got[0].Command != "claude" || got[0].Attached != 1 || got[0].Exited || got[0].Dir != "/home/alex/work/cal-billing" {
		t.Errorf("first session = %+v", got[0])
	}
	if !got[1].Exited || got[1].Attached != 0 {
		t.Errorf("second session = %+v", got[1])
	}
}

// testSessions isolates tmux under a private TMUX_TMPDIR so tests never touch
// the developer's own tmux servers.
func testSessions(t *testing.T) *Sessions {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tmp, err := os.MkdirTemp("/tmp", "cpt")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", tmp)
	t.Setenv("TMUX", "")
	t.Setenv("SHELL", "/bin/sh")
	s, err := NewSessions(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "-L", tmuxSocket, "kill-server").Run()
		os.RemoveAll(tmp)
	})
	return s
}

func TestSessionsLifecycle(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	if all, err := s.List(ctx); err != nil || len(all) != 0 {
		t.Fatalf("empty list = %+v, %v", all, err)
	}
	dir := t.TempDir()
	sess, err := s.Create(ctx, "agent-1", "cal/billing", dir, "echo started-in-$(pwd); sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if sess.Location != "cal/billing" || (sess.Dir != dir && sess.Dir != resolved) || sess.Exited {
		t.Fatalf("created session = %+v", sess)
	}
	if _, err := s.Create(ctx, "agent-1", "", dir, "true"); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate session: %v", err)
	}
	for _, bad := range []string{"has space", "a.b", "a:b", ""} {
		if _, err := s.Create(ctx, bad, "", dir, "true"); err == nil {
			t.Errorf("created session named %q", bad)
		}
	}
	if err := s.Kill(ctx, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "agent-1"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("killed session still listed: %v", err)
	}
}

func TestAFinishedProgramStaysVisible(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "quick", "", t.TempDir(), "echo done"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		sess, err := s.Get(ctx, "quick")
		if err != nil {
			t.Fatalf("a finished session disappeared: %v", err)
		}
		if sess.Exited {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("session never reported its program as exited")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAttachShowsTheSessionAndCarriesKeystrokes(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "echoer", "", t.TempDir(), "cat"); err != nil {
		t.Fatal(err)
	}
	master, cmd, err := s.Attach(ctx, "echoer", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	defer master.Close()
	var out syncBuffer
	done := make(chan struct{})
	go func() { io.Copy(&out, master); close(done) }()
	time.Sleep(300 * time.Millisecond)
	master.Write([]byte("typed-through-calport\r"))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "typed-through-calport") {
		if time.Now().After(deadline) {
			t.Fatalf("keystrokes never echoed; screen: %q", out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// syncBuffer is a bytes.Buffer safe to poll while another goroutine copies
// terminal output into it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestScreenShowsTheSessionsOutput(t *testing.T) {
	s := testSessions(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "shows", "", t.TempDir(), "echo visible-output; sleep 30"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, err := s.Screen(ctx, "shows", 100)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "visible-output") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen = %q", text)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
