package box

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/statefile"
	"github.com/sean-brydon/calport/internal/terminal"
)

// Session is a long-running program, usually a coding agent, started at a
// location on the box. It keeps running when no one is attached.
type Session struct {
	Name     string    `json:"name"`
	Location string    `json:"location,omitempty"`
	Dir      string    `json:"dir"`
	Command  string    `json:"command,omitempty"`
	Created  time.Time `json:"created"`
	Attached int       `json:"attached"`
	Exited   bool      `json:"exited"`
}

var (
	ErrUnknownSession = errors.New("no session with that name")
	ErrSessionExists  = errors.New("a session with that name already exists")
	sessionName       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
)

// Sessions runs programs in calport's own tmux server, separate from any tmux
// the user runs. Finished programs stay visible until killed, so an agent's
// last output is never lost.
type Sessions struct {
	// Config is the tmux configuration file for calport's server.
	Config string
}

const tmuxSocket = "calport"

const tmuxConfig = `set -g remain-on-exit on
set -g history-limit 50000
set -g mouse on
set -g default-terminal "tmux-256color"
`

func NewSessions(dir string) (*Sessions, error) {
	path := filepath.Join(dir, "tmux.conf")
	if b, err := os.ReadFile(path); err != nil || string(b) != tmuxConfig {
		if err := statefile.Write(path, []byte(tmuxConfig)); err != nil {
			return nil, err
		}
	}
	return &Sessions{Config: path}, nil
}

func (s *Sessions) tmux(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-L", tmuxSocket, "-f", s.Config}, args...)...)
	return cmd.CombinedOutput()
}

const listFormat = "#{session_name}\t#{session_created}\t#{session_attached}\t#{@calport_location}\t#{@calport_command}\t#{pane_dead}\t#{pane_start_path}"

func (s *Sessions) List(ctx context.Context) ([]Session, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, errors.New("tmux is not installed on this box")
	}
	out, err := s.tmux(ctx, "list-sessions", "-F", listFormat)
	if err != nil {
		// No server yet simply means no sessions.
		if strings.Contains(string(out), "no server running") || strings.Contains(string(out), "error connecting") {
			return []Session{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %s", strings.TrimSpace(string(out)))
	}
	return parseSessions(out), nil
}

func parseSessions(out []byte) []Session {
	sessions := []Session{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			continue
		}
		created, _ := strconv.ParseInt(f[1], 10, 64)
		attached, _ := strconv.Atoi(f[2])
		sessions = append(sessions, Session{
			Name:     f[0],
			Created:  time.Unix(created, 0).UTC(),
			Attached: attached,
			Location: f[3],
			Command:  f[4],
			Exited:   f[5] == "1",
			Dir:      f[6],
		})
	}
	return sessions
}

// Create starts command in dir. An empty command starts the user's shell.
// Commands run through a login shell, so tools the user installed (claude,
// codex, orca) are on PATH even when calportd runs under systemd.
func (s *Sessions) Create(ctx context.Context, name, location, dir, command string) (Session, error) {
	if !sessionName.MatchString(name) {
		return Session{}, fmt.Errorf("invalid session name %q: use letters, digits, - and _", name)
	}
	if _, err := s.tmux(ctx, "has-session", "-t", "="+name); err == nil {
		return Session{}, ErrSessionExists
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	argv := []string{shell, "-l"}
	if command != "" {
		argv = []string{shell, "-lc", command}
	}
	args := append([]string{"new-session", "-d", "-s", name, "-c", dir, "-x", "200", "-y", "50", "--"}, argv...)
	if out, err := s.tmux(ctx, args...); err != nil {
		return Session{}, fmt.Errorf("tmux new-session: %s", strings.TrimSpace(string(out)))
	}
	// set-option takes a pane target, whose exact-match form needs the colon.
	s.tmux(ctx, "set-option", "-t", "="+name+":", "@calport_location", location)
	s.tmux(ctx, "set-option", "-t", "="+name+":", "@calport_command", command)
	return s.Get(ctx, name)
}

func (s *Sessions) Get(ctx context.Context, name string) (Session, error) {
	all, err := s.List(ctx)
	if err != nil {
		return Session{}, err
	}
	for _, sess := range all {
		if sess.Name == name {
			return sess, nil
		}
	}
	return Session{}, ErrUnknownSession
}

func (s *Sessions) Kill(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	if out, err := s.tmux(ctx, "kill-session", "-t", "="+name); err != nil {
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Attach runs a tmux client for the session under a new pseudo-terminal. The
// caller relays the returned master to the laptop; closing it detaches.
func (s *Sessions) Attach(ctx context.Context, name string, cols, rows int) (*os.File, *exec.Cmd, error) {
	if _, err := s.Get(ctx, name); err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, "tmux", "-L", tmuxSocket, "-f", s.Config, "attach-session", "-t", "="+name)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	master, err := terminal.Start(cmd, cols, rows)
	if err != nil {
		return nil, nil, err
	}
	return master, cmd, nil
}

// Screen returns what the session shows, plus up to history earlier lines,
// so tools can read an agent's output without attaching.
func (s *Sessions) Screen(ctx context.Context, name string, history int) (string, error) {
	if _, err := s.Get(ctx, name); err != nil {
		return "", err
	}
	out, err := s.tmux(ctx, "capture-pane", "-p", "-J", "-t", "="+name+":", "-S", "-"+strconv.Itoa(max(history, 0)))
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n") + "\n", nil
}
