package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// holder is the launchd job listening on a port calport wants.
type holder struct {
	// Label is the job's launchd label, which is also how it is booted out.
	Label string
	// System marks a job from /Library/LaunchDaemons: freeing it needs root.
	System bool
	Path   string
}

type jobDir struct {
	path   string
	system bool
}

// jobDirs lists where launchd keeps job definitions, system scope first so a
// root-owned listener is reported ahead of a user one. A variable so tests can
// redirect it.
var jobDirs = func() []jobDir {
	out := []jobDir{
		{path: "/Library/LaunchDaemons", system: true},
		{path: "/Library/LaunchAgents", system: true},
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, jobDir{path: filepath.Join(home, "Library", "LaunchAgents")})
	}
	return out
}

// portHolder names the launchd job that listens on port. It reads job
// definitions rather than the running process because a listener on a
// privileged port is owned by root, and lsof shows an unprivileged caller
// nothing at all - while the plists that create one are world-readable.
//
// macOS only. Elsewhere the caller keeps whatever it could learn from the
// response itself.
func portHolder(port int) (holder, bool) {
	if runtime.GOOS != "darwin" {
		return holder{}, false
	}
	for _, dir := range jobDirs() {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".plist" {
				continue
			}
			path := filepath.Join(dir.path, e.Name())
			label, listens := jobListensOn(path, port)
			if !listens {
				continue
			}
			if label == "" {
				label = strings.TrimSuffix(e.Name(), ".plist")
			}
			return holder{Label: label, System: dir.system, Path: path}, true
		}
	}
	return holder{}, false
}

// jobListensOn reports whether the job's arguments name this port, and its
// label. The whole file is scanned for strings rather than only
// ProgramArguments, because a job can take its address from any argument.
func jobListensOn(path string, port int) (label string, listens bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var doc struct {
		Strings []string `xml:"dict>array>string"`
		Keys    []string `xml:"dict>key"`
		Values  []string `xml:"dict>string"`
	}
	if xml.Unmarshal(b, &doc) != nil {
		return "", false
	}
	for i, k := range doc.Keys {
		if k == "Label" && i < len(doc.Values) {
			label = doc.Values[i]
			break
		}
	}
	for _, s := range doc.Strings {
		if argNamesPort(s, port) {
			return label, true
		}
	}
	return label, false
}

// argNamesPort reports whether an argument names exactly this port, so that
// 8080 and 1080 are not read as port 80.
func argNamesPort(arg string, port int) bool {
	want := strconv.Itoa(port)
	if arg == want {
		return true
	}
	i := strings.LastIndex(arg, ":")
	if i < 0 {
		return false
	}
	return arg[i+1:] == want
}

// holderFix is the sequence that frees the port and hands it to calport.
// bootout alone is not enough: a job with RunAtLoad returns at the next boot,
// so disable is what makes it stick.
func holderFix(h holder) string {
	target, sudo := "gui/$(id -u)/"+h.Label, ""
	if h.System {
		target, sudo = "system/"+h.Label, "sudo "
	}
	return fmt.Sprintf("%slaunchctl bootout %s\n%slaunchctl disable %s\ncalport setup port80", sudo, target, sudo, target)
}

// holderDetail describes the job in the terms that decide how to free it.
//
// It says "configured to listen here" rather than naming the job as the one
// answering: a job definition says what would listen, not what does, and a
// definition left on disk after its job was booted out still matches. The
// caller only reaches this after something non-calport answered, so the job is
// the likely culprit and the right thing to act on - but the wording should
// not claim more than a plist can tell us.
func holderDetail(h holder) string {
	kind := "a user LaunchAgent"
	if h.System {
		kind = "a root LaunchDaemon"
	}
	return fmt.Sprintf("answered by another program; %s (%s) is configured to listen here", h.Label, kind)
}
