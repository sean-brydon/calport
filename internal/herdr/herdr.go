// Package herdr adds calport's boxes to the Herdr on this computer. Herdr
// reaches other machines over SSH only, so calport writes an SSH host per box
// that travels over calport's own network: boxes on a tailnet this computer
// is not on work too. Keys stay the user's: calport writes how to reach a
// box, never how to log in to it.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MinVersion is the first Herdr with saved SSH machines, on either end.
const MinVersion = "0.9.0"

// Host is the SSH host calport writes for a box.
func Host(box string) string { return "calport-" + box }

// SSHHost is how SSH reaches one box.
type SSHHost struct {
	Box     string
	Address string // the box's address, without its port
	User    string
	// Network is the calport network the box is reached through; empty means
	// this computer's own, which SSH reaches directly.
	Network string
	// Calport is the calport executable that carries the connection.
	Calport string
}

func (h SSHHost) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by calport herdr setup for the box %s; it is rewritten there.\n", h.Box)
	fmt.Fprintf(&b, "Host %s\n  HostName %s\n", Host(h.Box), h.Address)
	if h.User != "" {
		fmt.Fprintf(&b, "  User %s\n", h.User)
	}
	if h.Network != "" {
		fmt.Fprintf(&b, "  ProxyCommand %s network proxy %s %%h %%p\n", shellQuote(h.Calport), shellQuote(h.Network))
	}
	return b.String()
}

// includeLine is what ~/.ssh/config needs for the hosts to exist. OpenSSH
// reads options first-come, so it goes above every Host block.
const includeLine = "Include calport/*.conf"

// Config is the SSH configuration directory, normally ~/.ssh.
type Config struct{ Dir string }

func DefaultConfig() (Config, error) {
	home, err := os.UserHomeDir()
	return Config{Dir: filepath.Join(home, ".ssh")}, err
}

// Write saves one box's host, leaving every other box's alone.
func (c Config) Write(h SSHHost) error {
	dir := filepath.Join(c.Dir, "calport")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, h.Box+".conf"), []byte(h.Render()), 0o600)
}

func (c Config) Included() bool {
	b, err := os.ReadFile(filepath.Join(c.Dir, "config"))
	return err == nil && hasLine(string(b), includeLine)
}

// EnsureInclude puts the Include at the top of ~/.ssh/config, backing the
// file up first the one time it changes.
func (c Config) EnsureInclude() error {
	path := filepath.Join(c.Dir, "config")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hasLine(string(old), includeLine) {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	if len(old) > 0 {
		if err := os.WriteFile(path+".calport-backup", old, 0o600); err != nil {
			return err
		}
	}
	head := "# Added by calport: an SSH host per paired box, named calport-<box>.\n# It only works above any Host block.\n" + includeLine + "\n\n"
	return os.WriteFile(path, append([]byte(head), old...), 0o600)
}

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// CheckSSH logs in to host once without prompting, which is how Herdr's
// background connections log in too.
func CheckSSH(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, "true").CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// CLI runs the Herdr on this computer.
type CLI struct{ Path string }

// Find locates herdr. The desktop app starts calport with the short PATH
// macOS gives apps, which leaves out where Herdr installs itself.
func Find() (CLI, error) {
	if p, err := exec.LookPath("herdr"); err == nil {
		return CLI{Path: p}, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "herdr"), "/opt/homebrew/bin/herdr", "/usr/local/bin/herdr"} {
		if _, err := os.Stat(p); err == nil {
			return CLI{Path: p}, nil
		}
	}
	return CLI{}, errors.New("herdr is not installed on this computer; see herdr.dev")
}

func (c CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, c.Path, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("herdr %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("herdr %s: %w", args[0], err)
	}
	return out, nil
}

func (c CLI) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "--version")
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "herdr "), err
}

type Machine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Target  string `json:"target"`
	Session string `json:"session"`
	Enabled bool   `json:"enabled"`
}

func (c CLI) Machines(ctx context.Context) ([]Machine, error) {
	out, err := c.run(ctx, "machine", "list", "--json")
	if err != nil {
		return nil, err
	}
	var ms []Machine
	if err := json.Unmarshal(out, &ms); err != nil {
		return nil, fmt.Errorf("herdr machine list returned unexpected output: %.200s", out)
	}
	return ms, nil
}

type MachineStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

// MachineStatuses checks every saved machine without prompting.
func (c CLI) MachineStatuses(ctx context.Context) (map[string]MachineStatus, error) {
	out, err := c.run(ctx, "machine", "status", "--json")
	if err != nil {
		return nil, err
	}
	var list []MachineStatus
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("herdr machine status returned unexpected output: %.200s", out)
	}
	byID := make(map[string]MachineStatus, len(list))
	for _, s := range list {
		byID[s.ID] = s
	}
	return byID, nil
}

// AddMachine saves target in Herdr, starting session on it if it is not
// running. Without a terminal attached Herdr never prompts; it fails instead.
func (c CLI) AddMachine(ctx context.Context, target, label, session string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, err := c.run(ctx, "machine", "add", target, "--remote-session", session, "--label", label)
	return err
}

// Older reports whether version v is older than min. Both are dotted
// numbers, missing parts count as 0, and anything after a dash, such as a
// preview tag, is ignored. A version that is not a number at all is older.
func Older(v, min string) bool {
	a, b := parts(v), parts(min)
	if len(a) == 0 {
		return true
	}
	for i := range max(len(a), len(b)) {
		x, y := at(a, i), at(b, i)
		if x != y {
			return x < y
		}
	}
	return false
}

func at(p []int, i int) int {
	if i < len(p) {
		return p[i]
	}
	return 0
}

func parts(v string) []int {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
