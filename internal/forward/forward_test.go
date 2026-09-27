package forward

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func echo(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// tcpDial stands in for a box: it dials the port directly on this machine.
func tcpDial(ctx context.Context, port int) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestForwardCarriesDataAndHalfCloseOnBothLoopbacks(t *testing.T) {
	remote := echo(t)
	local := freePort(t)
	lns, err := Listen(local)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, ln := range lns {
		go Serve(ctx, ln, remote, tcpDial, nil)
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		if host == "::1" && len(lns) < 2 {
			continue
		}
		conn, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(local)))
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte("hi " + host))
		conn.(*net.TCPConn).CloseWrite()
		got, err := io.ReadAll(conn)
		conn.Close()
		if err != nil || string(got) != "hi "+host {
			t.Fatalf("%s: echo = %q, %v", host, got, err)
		}
	}
}

func TestAPortHeldOnIPv6ByAnotherProgramIsRefused(t *testing.T) {
	other, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback")
	}
	defer other.Close()
	port := other.Addr().(*net.TCPAddr).Port
	lns, err := Listen(port)
	if err == nil {
		for _, ln := range lns {
			ln.Close()
		}
		t.Fatal("a port shadowed on ::1 by another program was accepted")
	}
	if !strings.Contains(err.Error(), "::1") {
		t.Fatalf("error does not explain the IPv6 conflict: %v", err)
	}
	// The IPv4 half must have been released again.
	if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err != nil {
		t.Fatalf("refused forward leaked its IPv4 listener: %v", err)
	} else {
		ln.Close()
	}
}

func TestADialFailureClosesTheClientAndIsReported(t *testing.T) {
	local := freePort(t)
	lns, err := Listen(local)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reported atomic.Int32
	failing := func(context.Context, int) (net.Conn, error) { return nil, errors.New("box offline") }
	for _, ln := range lns {
		go Serve(ctx, ln, 1, failing, func(error) { reported.Add(1) })
	}
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("client was left hanging instead of closed: %v", err)
	}
	if reported.Load() != 1 {
		t.Fatalf("dial failure reported %d times, want 1", reported.Load())
	}
}

func TestCancellingServeFreesThePortAndEndsOpenConnections(t *testing.T) {
	remote := echo(t)
	local := freePort(t)
	lns, err := Listen(local)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		for _, ln := range lns[1:] {
			go Serve(ctx, ln, remote, tcpDial, nil)
		}
		Serve(ctx, lns[0], remote, tcpDial, nil)
		close(done)
	}()
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("x"))
	io.ReadFull(conn, make([]byte, 1))
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel while a connection was open")
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("open connection survived cancelling the forward")
	}
	if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local))); err != nil {
		t.Fatalf("port not released after cancel: %v", err)
	} else {
		ln.Close()
	}
}
