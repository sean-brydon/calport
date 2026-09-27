// Package kit installs Cal.com's worktree kit on a box: Orca-compatible setup
// and archive hooks that give every worktree its own port, database, env and
// URL, plus the router that serves those URLs. The scripts are embedded in
// calportd, so a box needs nothing from anywhere else.
package kit

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/service"
)

//go:embed cal
var files embed.FS

// Hooks are what Orca (or calport) runs for a worktree of the repository.
const (
	SetupHook   = `"$HOME/.local/bin/cal-worktree" setup`
	ArchiveHook = `"$HOME/.local/bin/cal-archive"`
)

// Config is the kit's config.json; the scripts read it at every run.
type Config struct {
	// Host is the label in worktree URLs: NAME-abc123.HOST.cal.localhost.
	Host string `json:"host"`
	// Root is the main checkout that worktrees are made from.
	Root string `json:"root"`
	Node string `json:"node,omitempty"`
	Orca string `json:"orca,omitempty"`
	// PostgresContainer runs pg_dump and pg_restore when the box has none.
	PostgresContainer string `json:"postgres_container,omitempty"`
}

// Status is what a box reports about its kit.
type Status struct {
	Installed bool   `json:"installed"`
	Config    Config `json:"config"`
	Dir       string `json:"dir"`
	// Pattern is the laptop route that reaches this box's worktree URLs.
	Pattern string `json:"pattern,omitempty"`
	// Worktrees are the worktrees the kit has set up, with their URLs.
	Worktrees []Worktree `json:"worktrees,omitempty"`
}

// Worktree is one worktree the kit has set up.
type Worktree struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Port   int    `json:"port"`
	Active bool   `json:"active"`
}

// Result says what an install did and what is left for a person to do.
type Result struct {
	Status
	Notes []string `json:"notes,omitempty"`
}

// Installer installs the kit for one user. Its fields are replaced in tests.
type Installer struct {
	Home string
	// Calportd runs the router, as `calportd kit router`.
	Calportd string
	// Find locates a program the way the user's login shell would.
	Find func(name string) string
	// Units installs and (re)starts a systemd user unit.
	Units func(service.Spec) error
	// Command runs a program and returns its output.
	Command func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func NewInstaller(calportd string) (*Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	in := &Installer{Home: home, Calportd: calportd, Units: func(s service.Spec) error { _, err := service.Install(s); return err }}
	in.Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output()
	}
	in.Find = in.find
	return in, nil
}

// Dir is where the kit lives; the scripts expect exactly this path.
func (in *Installer) Dir() string { return filepath.Join(in.Home, ".local", "share", "cal-worktrees") }

func (in *Installer) bin() string { return filepath.Join(in.Home, ".local", "bin") }

// Status reads the installed kit, if any.
func (in *Installer) Status() Status {
	s := Status{Dir: in.Dir()}
	b, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if err != nil || json.Unmarshal(b, &s.Config) != nil {
		return s
	}
	_, err = os.Stat(filepath.Join(s.Dir, "cal-worktree"))
	s.Installed = err == nil && s.Config.Root != ""
	if !s.Installed {
		return s
	}
	s.Pattern = "*." + s.Config.Host + ".cal.localhost"
	records, _ := filepath.Glob(filepath.Join(s.Dir, "routes", "*.json"))
	for _, file := range records {
		var w Worktree
		if b, err := os.ReadFile(file); err == nil && json.Unmarshal(b, &w) == nil && w.Host != "" {
			s.Worktrees = append(s.Worktrees, w)
		}
	}
	return s
}

// LooksLikeCal reports whether dir is a Cal.com checkout.
func LooksLikeCal(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(b, &pkg) == nil && pkg.Name == "calcom-monorepo"
}

