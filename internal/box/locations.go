// Package box is what calportd offers paired laptops beyond raw port streams:
// locations and worktrees, listening ports, agent sessions, and shares.
package box

import (
	"github.com/sean-brydon/calport/internal/kit"

	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/statefile"
	"github.com/sean-brydon/calport/internal/trust"
)

// Location is a named place on a box where work happens: a repository or any
// directory. Agents and worktrees are created relative to a location.
type Location struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Repo is true when Path is the root of a git repository.
	Repo bool `json:"repo"`
	// Cal is true for a Cal.com checkout, which the Cal.com kit can serve.
	Cal       bool       `json:"cal,omitempty"`
	Worktrees []Worktree `json:"worktrees,omitempty"`
	// Scripts run when calport creates or removes worktrees here.
	Scripts Scripts `json:"scripts"`
}

type Worktree struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	// Main marks the repository's own checkout.
	Main bool `json:"main,omitempty"`
	// SettingUp is true when the tool that made it is still running its
	// setup; a worktree.setup event follows.
	SettingUp bool `json:"setting_up,omitempty"`
}

var (
	ErrUnknownLocation = errors.New("no location with that name")
	ErrUnknownWorktree = errors.New("no worktree with that name in the location")
)

type Locations struct{ path string }

func NewLocations(path string) *Locations { return &Locations{path: path} }

type savedLocation struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Setup   string `json:"setup,omitempty"`
	Archive string `json:"archive,omitempty"`
}

func (l *Locations) Add(ctx context.Context, name, path string) (Location, error) {
	if !trust.ValidName(name) {
		return Location{}, fmt.Errorf("invalid location name %q", name)
	}
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return Location{}, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return Location{}, fmt.Errorf("%s is not a directory on this box", abs)
	}
	err = l.update(func(all []savedLocation) ([]savedLocation, error) {
		out := all[:0]
		for _, s := range all {
			if s.Name == name {
				continue
			}
			out = append(out, s)
		}
		return append(out, savedLocation{Name: name, Path: abs}), nil
	})
	if err != nil {
		return Location{}, err
	}
	return l.Get(ctx, name)
}

// SetScripts sets or, with empty values, clears a location's own lifecycle
// scripts; cleared, it falls back to Orca's for the repository.
func (l *Locations) SetScripts(name, setup, archive string) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name == name {
				all[i].Setup, all[i].Archive = setup, archive
				return all, nil
			}
		}
		return nil, ErrUnknownLocation
	})
}

func (l *Locations) Remove(name string) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i, s := range all {
			if s.Name == name {
				return append(all[:i], all[i+1:]...), nil
			}
		}
		return nil, ErrUnknownLocation
	})
}

func (l *Locations) List(ctx context.Context) ([]Location, error) {
	saved, err := l.read()
	if err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(saved))
	for _, s := range saved {
		out = append(out, describe(ctx, s))
	}
	return out, nil
}

func (l *Locations) Get(ctx context.Context, name string) (Location, error) {
	saved, err := l.read()
	if err != nil {
		return Location{}, err
	}
	for _, s := range saved {
		if s.Name == name {
			return describe(ctx, s), nil
		}
	}
	return Location{}, ErrUnknownLocation
}

// Dir resolves "location" or "location/worktree" to a directory.
func (l *Locations) Dir(ctx context.Context, ref string) (string, error) {
	name, wt, _ := strings.Cut(ref, "/")
	loc, err := l.Get(ctx, name)
	if err != nil {
		return "", err
	}
	if wt == "" {
		return loc.Path, nil
	}
	for _, w := range loc.Worktrees {
		if w.Name == wt {
			return w.Path, nil
		}
	}
	return "", ErrUnknownWorktree
}

// CreateWorktree adds a git worktree next to the repository, following the
// <parent>/<repo>-<name> layout, on a new branch from base.
func (l *Locations) CreateWorktree(ctx context.Context, location, name, branch, base string) (Worktree, error) {
	if !trust.ValidName(name) {
		return Worktree{}, fmt.Errorf("invalid worktree name %q", name)
	}
	loc, err := l.Get(ctx, location)
	if err != nil {
		return Worktree{}, err
	}
	if !loc.Repo {
		return Worktree{}, fmt.Errorf("location %s is not a git repository", location)
	}
	if branch == "" {
		branch = name
	}
	path := filepath.Join(filepath.Dir(loc.Path), filepath.Base(loc.Path)+"-"+name)
	args := []string{"-C", loc.Path, "worktree", "add", "-b", branch, path}
	if base != "" {
		args = append(args, base)
	}
	if out, err := git(ctx, args...); err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	for _, w := range describe(ctx, savedLocation{Name: loc.Name, Path: loc.Path}).Worktrees {
		if w.Path == path {
			return w, nil
		}
	}
	return Worktree{Name: name, Path: path, Branch: branch}, nil
}

