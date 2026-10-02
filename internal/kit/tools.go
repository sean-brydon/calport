package kit

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/doctor"
)

// Worktree tools read these files from a checkout to run hooks for the
// worktrees they make. Calport writes them into the kit's checkout and lists
// them in that clone's .git/info/exclude, so they are never committed.

// ToolState says whether a tool runs the kit's hooks for a checkout.
type ToolState string

const (
	ToolConfigured ToolState = "configured"
	ToolMissing    ToolState = "missing"
	// ToolTracked means the repository commits the tool's file; calport
	// never overwrites it.
	ToolTracked ToolState = "tracked"
	ToolSkipped ToolState = "skipped"
)

// ToolStatus is one worktree tool's state for a checkout.
type ToolStatus struct {
	Tool  string    `json:"tool"`
	Name  string    `json:"name"`
	State ToolState `json:"state"`
	// File is where the tool's config goes: relative to the checkout, or
	// absolute for a per-user one (Herdr's plugin).
	File string `json:"file,omitempty"`
	// Installed means the tool is on this box.
	Installed bool `json:"installed"`
	// OptIn tools are set up only when asked for by name.
	OptIn  bool   `json:"opt_in,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// OrcaHooks are the scripts Orca's own settings run for the repository.
// They take precedence over orca.yaml.
type OrcaHooks struct{ Setup, Archive string }

// marker starts every file calport writes where the format has comments.
const marker = "# Written by calport for the Cal.com worktree kit."

const herdrPluginID = "calport.cal-worktrees"

// Paseo rejects an id with a dot: ids are ^[a-z][a-z0-9-]*$, so this cannot be
// Herdr's id even though it is the same plugin in spirit.
const paseoPluginID = "calport-cal-worktrees"

//go:embed herdr-plugin/hook
var herdrHook []byte

// Every path is listed, rather than embedding the directory, because an
// embedded directory would swallow node_modules: the plugin keeps dev
// dependencies so it can be typechecked, and Paseo supplies the runtime
// modules itself.
//
//go:embed paseo-plugin/paseo-plugin.json paseo-plugin/package.json paseo-plugin/tsconfig.json
//go:embed paseo-plugin/index.server.ts paseo-plugin/index.client.tsx
//go:embed paseo-plugin/client paseo-plugin/server paseo-plugin/shared
var paseoPluginFS embed.FS

// paseoPluginFiles is the plugin as calport ships it, keyed by path relative
// to the plugin directory.
func paseoPluginFiles() map[string][]byte {
	files := map[string][]byte{}
	root := "paseo-plugin"
	err := fs.WalkDir(paseoPluginFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := paseoPluginFS.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		// The files are embedded at build time, so a walk over them cannot
		// fail in a built binary.
		panic("kit: reading the embedded Paseo plugin: " + err.Error())
	}
	return files
}

// paseoPluginWritten reports whether dir holds exactly the plugin this calport
// ships. A plugin is a directory of files that have to agree with each other,
// so one stale file is as much a mismatch as a missing one.
func paseoPluginWritten(dir string) bool {
	for rel, want := range paseoPluginFiles() {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil || !bytes.Equal(got, want) {
			return false
		}
	}
	return true
}

// repoTool is a tool configured by a file in the checkout.
type repoTool struct {
	id, name, file, content string
	// programs and dirs (under home) mean the tool is on this box.
	programs, dirs []string
	// note is guidance that holds whether or not the file is written.
	note string
}

var repoTools = []repoTool{
	{
		id: "orca", name: "Orca", file: "orca.yaml", programs: []string{"orca", "orca-ide"},
		content: marker + " Excluded locally, never committed.\n" +
			"scripts:\n  setup: '" + SetupHook + "'\n  archive: '" + ArchiveHook + "'\n" +
			"setupAgentStartupPolicy: wait-for-setup\n",
		note: "Orca asks you to trust the scripts once.",
	},
	{
		id: "cursor", name: "Cursor", file: ".cursor/worktrees.json", programs: []string{"cursor", "cursor-agent"}, dirs: []string{".cursor-server"},
		content: "{\n  \"setup-worktree\": [" + quoteJSON(SetupHook) + "]\n}\n",
		note:    "Cursor has no teardown hook; archive its worktrees with cal-archive.",
	},
	{
		id: "codex", name: "Codex", file: ".codex/environments/environment.toml", programs: []string{"codex"}, dirs: []string{".codex"},
		content: marker + " Excluded locally, never committed.\n" +
			"version = 1\nname = \"Cal.com worktree kit\"\n\n" +
			"[setup]\nscript = '" + SetupHook + "'\n\n[cleanup]\nscript = '" + ArchiveHook + "'\n",
		note: "Select the \"Cal.com worktree kit\" environment once in the Codex app. Codex does not run setup over Remote SSH yet (openai/codex#23648).",
	},
	{
		id: "superset", name: "Superset", file: ".superset/config.json", programs: []string{"superset"}, dirs: []string{".superset"},
		content: "{\n  \"setup\": [" + quoteJSON(SetupHook) + "],\n  \"teardown\": [" + quoteJSON(ArchiveHook) + "]\n}\n",
	},
}

// skippedTools are tools calport does not configure, and why.
var skippedTools = []ToolStatus{
	{Tool: "conductor", Name: "Conductor", State: ToolSkipped, Detail: "Conductor makes worktrees on your Mac only, never on a box."},
	{Tool: "claude", Name: "Claude Code", State: ToolSkipped, Detail: "Claude Code's WorktreeCreate hook replaces how it makes worktrees; run cal-setup in one instead."},
}

func quoteJSON(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// Tools reports each worktree tool's state for the checkout at root. orca is
// what Orca's own settings run for it.
func (in *Installer) Tools(ctx context.Context, root string, orca OrcaHooks) []ToolStatus {
	var out []ToolStatus
	for _, t := range repoTools {
		out = append(out, in.repoToolStatus(ctx, root, t, orca))
	}
	out = append(out, in.herdrStatus(ctx), in.paseoStatus(ctx))
	return append(out, skippedTools...)
}

// RunsHooks reports whether tool runs the kit's hooks itself for worktrees
// of root, from its file or plugin, so calport must not run them again.
func (in *Installer) RunsHooks(ctx context.Context, tool, root string) bool {
	if tool == "herdr" {
		return in.herdrStatus(ctx).State == ToolConfigured
	}
	if tool == "paseo" {
		return in.paseoStatus(ctx).State == ToolConfigured
	}
	for _, t := range repoTools {
		if t.id == tool {
			b, err := os.ReadFile(filepath.Join(root, t.file))
			return err == nil && bytes.Contains(b, []byte("cal-worktree"))
		}
	}
	return false
}

func (in *Installer) repoToolStatus(ctx context.Context, root string, t repoTool, orca OrcaHooks) ToolStatus {
	s := ToolStatus{Tool: t.id, Name: t.name, File: t.file, Installed: in.installed(t.programs, t.dirs)}
	if t.id == "orca" && (orca.Setup != "" || orca.Archive != "") {
		s.State, s.Detail = ToolConfigured, "Orca runs these hooks from its settings."
		if orca.Setup != SetupHook || orca.Archive != ArchiveHook {
			s.State, s.Detail = ToolSkipped, "Orca runs different scripts from its settings for this repository, and those take precedence over orca.yaml."
		}
		return s
	}
	if tracked(ctx, root, t.file) {
		s.State, s.Detail = ToolTracked, t.file+" is committed in this repository, so calport leaves it alone; add the kit's hooks to it by hand."
		return s
	}
	current, err := os.ReadFile(filepath.Join(root, t.file))
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.State, s.Detail = ToolMissing, t.note
	case err != nil:
		s.State, s.Detail = ToolSkipped, err.Error()
	case string(current) == t.content && excluded(ctx, root, t.file):
		s.State, s.Detail = ToolConfigured, t.note
	case string(current) == t.content:
		s.State, s.Detail = ToolMissing, t.file+" is not excluded from git yet."
	case bytes.HasPrefix(current, []byte(marker)):
		s.State, s.Detail = ToolMissing, "An earlier calport wrote "+t.file+"; setting up rewrites it."
	case bytes.Contains(current, []byte("cal-worktree")):
		s.State, s.Detail = ToolConfigured, "Your own "+t.file+" runs the kit's hooks."
	default:
		s.State, s.Detail = ToolSkipped, t.file+" is yours and runs other scripts; add the kit's hooks to it by hand."
	}
	return s
}

// SetUpTools writes the named tools' config for the checkout at root, or
// every missing one that is not opt-in when names is empty. It returns the
// files it wrote.
func (in *Installer) SetUpTools(ctx context.Context, root string, names []string, orca OrcaHooks) ([]string, error) {
	statuses := in.Tools(ctx, root, orca)
	for _, n := range names {
		if !slices.ContainsFunc(statuses, func(s ToolStatus) bool { return s.Tool == n }) {
			return nil, fmt.Errorf("unknown worktree tool %q", n)
		}
	}
	var written []string
	for _, s := range statuses {
		wanted := slices.Contains(names, s.Tool) || len(names) == 0 && !s.OptIn
		if !wanted || s.State != ToolMissing {
			continue
		}
		var err error
		if s.Tool == "herdr" {
			err = in.setUpHerdr(ctx)
		} else if s.Tool == "paseo" {
			err = in.setUpPaseo(ctx)
		} else {
			err = in.writeRepoFile(ctx, root, s.Tool)
		}
		if err != nil {
			return written, fmt.Errorf("%s: %w", s.Name, err)
		}
		written = append(written, s.File)
	}
	return written, nil
}

// writeRepoFile excludes the tool's file before writing it, so it is never
// untracked and committable, even for a moment.
func (in *Installer) writeRepoFile(ctx context.Context, root, tool string) error {
	i := slices.IndexFunc(repoTools, func(t repoTool) bool { return t.id == tool })
	t := repoTools[i]
	if err := exclude(ctx, root, t.file); err != nil {
		return err
	}
	path := filepath.Join(root, t.file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, []byte(t.content), 0o644)
}

func gitIn(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
}

func tracked(ctx context.Context, root, file string) bool {
	out, err := gitIn(ctx, root, "ls-files", "--", file)
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// excludeFile is the clone's own ignore list, shared by all its worktrees.
func excludeFile(ctx context.Context, root string) (string, error) {
	out, err := gitIn(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not a git checkout", root)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "info", "exclude"), nil
}

func excluded(ctx context.Context, root, file string) bool {
	path, err := excludeFile(ctx, root)
	if err != nil {
		return false
	}
	b, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(b), "\n") {
		if l := strings.TrimSpace(line); l == file || l == "/"+file {
			return true
		}
	}
	return false
}

// exclude appends /file to the clone's exclude list once, keeping its lines.
func exclude(ctx context.Context, root, file string) error {
	if excluded(ctx, root, file) {
		return nil
	}
	path, err := excludeFile(ctx, root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return writeAtomic(path, append(b, "/"+file+"\n"...), 0o644)
}

// installed reports whether any of programs is on PATH or in ~/.local/bin,
// or any of dirs exists in the home directory.
func (in *Installer) installed(programs, dirs []string) bool {
	for _, p := range programs {
		if in.program(p) != "" {
			return true
		}
	}
	for _, d := range dirs {
		if st, err := os.Stat(filepath.Join(in.Home, d)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// program finds a tool the kit drives: the kit's own copy first, then
// anywhere doctor looks, which covers the Node version manager directories an
// npm global like Paseo lives in and calportd's own PATH does not have.
func (in *Installer) program(name string) string {
	p := filepath.Join(in.bin(), name)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	p, _ = doctor.Tool(name)
	return p
}

// HerdrPluginDir is where the kit's Herdr plugin lives. Herdr has no
// per-repository file, so the plugin serves the kit's checkout for this user.
func (in *Installer) HerdrPluginDir() string { return filepath.Join(in.Dir(), "herdr-plugin") }

func (in *Installer) herdrManifest() string {
	hook := filepath.Join(in.HerdrPluginDir(), "hook")
	return marker + "\n" +
		"id = \"" + herdrPluginID + "\"\nname = \"Cal.com worktrees\"\nversion = \"1.0.0\"\n" +
		"min_herdr_version = \"0.8.2\"\n" +
		"description = \"Run the Cal.com kit's setup and archive hooks for Herdr worktrees\"\n" +
		"platforms = [\"linux\", \"macos\"]\n\n" +
		"[[events]]\non = \"worktree.created\"\ncommand = [" + quoteJSON(hook) + ", \"setup\"]\n\n" +
		"[[events]]\non = \"worktree.removed\"\ncommand = [" + quoteJSON(hook) + ", \"archive\"]\n"
}

func (in *Installer) herdrStatus(ctx context.Context) ToolStatus {
	dir := in.HerdrPluginDir()
	s := ToolStatus{Tool: "herdr", Name: "Herdr", File: dir, OptIn: true}
	herdr := in.program("herdr")
	if s.Installed = herdr != ""; !s.Installed {
		s.State, s.Detail = ToolSkipped, "Herdr is not installed on this box."
		return s
	}
	manifest, _ := os.ReadFile(filepath.Join(dir, "herdr-plugin.toml"))
	hook, _ := os.ReadFile(filepath.Join(dir, "hook"))
	if string(manifest) == in.herdrManifest() && bytes.Equal(hook, herdrHook) && herdrLinked(ctx, herdr) {
		s.State, s.Detail = ToolConfigured, "A Herdr plugin for your user runs the hooks for Herdr worktrees of this checkout."
		return s
	}
	s.State, s.Detail = ToolMissing, "Opt-in: installs a Herdr plugin for your user on this box, which runs the hooks for Herdr worktrees of this checkout."
	return s
}

// PaseoPluginDir is where the kit's Paseo plugin lives. Like Herdr, Paseo has
// no per-repository file: the plugin is installed into the daemon for this
// user and decides per workspace whether the worktree is this checkout's.
func (in *Installer) PaseoPluginDir() string { return filepath.Join(in.Dir(), "paseo-plugin") }

func (in *Installer) paseoStatus(ctx context.Context) ToolStatus {
	dir := in.PaseoPluginDir()
	s := ToolStatus{Tool: "paseo", Name: "Paseo", File: dir, OptIn: true}
	paseo := in.program("paseo")
	if s.Installed = paseo != ""; !s.Installed {
		s.State, s.Detail = ToolSkipped, "Paseo is not installed on this box."
		return s
	}
	plugin, installed := paseoInstalled(ctx, paseo)
	if paseoPluginWritten(dir) && installed {
		// Installed and asked for, but the daemon has not loaded it: Paseo
		// has plugins switched off, and saying "configured" here would
		// promise hooks that never run. Paseo reports a loaded plugin as
		// "running", and one held back by that switch as "disabled".
		if plugin.Enabled && plugin.Status == "disabled" {
			s.State = ToolMissing
			s.Detail = "Installed, but this Paseo has plugins switched off, so its hooks never run. Switch them on: paseo daemon config set pluginsEnabled true, then paseo daemon restart."
			return s
		}
		s.State, s.Detail = ToolConfigured, "A Paseo plugin for your user sets up Paseo workspaces of this checkout, and holds an agent until its worktree is ready."
		return s
	}
	s.State, s.Detail = ToolMissing, "Opt-in: installs a Paseo plugin for your user on this box, which sets up Paseo workspaces of this checkout."
	return s
}

// paseoPlugin is what Paseo reports about the kit's plugin. status is what the
// daemon has loaded; enabled is what the configuration asks for. They differ
// when the daemon has plugins switched off, and then nothing runs however
// correctly the plugin is installed.
type paseoPlugin struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"`
}

