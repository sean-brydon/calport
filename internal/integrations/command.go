package integrations

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sean-brydon/calport/internal/events"
)

const Usage = `Integrations
  %[1]s integrations install claude|cursor|codex|all
                         Install the calport skill and agent hooks for a tool
  %[1]s hook TOOL EVENT  What those hooks run: turns a tool's hook into a
                         calport event (agent.finished, agent.waiting)
`

// Emit publishes an event on this machine's calport: the laptop agent or the
// box daemon, whichever the running binary talks to.
type Emit func(events.Event) error

// Hook handles `hook TOOL EVENT [PAYLOAD]`. It never fails the calling tool:
// problems are reported on stderr and the tool's expected reply is printed.
func Hook(args []string, stdin *os.File, stdout, stderr io.Writer, emit Emit) {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: hook TOOL EVENT [PAYLOAD]")
		return
	}
	tool, event := args[0], args[1]
	defer fmt.Fprint(stdout, Reply(tool))
	var payload []byte
	if len(args) >= 3 {
		payload = []byte(args[2])
	} else if info, err := stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		// Read stdin only when something is piped in: a terminal would block.
		payload, _ = io.ReadAll(io.LimitReader(stdin, 1<<20))
	}
	e, ok := Translate(tool, event, payload)
	if !ok {
		return
	}
	if err := emit(e); err != nil {
		fmt.Fprintf(stderr, "calport hook: %v\n", err)
	}
}

// Install handles `integrations install TOOL...` for the binary at bin.
func Install(args []string, bin string, out io.Writer) error {
	if len(args) < 2 || args[0] != "install" {
		return errors.New("usage: integrations install claude|cursor|codex|all")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	tools := args[1:]
	if len(tools) == 1 && tools[0] == "all" {
		tools = []string{"claude", "cursor", "codex"}
	}
	for _, tool := range tools {
		switch tool {
		case "claude":
			skill, err := InstallSkill(filepath.Join(home, ".claude", "skills"))
			if err != nil {
				return err
			}
			settings := filepath.Join(home, ".claude", "settings.json")
			changed, err := InstallClaudeHooks(settings, bin)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Claude Code: skill at %s; hooks %s in %s\n", skill, verb(changed), settings)
		case "cursor":
			hooks := filepath.Join(home, ".cursor", "hooks.json")
			changed, err := InstallCursorHooks(hooks, bin)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Cursor: stop hook %s in %s (existing hooks kept)\n", verb(changed), hooks)
		case "codex":
			skill, err := InstallSkill(filepath.Join(home, ".codex", "skills"))
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Codex: skill at %s\n", skill)
			// config.toml has a single notify setting the user may already
			// use, so calport suggests it rather than overwriting it.
			fmt.Fprintf(out, "  To announce finished turns, add to ~/.codex/config.toml:\n    notify = [%q, \"hook\", \"codex\", \"notify\"]\n", bin)
		default:
			return fmt.Errorf("unknown tool %q; use claude, cursor, codex, or all", tool)
		}
	}
	return nil
}

func verb(changed bool) string {
	if changed {
		return "added"
	}
	return "already present"
}
