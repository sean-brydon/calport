// Package doctor describes what is set up, what is missing, and how to fix
// it, for a laptop or a box. Checks never change anything.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	// Info marks an optional tool that is simply not in use.
	Info Status = "info"
)

type Check struct {
	Area   string `json:"area"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Fix is a command, or one plain sentence, that resolves the problem.
	Fix string `json:"fix,omitempty"`
}

// extraToolDirs are searched after PATH. macOS GUI apps inherit a minimal
// PATH, so a Homebrew install is invisible to a check that trusts PATH alone.
// A variable so tests can redirect it.
var extraToolDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// Tool finds an executable on PATH, in ~/.local/bin, in a Homebrew prefix, or
// where a Node version manager keeps its global installs: that is where Orca,
// Herdr, Paseo, cloudflared, and the agent CLIs put themselves.
func Tool(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	dirs := extraToolDirs
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, ".local", "bin")}, dirs...)
		dirs = append(dirs, nodeToolDirs(home, dirs)...)
	}
	return findExecutable(name, dirs)
}

// findExecutable returns the first dir holding name as something runnable.
func findExecutable(name string, dirs []string) (string, bool) {
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// nodeToolDirs are where Node keeps globally installed CLIs. An npm global
// lands in a directory belonging to the selected Node version, which only a
// shell that ran the version manager's init has on PATH - so calportd under
// systemd, and a macOS app, both see nothing without looking here. known is
// where Tool has already decided to look.
//
// Each manager is asked for the version it considers current, rather than
// having its versions globbed: picking an arbitrary installed version would
// run a CLI the user has not selected.
func nodeToolDirs(home string, known []string) []string {
	fnm := os.Getenv("FNM_DIR")
	if fnm == "" {
		fnm = filepath.Join(home, ".local", "share", "fnm")
	}
	dirs := []string{
		filepath.Join(fnm, "aliases", "default", "bin"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
	}
	// nvm names its default in a file, which may hold a full version
	// ("v20.19.0") or a prefix it resolves at use time ("20", "lts/iron").
	nvm := os.Getenv("NVM_DIR")
	if nvm == "" {
		nvm = filepath.Join(home, ".nvm")
	}
	if alias, err := os.ReadFile(filepath.Join(nvm, "alias", "default")); err == nil {
		want := strings.TrimSpace(string(alias))
		versions := filepath.Join(nvm, "versions", "node")
		if entries, err := os.ReadDir(versions); err == nil {
			for _, e := range entries {
				if e.Name() == want || strings.HasPrefix(e.Name(), "v"+want+".") {
					dirs = append(dirs, filepath.Join(versions, e.Name(), "bin"))
				}
			}
		}
	}
	// Wherever node itself is, npm's global bin is its sibling. This catches a
	// hand-rolled prefix that no manager-specific path would, as long as
	// something put node somewhere we can see - commonly a symlink in
	// ~/.local/bin, which is why the search covers known too.
	node, err := exec.LookPath("node")
	if err != nil {
		node, _ = findExecutable("node", append(append([]string{}, known...), dirs...))
	}
	if node != "" {
		if real, err := filepath.EvalSymlinks(node); err == nil {
			node = real
		}
		dirs = append(dirs, filepath.Dir(node))
	}
	return dirs
}

// ToolCheck reports a tool's presence. required marks tools calport cannot
// work without; the rest are optional features.
func ToolCheck(area, name, purpose, install string, required bool) Check {
	if path, ok := Tool(name); ok {
		return Check{Area: area, Name: name, Status: OK, Detail: path}
	}
	c := Check{Area: area, Name: name, Status: Info, Detail: "not installed; needed for " + purpose, Fix: install}
	if required {
		c.Status = Fail
	}
	return c
}

// OrcaChecks reports whether Orca is installed and running, and whether each
// repository calport knows about is registered with it.
func OrcaChecks(ctx context.Context, area string, repos []string) []Check {
	orca, ok := Tool("orca")
	if !ok {
		return []Check{{Area: area, Name: "orca", Status: Info, Detail: "not installed; calport works without it", Fix: "Install the Orca CLI to use --provider orca"}}
	}
	checks := []Check{{Area: area, Name: "orca", Status: OK, Detail: orca}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, orca, "status", "--json").Run(); err != nil {
		return append(checks, Check{Area: area, Name: "Orca runtime", Status: Warn, Detail: "not running", Fix: "orca serve  (or open the Orca app)"})
	}
	checks = append(checks, Check{Area: area, Name: "Orca runtime", Status: OK, Detail: "running"})
	if len(repos) == 0 {
		return checks
	}
	list, err := exec.CommandContext(ctx, orca, "repo", "list", "--json").Output()
	if err != nil {
		return checks
	}
	for _, repo := range repos {
		quoted, _ := json.Marshal(repo)
		if strings.Contains(string(list), `"path": `+string(quoted)) || strings.Contains(string(list), `"path":`+string(quoted)) {
			checks = append(checks, Check{Area: area, Name: "Orca knows " + filepath.Base(repo), Status: OK, Detail: repo})
			continue
		}
		checks = append(checks, Check{
			Area: area, Name: "Orca knows " + filepath.Base(repo), Status: Info,
			Detail: repo + " is not registered; calport registers it on first --provider orca",
			Fix:    "orca repo add --path " + repo,
		})
	}
	return checks
}

// Print writes checks for a person, grouped by area, with fixes indented.
func Print(w io.Writer, checks []Check) (problems int) {
	area := ""
	for _, c := range checks {
		if c.Area != area {
			if area != "" {
				fmt.Fprintln(w)
			}
			area = c.Area
			fmt.Fprintln(w, area)
		}
		mark := map[Status]string{OK: "✓", Warn: "!", Fail: "✗", Info: "·"}[c.Status]
		line := fmt.Sprintf("  %s %s", mark, c.Name)
		if c.Detail != "" {
			line += "  " + c.Detail
		}
		fmt.Fprintln(w, line)
		if c.Fix != "" && c.Status != OK {
			fmt.Fprintf(w, "      → %s\n", c.Fix)
		}
		if c.Status == Warn || c.Status == Fail {
			problems++
		}
	}
	return problems
}
