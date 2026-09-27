package box

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Real /proc/net lines: 127.0.0.1:7443, 0.0.0.0:6768, an established socket,
// and :::3000 in tcp6.
const procTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1D13 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 00000000:1A70 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1D13 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 20 4 30 10 -1
`
const procTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 444 1 0000000000000000 100 0 0 10 0
`

func TestProcPortsReadsListeningSocketsAndTheirOwners(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "net"), 0o755)
	os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(procTCP), 0o644)
	os.WriteFile(filepath.Join(root, "net", "tcp6"), []byte(procTCP6), 0o644)
	fd := filepath.Join(root, "4242", "fd")
	os.MkdirAll(fd, 0o755)
	os.Symlink("socket:[444]", filepath.Join(fd, "7"))
	os.WriteFile(filepath.Join(root, "4242", "comm"), []byte("next-server\n"), 0o644)
	os.WriteFile(filepath.Join(root, "4242", "cmdline"), []byte("node\x00next\x00dev\x00"), 0o644)

	got, err := procPorts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Port{
		{Port: 3000, Address: "::", PID: 4242, Process: "next-server", Command: "node next dev"},
		{Port: 6768, Address: "0.0.0.0"},
		{Port: 7443, Address: "127.0.0.1"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("port %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseLsof(t *testing.T) {
	out := []byte("p501\ncnode\nn*:3000\nn[::1]:3000\np77\ncpostgres\nn127.0.0.1:5432\n")
	got := mergePorts(parseLsof(out))
	if len(got) != 2 || got[0] != (Port{Port: 3000, Address: "0.0.0.0", PID: 501, Process: "node"}) || got[1].Port != 5432 || got[1].Process != "postgres" {
		t.Fatalf("parseLsof = %+v", got)
	}
}

func TestListPortsFindsARealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	want := ln.Addr().(*net.TCPAddr).Port
	ports, err := ListPorts(context.Background())
	if err != nil {
		t.Skipf("port listing unavailable here: %v", err)
	}
	for _, p := range ports {
		if p.Port == want {
			if p.PID != os.Getpid() {
				t.Fatalf("port %d owned by pid %d, want this test (%d)", want, p.PID, os.Getpid())
			}
			return
		}
	}
	t.Fatalf("listening port %d not reported in %+v", want, ports)
}