// RemoveWorktree removes a worktree. Git refuses when it has uncommitted
// changes unless force is set, and that refusal is passed on unchanged.
func (l *Locations) RemoveWorktree(ctx context.Context, location, name string, force bool) error {
	loc, err := l.Get(ctx, location)
	if err != nil {
		return err
	}
	for _, w := range loc.Worktrees {
		if w.Name != name {
			continue
		}
		if w.Main {
			return errors.New("refusing to remove the repository's main checkout")
		}
		// Orca keeps its own records of the worktrees it made; removing one
		// behind its back would leave them stale.
		if orcaManaged(w.Path) {
			// --run-hooks runs Orca's archive script, as removing it in the
			// Orca app does; without it the worktree's services are left behind.
			args := []string{"worktree", "rm", "--worktree", "path:" + w.Path, "--run-hooks", "--json"}
			if force {
				args = append(args, "--force")
			}
			_, err := runTool(ctx, nil, "orca", args...)
			return err
		}
		args := []string{"-C", loc.Path, "worktree", "remove", w.Path}
		if force {
			args = append(args, "--force")
		}
		if out, err := git(ctx, args...); err != nil {
			return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	return ErrUnknownWorktree
}

func describe(ctx context.Context, s savedLocation) Location {
	loc := Location{Name: s.Name, Path: s.Path, Scripts: scriptsFor(s)}
	out, err := git(ctx, "-C", s.Path, "worktree", "list", "--porcelain")
	if err != nil {
		return loc
	}
	loc.Repo = true
	loc.Cal = kit.LooksLikeCal(s.Path)
	loc.Worktrees = parseWorktrees(out, s.Path)
	return loc
}

// parseWorktrees reads `git worktree list --porcelain`. Each worktree is named
// by its directory, with the repository's own "<repo>-" prefix removed, so
// ~/work/cal-billing is "billing" in location "cal".
func parseWorktrees(out []byte, repo string) []Worktree {
	var all []Worktree
	var cur *Worktree
	prefix := filepath.Base(repo) + "-"
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			all = append(all, Worktree{Path: value})
			cur = &all[len(all)-1]
			cur.Main = len(all) == 1
			if cur.Main {
				cur.Name = filepath.Base(value)
			} else {
				cur.Name = strings.TrimPrefix(filepath.Base(value), prefix)
			}
		case "HEAD":
			if cur != nil && len(value) >= 10 {
				cur.Head = value[:10]
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		}
	}
	// Worktrees git has lost track of (prunable, e.g. under a cleared /tmp)
	// are not places anyone can work.
	live := all[:0]
	for _, w := range all {
		if _, err := os.Stat(w.Path); err == nil {
			live = append(live, w)
		}
	}
	return live
}

func git(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.CombinedOutput()
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func (l *Locations) read() ([]savedLocation, error) {
	b, err := os.ReadFile(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []savedLocation
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%s is unreadable (a backup may be at %s.bak): %w", l.path, l.path, err)
	}
	return all, nil
}

func (l *Locations) update(change func([]savedLocation) ([]savedLocation, error)) error {
	unlock, err := statefile.Lock(l.path)
	if err != nil {
		return err
	}
	defer unlock()
	all, err := l.read()
	if err != nil {
		return err
	}
	all, err = change(all)
	if err != nil {
		return err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return statefile.WriteWithBackup(l.path, append(b, '\n'))
}

func orcaManaged(path string) bool {
	home, err := os.UserHomeDir()
	return err == nil && strings.HasPrefix(path, filepath.Join(home, "orca", "workspaces")+string(filepath.Separator))
}

// OrcaManaged reports whether Orca made the worktree at path and so runs its
// lifecycle scripts itself.
func OrcaManaged(path string) bool { return orcaManaged(path) }
