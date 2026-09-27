package box

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/sean-brydon/calport/internal/doctor"
	"github.com/sean-brydon/calport/internal/kit"
)

// KitRequest installs the Cal.com kit for a location. Host is the URL label
// to use if the box has no kit yet; the laptop sends its name for the box.
type KitRequest struct {
	Location string `json:"location"`
	Host     string `json:"host"`
}

// KitResult is an install's outcome, including whether calport now runs the
// hooks itself because Orca has none set for the repository.
type KitResult struct {
	kit.Result
	HooksFrom string `json:"hooks_from"`
}

func (b *Box) kitStatus(w http.ResponseWriter, r *http.Request) error {
	if b.Kit == nil {
		writeJSON(w, kit.Status{})
		return nil
	}
	writeJSON(w, b.Kit.Status())
	return nil
}

func (b *Box) installKit(w http.ResponseWriter, r *http.Request) error {
	if b.Kit == nil {
		return httpError{http.StatusNotImplemented, "the Cal.com kit needs a Linux box with systemd"}
	}
	var req KitRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	loc, err := b.Locations.Get(r.Context(), req.Location)
	if err != nil {
		return err
	}
	res, err := b.Kit.Install(r.Context(), loc.Path, req.Host)
	if err != nil {
		return badRequest("%v", err)
	}
	// Worktrees calport makes (with git, Herdr, or Orca without hooks) run
	// the kit's hooks too, unless the location already has scripts.
	if loc.Scripts.Setup == "" && loc.Scripts.Archive == "" {
		if err := b.Locations.SetScripts(loc.Name, kit.SetupHook, kit.ArchiveHook); err != nil {
			return err
		}
		if loc, err = b.Locations.Get(r.Context(), loc.Name); err != nil {
			return err
		}
	}
	b.publish(r, "kit.installed", map[string]any{"location": loc.Name, "pattern": res.Pattern})
	writeJSON(w, KitResult{Result: res, HooksFrom: loc.Scripts.From})
	return nil
}

// kitChecks reports on the kit for each Cal.com location.
func (b *Box) kitChecks(ctx context.Context, locs []Location) []doctor.Check {
	const area = "Cal.com worktrees"
	var checks []doctor.Check
	var status kit.Status
	if b.Kit != nil {
		status = b.Kit.Status()
	}
	for _, l := range locs {
		if !l.Cal {
			continue
		}
		if !status.Installed {
			checks = append(checks, doctor.Check{Area: area, Name: l.Name, Status: doctor.Warn,
				Detail: "worktrees do not get their own port, database and URL", Fix: "calport kit install BOX/" + l.Name + "  (or Set up in the app)"})
			continue
		}
		checks = append(checks, doctor.Check{Area: area, Name: "kit", Status: doctor.OK, Detail: "URLs " + status.Pattern})
		if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", kit.RouterAddr); err != nil {
			checks = append(checks, doctor.Check{Area: area, Name: "router", Status: doctor.Fail, Detail: "not answering on " + kit.RouterAddr, Fix: "systemctl --user restart cal-worktree-proxy"})
		} else {
			c.Close()
			checks = append(checks, doctor.Check{Area: area, Name: "router", Status: doctor.OK, Detail: kit.RouterAddr})
		}
		if status.Config.Orca != "" && l.Scripts.From != "orca" {
			checks = append(checks, doctor.Check{Area: area, Name: "Orca hooks for " + l.Name, Status: doctor.Warn,
				Detail: "worktrees made in Orca's app skip setup", Fix: "In Orca, open the repository's Worktree Hooks settings and set Setup to " + kit.SetupHook + " and Archive to " + kit.ArchiveHook})
		}
	}
	return checks
}

func (c *Client) Kit(ctx context.Context) (out kit.Status, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/kit", nil, &out)
}

func (c *Client) InstallKit(ctx context.Context, req KitRequest) (out KitResult, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/kit", req, &out)
}

