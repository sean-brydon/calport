// Package hooks runs user commands when calport events happen, which is how
// calport drives other tools (Orca, Herdr, Cursor, notifications).
//
// Loops are the risk when integrations run in both directions: calport creates
// a worktree, a hook tells Orca, Orca tells calport, and so on. A hook names
// the tool it drives; events that came from that tool never trigger it, and
// everything the hook does in calport is stamped with that tool as its origin.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/events"
)

type Hook struct {
	// On is an event type, a prefix pattern such as "worktree.*", or "*".
	On string `json:"on"`
	// Run is a shell command. It gets the event as JSON on stdin and as
	// CALPORT_* environment variables.
	Run string `json:"run"`
	// Tool is the tool this hook drives. Events from it are skipped.
	Tool    string `json:"tool,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

type Config struct {
	Hooks []Hook `json:"hooks"`
}

// OriginEnv carries the origin into commands hooks run, so calport requests
// they make are attributed to the tool rather than to calport itself.
const OriginEnv = "CALPORT_ORIGIN"

// Matches reports whether h should run for e.
func Matches(h Hook, e events.Event) bool {
	if h.Run == "" {
		return false
	}
	if h.Tool != "" && e.Origin == h.Tool {
		return false
	}
	switch {
	case h.On == "*":
		return true
	case strings.HasSuffix(h.On, ".*"):
		return strings.HasPrefix(e.Type, strings.TrimSuffix(h.On, "*"))
	}
	return h.On == e.Type
}

// Env describes e to a hook command.
func Env(e events.Event, tool string) []string {
	origin := tool
	if origin == "" {
		origin = "hook"
	}
	env := []string{
		"CALPORT_EVENT=" + e.Type,
		"CALPORT_EVENT_BOX=" + e.Box,
		"CALPORT_EVENT_ORIGIN=" + e.Origin,
		OriginEnv + "=" + origin,
	}
	for k, v := range e.Data {
		key := strings.ToUpper(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return '_'
		}, k))
		env = append(env, fmt.Sprintf("CALPORT_%s=%v", key, v))
	}
	return env
}

// Runner re-reads its config for every event, so edits apply without a
// restart, and runs matching hooks one at a time off the event path.
type Runner struct {
	Path string
	Log  *log.Logger
}

func (r *Runner) Run(ctx context.Context, bus *events.Bus) {
	ch, stop := bus.Subscribe()
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			r.handle(ctx, e)
		}
	}
}

func (r *Runner) load() (Config, error) {
	var c Config
	b, err := os.ReadFile(r.Path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", r.Path, err)
	}
	return c, nil
}

func (r *Runner) handle(ctx context.Context, e events.Event) {
	cfg, err := r.load()
	if err != nil {
		r.logf("hooks: %v", err)
		return
	}
	for _, h := range cfg.Hooks {
		if Matches(h, e) {
			r.exec(ctx, h, e)
		}
	}
}

func (r *Runner) exec(ctx context.Context, h Hook, e events.Event) {
	timeout := time.Minute
	if d, err := time.ParseDuration(h.Timeout); err == nil && d > 0 {
		timeout = d
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload, _ := json.Marshal(e)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", h.Run)
	cmd.Env = append(os.Environ(), Env(e, h.Tool)...)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.logf("hook %q for %s failed: %v: %s", h.On, e.Type, err, strings.TrimSpace(string(out)))
		return
	}
	r.logf("hook %q ran for %s", h.On, e.Type)
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log.Printf(format, args...)
	}
}
