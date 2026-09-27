package box

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCloudflared prints a quick-tunnel banner like the real binary, records
// its arguments, and then runs until killed.
func fakeCloudflared(t *testing.T) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	bin = filepath.Join(dir, "cloudflared")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n" +
		"echo '2026-09-27T20:00:00Z INF |  https://quiet-river-demo.trycloudflare.com  |' >&2\n" +
		"exec sleep 60\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func listening(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestShareLifecycle(t *testing.T) {
	bin, argsFile := fakeCloudflared(t)
	stopped := make(chan Share, 1)
	s := &Shares{Binary: bin, OnStop: func(sh Share) { stopped <- sh }}
	defer s.StopAll()
	port := listening(t)
	sh, err := s.Create(context.Background(), port)
	if err != nil {
		t.Fatal(err)
	}
	if sh.URL != "https://quiet-river-demo.trycloudflare.com" || sh.State != "live" || sh.Port != port {
		t.Fatalf("share = %+v", sh)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"tunnel", "--url http://localhost:", "--http-host-header localhost:"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("cloudflared args %q missing %q", args, want)
		}
	}
	if list := s.List(); len(list) != 1 || list[0].ID != sh.ID {
		t.Fatalf("list = %+v", list)
	}
	if _, err := s.Remove(sh.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("removed share still listed")
	}
	select {
	case got := <-stopped:
		t.Fatalf("an explicit removal was reported as an unexpected stop: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := s.Remove(sh.ID); !errors.Is(err, ErrUnknownShare) {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestATunnelThatDiesIsReported(t *testing.T) {
	bin, _ := fakeCloudflared(t)
	stopped := make(chan Share, 1)
	s := &Shares{Binary: bin, OnStop: func(sh Share) { stopped <- sh }}
	sh, err := s.Create(context.Background(), listening(t))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.items[sh.ID].cmd.Process.Kill()
	s.mu.Unlock()
	select {
	case got := <-stopped:
		if got.ID != sh.ID || got.State != "stopped" {
			t.Fatalf("stop report = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a dead tunnel was never reported")
	}
	if len(s.List()) != 0 {
		t.Fatal("a dead tunnel is still listed as a share")
	}
}

func TestSharingAClosedPortOrWithoutCloudflaredFails(t *testing.T) {
	bin, _ := fakeCloudflared(t)
	s := &Shares{Binary: bin}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := s.Create(context.Background(), closed); err == nil || !strings.Contains(err.Error(), "nothing is listening") {
		t.Fatalf("sharing a closed port: %v", err)
	}
	missing := &Shares{Binary: filepath.Join(t.TempDir(), "no-such-binary")}
	if _, err := missing.Create(context.Background(), listening(t)); err == nil {
		t.Fatal("shared without a working cloudflared")
	}
}