// Install puts the kit in place for the checkout at root. An existing kit
// keeps its URL label, so worktree URLs already in use stay valid.
func (in *Installer) Install(ctx context.Context, root, host string) (Result, error) {
	var res Result
	root, err := filepath.Abs(root)
	if err != nil {
		return res, err
	}
	if !LooksLikeCal(root) {
		return res, fmt.Errorf("%s is not a Cal.com checkout", root)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return res, fmt.Errorf("%s is not a git checkout", root)
	}
	prev := in.Status()
	if prev.Installed && prev.Config.Root != root {
		return res, fmt.Errorf("the kit on this box already serves %s; it supports one Cal.com checkout per box", prev.Config.Root)
	}
	cfg := Config{Host: strings.ToLower(host), Root: root}
	if prev.Installed && prev.Config.Host != "" {
		cfg.Host = prev.Config.Host
	}
	if !validLabel(cfg.Host) {
		return res, fmt.Errorf("%q cannot be part of a URL", cfg.Host)
	}
	for _, tool := range []string{"python3", "git", "systemctl"} {
		if in.Find(tool) == "" {
			return res, fmt.Errorf("the kit needs %s on the box", tool)
		}
	}
	if cfg.Node = in.Find("node"); cfg.Node == "" {
		return res, errors.New("the kit needs Node.js on the box (the version Cal.com uses)")
	}
	cfg.Orca = firstFound(in.Find, "orca-ide", "orca")
	var notes []string
	if in.Find("pg_dump") == "" {
		if cfg.PostgresContainer = in.postgresContainer(ctx, databasePort(root)); cfg.PostgresContainer == "" {
			notes = append(notes, "No pg_dump here and no Postgres container found, so new worktrees migrate and seed a fresh database instead of copying the main one.")
		}
	}
	yarn, err := in.yarnWrapper(cfg.Node)
	if err != nil {
		return res, err
	}

	res.Notes = notes
	dir := in.Dir()
	for _, d := range []string{dir, filepath.Join(dir, "routes"), filepath.Join(dir, "bin"), in.bin()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return res, err
		}
	}
	if err := fs.WalkDir(files, "cal", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if !strings.HasSuffix(path, ".cjs") {
			mode = 0o755
		}
		return writeAtomic(filepath.Join(dir, filepath.Base(path)), b, mode)
	}); err != nil {
		return res, err
	}
	if err := writeAtomic(filepath.Join(dir, "bin", "yarn"), []byte(yarn), 0o755); err != nil {
		return res, err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := writeAtomic(filepath.Join(dir, "config.json"), append(b, '\n'), 0o600); err != nil {
		return res, err
	}
	for _, name := range []string{"cal-worktree", "cal-archive", "cal-setup"} {
		note, err := link(filepath.Join(dir, name), filepath.Join(in.bin(), name))
		if err != nil {
			return res, err
		}
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
	}

	// The unit names match what earlier installs used, so this takes over
	// their router and reconciler instead of fighting them for the port.
	if err := in.Units(service.Spec{
		Name: "cal-worktree-proxy", Description: "Cal.com worktree URL router",
		Program: in.Calportd, Args: []string{"kit", "router", "--dir", dir},
	}); err != nil {
		return res, fmt.Errorf("starting the worktree router: %w", err)
	}
	if cfg.Orca != "" {
		if err := in.Units(service.Spec{
			Name: "cal-worktree-lifecycle", Description: "Stop Cal.com worktrees Orca archives, restart ones it restores",
			Program: in.Find("python3"), Args: []string{filepath.Join(dir, "reconcile.py")},
		}); err != nil {
			return res, fmt.Errorf("starting the lifecycle service: %w", err)
		}
	} else {
		res.Notes = append(res.Notes, "Orca is not installed here, so archived worktrees are not stopped automatically; the archive hook still stops them.")
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); err != nil {
		res.Notes = append(res.Notes, "The main checkout has no .env yet; worktree setup copies it, so create it first.")
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules")); err != nil {
		res.Notes = append(res.Notes, "Run yarn in the main checkout once; worktrees reuse its download cache.")
	}
	res.Status = in.Status()
	return res, nil
}

