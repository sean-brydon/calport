package integrations

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sean-brydon/calport/internal/statefile"
)

//go:embed SKILL.md
var Skill []byte

// InstallSkill writes the calport skill where an agent tool discovers skills,
// e.g. ~/.claude/skills/calport/SKILL.md.
func InstallSkill(skillsDir string) (string, error) {
	path := filepath.Join(skillsDir, "calport", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, Skill, 0o644)
}

// InstallClaudeHooks adds calport's hooks to a Claude Code settings file,
// keeping every existing setting and hook. It reports whether it changed
// anything; running it again is a no-op.
func InstallClaudeHooks(settingsPath, bin string) (bool, error) {
	return editJSON(settingsPath, func(root map[string]any) bool {
		hooks := object(root, "hooks")
		changed := false
		// SessionStart and UserPromptSubmit mark the agent busy again, so a
		// "needs you" state clears once someone answers it.
		for _, event := range []string{"Stop", "Notification", "SessionStart", "UserPromptSubmit"} {
			command := hookCommand(bin, "claude", event)
			list, _ := hooks[event].([]any)
			if containsCommand(list, command) {
				continue
			}
			hooks[event] = append(list, map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": command}},
			})
			changed = true
		}
		return changed
	})
}

// InstallCursorHooks appends calport's hook to Cursor's hooks file, after any
// hooks other tools (such as Orca) already registered there.
func InstallCursorHooks(hooksPath, bin string) (bool, error) {
	return editJSON(hooksPath, func(root map[string]any) bool {
		if _, ok := root["version"]; !ok {
			root["version"] = 1
		}
		hooks := object(root, "hooks")
		command := hookCommand(bin, "cursor", "stop")
		list, _ := hooks["stop"].([]any)
		if containsCommand(list, command) {
			return false
		}
		hooks["stop"] = append(list, map[string]any{"command": command, "timeout": 10})
		return true
	})
}

func hookCommand(bin, tool, event string) string {
	return fmt.Sprintf("%s hook %s %s", shellQuote(bin), tool, event)
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func object(root map[string]any, key string) map[string]any {
	if m, ok := root[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	root[key] = m
	return m
}

// containsCommand looks through both hook shapes, Cursor's flat
// {"command": …} and Claude's nested {"hooks": [{"command": …}]}.
func containsCommand(list []any, command string) bool {
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m["command"] == command {
			return true
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			if hm, _ := h.(map[string]any); hm["command"] == command {
				return true
			}
		}
	}
	return false
}

// editJSON applies change to a JSON object file and writes it back only when
// something changed, keeping the previous contents at path+".calport-backup".
// A file that is not a JSON object is left untouched.
func editJSON(path string, change func(map[string]any) bool) (bool, error) {
	root := map[string]any{}
	before, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(before, &root); err != nil {
			return false, fmt.Errorf("%s is not valid JSON, so calport left it alone: %w", path, err)
		}
	case !os.IsNotExist(err):
		return false, err
	}
	if !change(root) {
		return false, nil
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if len(before) > 0 {
		if err := os.WriteFile(path+".calport-backup", before, 0o600); err != nil {
			return false, err
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := statefile.Write(path, append(out, '\n')); err != nil {
		return false, err
	}
	return true, os.Chmod(path, mode)
}
