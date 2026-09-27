package box

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

// Service is a listening port that belongs to a worktree, found by where its
// process runs: Next.js in ~/work/cal-billing/apps/web serves cal/billing.
type Service struct {
	Location string `json:"location"`
	Worktree string `json:"worktree"`
	Path     string `json:"path"`
	Port     int    `json:"port"`
	Process  string `json:"process,omitempty"`
	// Main marks the location's own checkout, reachable as LOCATION.BOX.
	Main bool `json:"main,omitempty"`
}

// Services joins listening ports with location worktrees. A process inside
// nested paths belongs to the deepest worktree containing it.
func Services(ports []Port, locations []Location) []Service {
	type tree struct {
		location, name, path string
		main                 bool
	}
	var trees []tree
	for _, l := range locations {
		if !l.Repo {
			trees = append(trees, tree{l.Name, l.Name, l.Path, true})
			continue
		}
		for _, w := range l.Worktrees {
			trees = append(trees, tree{l.Name, w.Name, w.Path, w.Main})
		}
	}
	sort.Slice(trees, func(i, j int) bool { return len(trees[i].path) > len(trees[j].path) })
	var out []Service
	for _, p := range ports {
		if p.Dir == "" {
			continue
		}
		for _, t := range trees {
			if p.Dir == t.path || strings.HasPrefix(p.Dir, t.path+string(filepath.Separator)) {
				out = append(out, Service{Location: t.location, Worktree: t.name, Path: t.path, Port: p.Port, Process: p.Command, Main: t.main})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func (b *Box) handleServices(w http.ResponseWriter, r *http.Request) error {
	ports, err := ListPorts(r.Context())
	if err != nil {
		return err
	}
	locs, err := b.Locations.List(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, Services(ports, locs))
	return nil
}

// OpenRequest hands a worktree to Orca or Herdr, optionally starting an agent
// there. calport does not manage the session itself.
type OpenRequest struct {
	Tool  string `json:"tool"`
	Agent string `json:"agent,omitempty"`
	// HerdrSession selects the Herdr session to open the workspace in.
	HerdrSession string `json:"herdr_session,omitempty"`
}

func (b *Box) handleOpen(w http.ResponseWriter, r *http.Request) error {
	var req OpenRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	location, worktree := r.PathValue("name"), r.PathValue("worktree")
	loc, err := b.Locations.Get(r.Context(), location)
	if err != nil {
		return err
	}
	dir, err := b.Locations.Dir(r.Context(), location+"/"+worktree)
	if err != nil {
		return err
	}
	if err := OpenIn(r.Context(), req, loc.Path, dir, worktree); err != nil {
		return err
	}
	b.publish(r, "worktree.opened", map[string]any{"location": location, "name": worktree, "path": dir, "tool": req.Tool, "agent": req.Agent})
	writeJSON(w, map[string]string{"opened": dir, "tool": req.Tool})
	return nil
}

// Tools reports which handoff targets and agents this box has.
func Tools() []string {
	var have []string
	for _, t := range []string{"orca", "herdr", "claude", "codex"} {
		if _, err := toolPath(t); err == nil {
			have = append(have, t)
		}
	}
	return have
}