func validLabel(s string) bool {
	if s == "" || len(s) > 63 || strings.Trim(s, "-") != s {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// yarnWrapper runs the yarn that ships with node through corepack, falling
// back to a yarn on PATH.
func (in *Installer) yarnWrapper(node string) (string, error) {
	resolved, err := filepath.EvalSymlinks(node)
	if err == nil {
		corepack := filepath.Join(filepath.Dir(filepath.Dir(resolved)), "lib", "node_modules", "corepack", "dist", "yarn.js")
		if _, err := os.Stat(corepack); err == nil {
			return fmt.Sprintf("#!/bin/sh\nexec %q %q \"$@\"\n", resolved, corepack), nil
		}
	}
	if yarn := in.Find("yarn"); yarn != "" {
		return fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", yarn), nil
	}
	return "", errors.New("the kit needs yarn on the box (corepack enable)")
}

// databasePort is the local port of the database the checkout's .env uses.
func databasePort(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return ""
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && (k == "DATABASE_URL" || k == "DATABASE_DIRECT_URL") {
			values[k] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	raw := values["DATABASE_DIRECT_URL"]
	if raw == "" {
		raw = values["DATABASE_URL"]
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		return ""
	}
	if u.Port() == "" {
		return "5432"
	}
	return u.Port()
}

// postgresContainer finds the running container that publishes the
// database's port, for pg_dump and pg_restore on a box without them.
func (in *Installer) postgresContainer(ctx context.Context, port string) string {
	docker := in.Find("docker")
	if docker == "" || port == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := in.Command(ctx, docker, "ps", "--filter", "publish="+port, "--format", "{{.Names}}")
	if err != nil {
		return ""
	}
	names := strings.Fields(string(out))
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func firstFound(find func(string) string, names ...string) string {
	for _, n := range names {
		if p := find(n); p != "" {
			return p
		}
	}
	return ""
}

// find looks a program up through the user's login shell, where version
// managers like fnm and nvm put node, then in their usual directories. The
// daemon's own PATH, under systemd, has neither.
func (in *Installer) find(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, shell := range []string{os.Getenv("SHELL"), "/bin/bash"} {
		if shell == "" {
			continue
		}
		out, err := exec.CommandContext(ctx, shell, "-lic", "command -v "+name).Output()
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if p := strings.TrimSpace(lines[len(lines)-1]); lasting(p) {
			return p
		}
	}
	dirs := []string{
		filepath.Join(in.Home, ".local", "bin"),
		filepath.Join(in.Home, ".local", "share", "fnm", "aliases", "default", "bin"),
		filepath.Join(in.Home, ".volta", "bin"),
		"/usr/local/bin", "/usr/bin", "/bin",
	}
	if nvm, _ := filepath.Glob(filepath.Join(in.Home, ".nvm", "versions", "node", "*", "bin")); len(nvm) > 0 {
		sort.Strings(nvm)
		dirs = append(dirs, nvm[len(nvm)-1])
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// lasting reports whether a found path outlives the shell that found it.
// fnm, for one, puts node on PATH through a per-shell link under /run that
// is gone once that shell exits.
func lasting(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for _, dir := range []string{"/run/", "/tmp/", "/var/run/", os.TempDir() + "/"} {
		if strings.HasPrefix(path, dir) {
			return false
		}
	}
	return !strings.Contains(path, "fnm_multishells")
}

// link points path at target. A file of the same name that is not already
// the kit's is kept as NAME.calport-backup.
func link(target, path string) (string, error) {
	if cur, err := os.Readlink(path); err == nil {
		if cur == target {
			return "", nil
		}
		os.Remove(path)
	} else if _, err := os.Lstat(path); err == nil {
		backup := path + ".calport-backup"
		if err := os.Rename(path, backup); err != nil {
			return "", err
		}
		return fmt.Sprintf("Kept your previous %s as %s.", filepath.Base(path), backup), os.Symlink(target, path)
	}
	return "", os.Symlink(target, path)
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
