package box

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorktreeRequest asks for a new worktree at a location. Provider chooses who
// creates it: "git" (default), or "orca" / "herdr" so the new worktree also
// appears in that tool, with Orca optionally starting an agent in it.
type WorktreeRequest struct {
	Name     string `json:"name"`
	Branch   string `json:"branch,omitempty"`
	Base     string `json:"base,omitempty"`
	Provider string `json:"provider,omitempty"`
	// Agent and Prompt are passed to Orca, which starts the agent in the
	// worktree's first terminal.
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	// HerdrSession selects the Herdr session to open the workspace in.
	HerdrSession string `json:"herdr_session,omitempty"`
}

// toolPath finds a tool on PATH or in ~/.local/bin, where Orca, Herdr, and
// agent CLIs install themselves but which a systemd unit's PATH omits.
func toolPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not installed on this box", name)
}

func runTool(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	bin, err := toolPath(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%s: %s", name, strings.TrimSpace(string(ee.Stderr)))
		}
		if len(out) > 0 {
			return out, fmt.Errorf("%s: %s", name, strings.TrimSpace(string(out)))
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// Create makes a worktree with the requested provider and returns it as git
// reports it, so every provider's result looks the same to callers.
func (l *Locations) Create(ctx context.Context, location string, req WorktreeRequest) (Worktree, error) {
	loc, err := l.Get(ctx, location)
	if err != nil {
		return Worktree{}, err
	}
	var path string
	switch req.Provider {
	case "", "git":
		return l.CreateWorktree(ctx, location, req.Name, req.Branch, req.Base)
	case "orca":
		path, err = orcaCreate(ctx, loc, req)
	case "herdr":
		path, err = herdrCreate(ctx, loc, req)
	default:
		return Worktree{}, fmt.Errorf("unknown worktree provider %q; use git, orca, or herdr", req.Provider)
	}
	if err != nil {
		return Worktree{}, err
	}
	for _, w := range describe(ctx, savedLocation{Name: loc.Name, Path: loc.Path}).Worktrees {
		if w.Path == path {
			return w, nil
		}
	}
	return Worktree{Name: req.Name, Path: path, Branch: req.Branch}, nil
}

// orcaEnsureRepo registers the location's repository with Orca unless Orca
// already knows it, so --provider orca needs no setup in the Orca app first.
func orcaEnsureRepo(ctx context.Context, path string) error {
	out, err := runTool(ctx, nil, "orca", "repo", "list", "--json")
	if err != nil {
		return err
	}
	if orcaKnowsRepo(out, path) {
		return nil
	}
	_, err = runTool(ctx, nil, "orca", "repo", "add", "--path", path, "--json")
	return err
}

func orcaKnowsRepo(repoList []byte, path string) bool {
	quoted, _ := json.Marshal(path)
	return bytes.Contains(repoList, append([]byte(`"path":`), quoted...)) ||
		bytes.Contains(repoList, append([]byte(`"path": `), quoted...))
}

func orcaCreate(ctx context.Context, loc Location, req WorktreeRequest) (string, error) {
	if err := orcaEnsureRepo(ctx, loc.Path); err != nil {
		return "", err
	}
	args := []string{"worktree", "create", "--repo", "path:" + loc.Path, "--name", req.Name, "--no-parent", "--json"}
	if req.Base != "" {
		args = append(args, "--base-branch", req.Base)
	}
	if req.Agent != "" {
		args = append(args, "--agent", req.Agent)
	}
	if req.Prompt != "" {
		args = append(args, "--prompt", req.Prompt)
	}
	out, err := runTool(ctx, nil, "orca", args...)
	if err != nil {
		return "", err
	}
	return parseOrcaCreate(out)
}

func parseOrcaCreate(out []byte) (string, error) {
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Worktree struct {
				Path string `json:"path"`
			} `json:"worktree"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("orca returned unexpected output: %.200s", out)
	}
	if !resp.OK || resp.Result.Worktree.Path == "" {
		return "", fmt.Errorf("orca did not create the worktree: %v", resp.Error)
	}
	return resp.Result.Worktree.Path, nil
}

func herdrCreate(ctx context.Context, loc Location, req WorktreeRequest) (string, error) {
	branch := req.Branch
	if branch == "" {
		branch = req.Name
	}
	path := filepath.Join(filepath.Dir(loc.Path), filepath.Base(loc.Path)+"-"+req.Name)
	args := []string{"worktree", "create", "--cwd", loc.Path, "--branch", branch, "--path", path, "--label", req.Name}
	if req.Base != "" {
		args = append(args, "--base", req.Base)
	}
	var env []string
	if req.HerdrSession != "" {
		env = append(env, "HERDR_SESSION="+req.HerdrSession)
	}
	out, err := runTool(ctx, env, "herdr", args...)
	if err != nil {
		return "", err
	}
	return parseHerdrCreate(out)
}

func parseHerdrCreate(out []byte) (string, error) {
	var resp struct {
		Result struct {
			RootPane struct {
				Cwd string `json:"cwd"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.Result.RootPane.Cwd == "" {
		return "", fmt.Errorf("herdr returned unexpected output: %.200s", out)
	}
	return resp.Result.RootPane.Cwd, nil
}

var validAgent = map[string]bool{"": true, "claude": true, "codex": true}

// OpenIn opens dir in Orca or Herdr and, when agent is set, starts it there.
func OpenIn(ctx context.Context, req OpenRequest, repo, dir, name string) error {
	if !validAgent[req.Agent] {
		return fmt.Errorf("unknown agent %q; use claude or codex", req.Agent)
	}
	switch req.Tool {
	case "orca":
		if err := orcaEnsureRepo(ctx, repo); err != nil {
			return err
		}
		args := []string{"terminal", "create", "--worktree", "path:" + dir, "--focus", "--json"}
		if req.Agent != "" {
			args = append(args, "--command", req.Agent)
		}
		_, err := runTool(ctx, nil, "orca", args...)
		return err
	case "herdr":
		var env []string
		if req.HerdrSession != "" {
			env = append(env, "HERDR_SESSION="+req.HerdrSession)
		}
		out, err := runTool(ctx, env, "herdr", "worktree", "open", "--cwd", repo, "--path", dir)
		if err != nil || req.Agent == "" {
			return err
		}
		var resp struct {
			Result struct {
				RootPane struct {
					PaneID string `json:"pane_id"`
				} `json:"root_pane"`
			} `json:"result"`
		}
		if json.Unmarshal(out, &resp) != nil || resp.Result.RootPane.PaneID == "" {
			return fmt.Errorf("herdr opened the worktree but did not report a pane to start %s in", req.Agent)
		}
		_, err = runTool(ctx, env, "herdr", "agent", "start", name, "--kind", req.Agent, "--pane", resp.Result.RootPane.PaneID)
		return err
	}
	return fmt.Errorf("unknown tool %q; use orca or herdr", req.Tool)
}

// ImportOrcaRepos registers every repository Orca knows as a location, named
// after its folder. Locations that already exist are left as they are.
func (l *Locations) ImportOrcaRepos(ctx context.Context) ([]Location, error) {
	out, err := runTool(ctx, nil, "orca", "repo", "list", "--json")
	if err != nil {
		return nil, err
	}
	paths, err := parseOrcaRepoPaths(out)
	if err != nil {
		return nil, err
	}
	existing, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, loc := range existing {
		known[loc.Path] = true
		known["name:"+loc.Name] = true
	}
	var added []Location
	for _, path := range paths {
		if known[path] {
			continue
		}
		name := locationName(filepath.Base(path))
		for i := 2; known["name:"+name]; i++ {
			name = fmt.Sprintf("%s-%d", locationName(filepath.Base(path)), i)
		}
		loc, err := l.Add(ctx, name, path)
		if err != nil {
			continue // a repo Orca lists but that is gone from disk
		}
		known["name:"+name] = true
		added = append(added, loc)
	}
	return added, nil
}

func parseOrcaRepoPaths(out []byte) ([]string, error) {
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Repos []struct {
				Path string `json:"path"`
			} `json:"repos"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("orca returned unexpected output: %.200s", out)
	}
	var paths []string
	for _, r := range resp.Result.Repos {
		if r.Path != "" {
			paths = append(paths, r.Path)
		}
	}
	return paths, nil
}

func locationName(base string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" {
		return "repo"
	}
	return name
}