// toolCmd runs a tool program resolves, with the tool's own directory ahead of
// PATH. An npm global is a script starting "#!/usr/bin/env node", so it runs
// only when the Node it was installed under is on PATH - and under a version
// manager that Node sits in the same directory as the tool itself. calportd's
// PATH has neither.
func toolCmd(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	path := filepath.Dir(program)
	if existing := os.Getenv("PATH"); existing != "" {
		path += string(os.PathListSeparator) + existing
	}
	cmd.Env = append(os.Environ(), "PATH="+path)
	return cmd
}

// paseoInstalled asks Paseo, rather than trusting the files on disk: a plugin
// directory can be written and never installed. The subcommand is ls - add is
// an alias of install, and there is no list.
func paseoInstalled(ctx context.Context, paseo string) (paseoPlugin, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := toolCmd(ctx, paseo, "plugin", "ls", "--json").Output()
	if err != nil {
		return paseoPlugin{}, false
	}
	var all []paseoPlugin
	if json.Unmarshal(out, &all) != nil {
		return paseoPlugin{}, false
	}
	for _, p := range all {
		if p.ID == paseoPluginID {
			return p, true
		}
	}
	return paseoPlugin{}, false
}

func (in *Installer) setUpPaseo(ctx context.Context) error {
	dir := in.PaseoPluginDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for rel, content := range paseoPluginFiles() {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := writeAtomic(path, content, 0o644); err != nil {
			return err
		}
	}
	paseo := in.program("paseo")
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// add refuses an id it already has, so a plugin whose files this calport
	// just rewrote is reloaded rather than installed again.
	if _, installed := paseoInstalled(ctx, paseo); installed {
		if out, err := toolCmd(ctx, paseo, "plugin", "reload", paseoPluginID).CombinedOutput(); err != nil {
			return fmt.Errorf("paseo plugin reload: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	if out, err := toolCmd(ctx, paseo, "plugin", "add", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("paseo plugin add: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func herdrLinked(ctx context.Context, herdr string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := toolCmd(ctx, herdr, "plugin", "list", "--json").Output()
	return err == nil && bytes.Contains(out, []byte(`"`+herdrPluginID+`"`))
}

func (in *Installer) setUpHerdr(ctx context.Context) error {
	dir := in.HerdrPluginDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "hook"), herdrHook, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "herdr-plugin.toml"), []byte(in.herdrManifest()), 0o644); err != nil {
		return err
	}
	herdr := in.program("herdr")
	if herdrLinked(ctx, herdr) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if out, err := toolCmd(ctx, herdr, "plugin", "link", dir, "--enabled").CombinedOutput(); err != nil {
		return fmt.Errorf("herdr plugin link: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
