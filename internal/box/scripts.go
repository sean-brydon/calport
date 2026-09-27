package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Scripts are a location's worktree lifecycle commands, compatible with
// Orca's: they get ORCA_ROOT_PATH, ORCA_WORKTREE_PATH and ORCA_WORKSPACE_NAME,
// so a script written for Orca runs unchanged when calport creates the
// worktree with git or Herdr instead.
type Scripts struct {
	Setup   string `json:"setup,omitempty"`
	Archive string `json:"archive,omitempty"`
	// From says where the scripts came from: "calport" when set on the
	// location, "orca" when read from Orca's settings for the repository.
	From string `json:"from,omitempty"`
}

// orcaDataFiles are Orca's per-profile settings; tests point it elsewhere.
var orcaDataFiles = func() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".config", "orca", "profiles", "*", "orca-data.json"))
	return matches
}

// OrcaScripts reads the worktree hooks Orca has for the repository at repo.
// It is read at use rather than copied, so it always matches what Orca shows.
func OrcaScripts(repo string) (Scripts, bool) {
	for _, file := range orcaDataFiles() {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var data struct {
			Repos []struct {
				Path         string `json:"path"`
				HookSettings struct {
					Scripts struct {
						Setup   string `json:"setup"`
						Archive string `json:"archive"`
					} `json:"scripts"`
				} `json:"hookSettings"`
			} `json:"repos"`
		}
		if json.Unmarshal(b, &data) != nil {
			continue
		}
		for _, r := range data.Repos {
			s := r.HookSettings.Scripts
			if r.Path == repo && (s.Setup != "" || s.Archive != "") {
				return Scripts{Setup: s.Setup, Archive: s.Archive, From: "orca"}, true
			}
		}
	}
	return Scripts{}, false
}

// scriptsFor is the location's own scripts if set, otherwise Orca's.
func scriptsFor(saved savedLocation) Scripts {
	if saved.Setup != "" || saved.Archive != "" {
		return Scripts{Setup: saved.Setup, Archive: saved.Archive, From: "calport"}
	}
	s, _ := OrcaScripts(saved.Path)
	return s
}

// runScript runs a lifecycle script in the worktree through a login shell, so
// tools the user installed are on PATH, logging to logPath.
func runScript(ctx context.Context, script, repo, dir, name, logPath string, timeout time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-lc", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"ORCA_ROOT_PATH="+repo, "ORCA_WORKTREE_PATH="+dir, "ORCA_WORKSPACE_NAME="+name,
		"CALPORT_WORKTREE_PATH="+dir, "CALPORT_WORKTREE_NAME="+name)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v (log: %s)", err, logPath)
	}
	return nil
}
