package box

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// HerdrSessionName is the Herdr session a laptop adds a box's agents from.
const HerdrSessionName = "agents"

// HerdrStatus is what a box reports about its Herdr, so a laptop can add the
// box to its own Herdr window or say what is in the way.
type HerdrStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
	// Updatable is false when Herdr lives where this user cannot write, such
	// as a package manager's /usr/bin: `herdr update` cannot replace it there.
	Updatable bool `json:"updatable"`
	// User is who calportd runs as, and so who a laptop logs in as over SSH.
	User     string         `json:"user"`
	Sessions []HerdrSession `json:"sessions"`
}

type HerdrSession struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Agents  int    `json:"agents"`
}

// HerdrUpdate reports an update: the version before, the state after, and the
// sessions it stopped, which start again the next time anything attaches.
type HerdrUpdate struct {
	From    string      `json:"from"`
	Status  HerdrStatus `json:"status"`
	Stopped []string    `json:"stopped"`
}

func herdrStatus(ctx context.Context) (HerdrStatus, error) {
	s := HerdrStatus{Sessions: []HerdrSession{}}
	if u, err := user.Current(); err == nil {
		s.User = u.Username
	}
	path, err := toolPath("herdr")
	if err != nil {
		return s, nil
	}
	s.Installed, s.Path = true, path
	const writable = 0x2 // W_OK, which syscall does not name.
	s.Updatable = syscall.Access(filepath.Dir(path), writable) == nil
	out, err := runTool(ctx, nil, "herdr", "--version")
	if err != nil {
		return s, err
	}
	s.Version = strings.TrimPrefix(strings.TrimSpace(string(out)), "herdr ")
	out, err = runTool(ctx, nil, "herdr", "session", "list", "--json")
	if err != nil {
		return s, err
	}
	var list struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return s, fmt.Errorf("herdr session list returned unexpected output: %.200s", out)
	}
	for _, l := range list.Sessions {
		hs := HerdrSession{Name: l.Name, Running: l.Running}
		if l.Running {
			if hs.Agents, err = herdrAgents(ctx, l.Name); err != nil {
				return s, err
			}
		}
		s.Sessions = append(s.Sessions, hs)
	}
	return s, nil
}

func herdrAgents(ctx context.Context, session string) (int, error) {
	out, err := runTool(ctx, []string{"HERDR_SESSION=" + session}, "herdr", "agent", "list")
	if err != nil {
		return 0, err
	}
	var resp struct {
		Result struct {
			Agents []json.RawMessage `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, fmt.Errorf("herdr agent list returned unexpected output: %.200s", out)
	}
	return len(resp.Result.Agents), nil
}

// updateHerdr installs the latest Herdr. Herdr will not replace itself while
// an older server runs, so this stops the box's sessions first, and refuses
// while any of them hosts an agent: stopping it would end someone's work.
func updateHerdr(ctx context.Context) (HerdrUpdate, error) {
	before, err := herdrStatus(ctx)
	if err != nil {
		return HerdrUpdate{}, err
	}
	if !before.Installed {
		return HerdrUpdate{}, httpError{http.StatusNotFound, "Herdr is not installed on this box"}
	}
	if !before.Updatable {
		return HerdrUpdate{}, httpError{http.StatusConflict, fmt.Sprintf("Herdr at %s was installed by a package manager; update it with that", before.Path)}
	}
	var busy []string
	for _, s := range before.Sessions {
		if s.Agents > 0 {
			busy = append(busy, fmt.Sprintf("%s (%d)", s.Name, s.Agents))
		}
	}
	if len(busy) > 0 {
		return HerdrUpdate{}, httpError{http.StatusConflict, "agents are running in Herdr sessions " + strings.Join(busy, ", ") + "; updating would stop them"}
	}
	u := HerdrUpdate{From: before.Version, Stopped: []string{}}
	for _, s := range before.Sessions {
		if !s.Running {
			continue
		}
		if _, err := runTool(ctx, nil, "herdr", "session", "stop", s.Name); err != nil {
			return u, err
		}
		u.Stopped = append(u.Stopped, s.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := runTool(ctx, nil, "herdr", "update")
	if err != nil {
		return u, err
	}
	// Herdr exits 0 when it declines to update, so read what it said.
	if strings.Contains(string(out), "not updated") {
		return u, fmt.Errorf("herdr did not update: %s", strings.TrimSpace(string(out)))
	}
	u.Status, err = herdrStatus(ctx)
	return u, err
}

func (b *Box) handleHerdr(w http.ResponseWriter, r *http.Request) error {
	s, err := herdrStatus(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, s)
	return nil
}

func (b *Box) handleHerdrUpdate(w http.ResponseWriter, r *http.Request) error {
	u, err := updateHerdr(r.Context())
	if err != nil {
		return err
	}
	b.publish(r, "herdr.updated", map[string]any{"from": u.From, "version": u.Status.Version})
	writeJSON(w, u)
	return nil
}
