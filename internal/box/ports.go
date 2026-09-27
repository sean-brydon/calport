package box

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Port is a TCP port something on the box is listening on.
type Port struct {
	Port    int    `json:"port"`
	Address string `json:"address"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	Command string `json:"command,omitempty"`
	// Dir is the process's working directory, which says which worktree a
	// dev server belongs to.
	Dir string `json:"dir,omitempty"`
}

// ListPorts reports listening TCP ports. Process details are only visible for
// processes the daemon's user can inspect.
func ListPorts(ctx context.Context) ([]Port, error) {
	if runtime.GOOS == "linux" {
		return procPorts("/proc")
	}
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return mergePorts(parseLsof(out)), nil
}

func procPorts(root string) ([]Port, error) {
	inodes := map[string]Port{}
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile(filepath.Join(root, "net", name))
		if err != nil {
			continue
		}
		for inode, p := range parseProcNet(b) {
			inodes[inode] = p
		}
	}
	owners := socketOwners(root)
	ports := make([]Port, 0, len(inodes))
	for inode, p := range inodes {
		if pid, ok := owners[inode]; ok {
			p.PID = pid
			p.Process, p.Command = describeProcess(root, pid)
			p.Dir, _ = os.Readlink(filepath.Join(root, strconv.Itoa(pid), "cwd"))
		}
		ports = append(ports, p)
	}
	return mergePorts(ports), nil
}

// parseProcNet reads /proc/net/tcp{,6} and returns listening sockets by inode.
func parseProcNet(b []byte) map[string]Port {
	out := map[string]Port{}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Scan() // header
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		hexIP, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil {
			continue
		}
		out[f[9]] = Port{Port: int(port), Address: decodeProcIP(hexIP)}
	}
	return out
}

// decodeProcIP turns the kernel's hex form, little-endian per 32-bit word,
// into an address string.
func decodeProcIP(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return s
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	return net.IP(b).String()
}

func socketOwners(root string) map[string]int {
	owners := map[string]int{}
	procs, _ := os.ReadDir(root)
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(root, p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(root, p.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			if inode, ok := strings.CutPrefix(target, "socket:["); ok {
				owners[strings.TrimSuffix(inode, "]")] = pid
			}
		}
	}
	return owners
}

func describeProcess(root string, pid int) (name, command string) {
	dir := filepath.Join(root, strconv.Itoa(pid))
	if b, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
		name = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		command = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
		if len(command) > 200 {
			command = command[:200] + "…"
		}
	}
	return name, command
}

// parseLsof reads `lsof -Fpcn` output: p<pid>, c<command>, n<address:port>.
func parseLsof(b []byte) []Port {
	var out []Port
	var pid int
	var cmd string
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			cmd = line[1:]
		case 'n':
			i := strings.LastIndex(line, ":")
			if i < 0 {
				continue
			}
			port, err := strconv.Atoi(line[i+1:])
			if err != nil {
				continue
			}
			addr := strings.Trim(line[1:i], "[]")
			if addr == "*" {
				addr = "0.0.0.0"
			}
			out = append(out, Port{Port: port, Address: addr, PID: pid, Process: cmd})
		}
	}
	return out
}

// mergePorts keeps one entry per port, preferring the one that says which
// process owns it and the widest bind address.
func mergePorts(all []Port) []Port {
	best := map[int]Port{}
	for _, p := range all {
		cur, ok := best[p.Port]
		if !ok || (cur.PID == 0 && p.PID != 0) || (cur.PID == p.PID && wider(p.Address, cur.Address)) {
			best[p.Port] = p
		}
	}
	out := make([]Port, 0, len(best))
	for _, p := range best {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func wider(a, b string) bool {
	unspecified := func(s string) bool { ip := net.ParseIP(s); return ip != nil && ip.IsUnspecified() }
	return unspecified(a) && !unspecified(b)
}
