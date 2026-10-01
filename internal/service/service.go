// Package service installs a calport process as a per-user OS service: a
// launchd agent on macOS or a systemd user unit on Linux. The service starts
// at login, restarts after a crash, and stays stopped after a clean exit, so a
// deliberate stop is never fought by the supervisor.
package service

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Spec struct {
	// Name is the launchd label and the systemd unit name without ".service".
	Name        string
	Description string
	Program     string
	Args        []string
	Env         map[string]string
	LogPath     string
	// KeepChildren leaves processes the service started running when it
	// stops or restarts, the way sshd keeps SSH sessions: calportd's agent
	// sessions must survive the daemon being upgraded.
	KeepChildren bool
	// RestartAlways brings the service back even when it exits cleanly, for
	// something whose job is to stay up. calportd leaves this off: it exits
	// only when something is wrong, and restarting it forever would bury the
	// reason. A program run as a managed unit can shut itself down on purpose
	// and still need to come back.
	RestartAlways bool
}

// These are replaced in tests.
var (
	goos    = runtime.GOOS
	homeDir = os.UserHomeDir
	command = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
)

func unitPath(s Spec) (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", s.Name+".plist"), nil
	case "linux":
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "systemd", "user", s.Name+".service"), nil
	}
	return "", fmt.Errorf("services are supported on macOS and Linux, not %s", goos)
}

// Render produces the unit file for the current platform.
func Render(s Spec) ([]byte, error) {
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	switch goos {
	case "darwin":
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
`)
		fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n<key>ProgramArguments</key><array>\n", esc(s.Name))
		for _, arg := range append([]string{s.Program}, s.Args...) {
			fmt.Fprintf(&b, "<string>%s</string>\n", esc(arg))
		}
		b.WriteString("</array>\n<key>EnvironmentVariables</key><dict>\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "<key>%s</key><string>%s</string>\n", esc(k), esc(s.Env[k]))
		}
		b.WriteString(`</dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>5</integer>
<key>ProcessType</key><string>Background</string>
`)
		if s.KeepChildren {
			b.WriteString("<key>AbandonProcessGroup</key><true/>\n")
		}
		if s.LogPath != "" {
			fmt.Fprintf(&b, "<key>StandardOutPath</key><string>%s</string>\n<key>StandardErrorPath</key><string>%s</string>\n", esc(s.LogPath), esc(s.LogPath))
		}
		b.WriteString("</dict></plist>\n")
		return b.Bytes(), nil
	case "linux":
		var b bytes.Buffer
		fmt.Fprintf(&b, "[Unit]\nDescription=%s\nAfter=network-online.target\n\n[Service]\nType=simple\n", s.Description)
		fmt.Fprintf(&b, "ExecStart=%s\n", systemdArgs(append([]string{s.Program}, s.Args...)))
		for _, k := range keys {
			fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+s.Env[k]))
		}
		// systemd 240+. Keeping a unit's output in a file calportd owns keeps
		// credentials a program prints out of the journal, which is readable
		// by the box user and persists.
		if s.LogPath != "" {
			fmt.Fprintf(&b, "StandardOutput=append:%s\nStandardError=append:%s\n", s.LogPath, s.LogPath)
		}
		policy := "on-failure"
		if s.RestartAlways {
			policy = "always"
		}
		fmt.Fprintf(&b, "Restart=%s\nRestartSec=5\n", policy)
		if s.KeepChildren {
			b.WriteString("KillMode=process\n")
		}
		b.WriteString("\n[Install]\nWantedBy=default.target\n")
		return b.Bytes(), nil
	}
	return nil, fmt.Errorf("services are supported on macOS and Linux, not %s", goos)
}

func esc(s string) string { return html.EscapeString(s) }

func systemdArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = systemdQuote(a)
	}
	return strings.Join(quoted, " ")
}

func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

// Installed reports whether the unit on disk is exactly the one Render would
// write, so a unit left behind for another binary or home does not count.
func Installed(s Spec) bool {
	path, err := unitPath(s)
	if err != nil {
		return false
	}
	have, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	want, err := Render(s)
	return err == nil && bytes.Equal(have, want)
}

// InstalledByName reports whether a unit file with this name exists at all,
// whatever it contains.
//
// This exists alongside Installed because the two answer different questions
// and neither can stand in for the other. Installed is for a caller that holds
// the Spec it is about to write and wants to know "is the unit on disk already
// exactly mine?". InstalledByName is for a caller that holds only a name -
// calportd's managed units are named over the wire, and the Spec they were
// written from lives on the box - where an exact compare would render an empty
// Spec and report every installed unit as missing.
func InstalledByName(name string) bool {
	path, err := unitPath(Spec{Name: name})
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Install writes the unit and (re)loads it, which starts the service.
func Install(s Spec) (string, error) {
	if !filepath.IsAbs(s.Program) {
		return "", fmt.Errorf("service program must be an absolute path, got %s", s.Program)
	}
	// The supervisor re-executes this path on every restart; a binary from
	// `go run` or another temporary build disappears.
	if strings.HasPrefix(s.Program, os.TempDir()) || strings.Contains(s.Program, "/go-build") {
		return "", fmt.Errorf("refusing to install a temporary binary %s; build it to a stable path first", s.Program)
	}
	path, err := unitPath(s)
	if err != nil {
		return "", err
	}
	data, err := Render(s)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, load(s, path)
}

func Uninstall(s Spec) (string, error) {
	path, err := unitPath(s)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	unload(s)
	return path, os.Remove(path)
}

// Start asks the supervisor to start the service if it is not running.
// Running reports whether the service is up right now, which is not the same
// question as Installed: a unit can be written and enabled and still be dead,
// and telling those apart is the difference between "it is configured" and
// "it is working".
func Running(s Spec) bool {
	if goos == "darwin" {
		out, err := command("launchctl", "print", launchdTarget(s))
		return err == nil && strings.Contains(string(out), "state = running")
	}
	out, err := command("systemctl", "--user", "is-active", s.Name+".service")
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func Start(s Spec) error {
	var out []byte
	var err error
	if goos == "darwin" {
		out, err = command("launchctl", "kickstart", launchdTarget(s))
	} else {
		out, err = command("systemctl", "--user", "start", s.Name+".service")
	}
	if err != nil {
		return fmt.Errorf("starting %s: %v: %s", s.Name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func launchdTarget(s Spec) string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), s.Name) }

func load(s Spec, path string) error {
	if goos == "darwin" {
		command("launchctl", "bootout", launchdTarget(s))
		if out, err := command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if out, err := command("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// restart, not start: a reinstall must pick up a changed binary or args.
	if out, err := command("systemctl", "--user", "enable", s.Name+".service"); err != nil {
		return fmt.Errorf("systemctl enable: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := command("systemctl", "--user", "restart", s.Name+".service"); err != nil {
		return fmt.Errorf("systemctl restart: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func unload(s Spec) {
	if goos == "darwin" {
		command("launchctl", "bootout", launchdTarget(s))
		return
	}
	command("systemctl", "--user", "disable", "--now", s.Name+".service")
}
