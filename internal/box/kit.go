package box

import (
	"context"
	"github.com/sean-brydon/calport/internal/events"
	"log"
	"net"
	"net/http"
	"net/url"
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

// KitToolsRequest sets up worktree tools for the kit's location: the named
// ones, or every missing one that is not opt-in.
type KitToolsRequest struct {
	Location string   `json:"location"`
	Tools    []string `json:"tools,omitempty"`
}

// KitTools is each worktree tool's state for the kit's checkout, and the
// files a setup wrote.
type KitTools struct {
	Root    string           `json:"root"`
	Tools   []kit.ToolStatus `json:"tools"`
	Written []string         `json:"written,omitempty"`
}

// kitLocation is the location the installed kit serves, as named.
func (b *Box) kitLocation(ctx context.Context, name string) (Location, error) {
	if b.Kit == nil {
		return Location{}, httpError{http.StatusNotImplemented, "the Cal.com kit needs a Linux box with systemd"}
	}
	loc, err := b.Locations.Get(ctx, name)
	if err != nil {
		return loc, err
	}
	if s := b.Kit.Status(); !s.Installed || s.Config.Root != loc.Path {
		return loc, badRequest("the Cal.com kit is not installed for %s; run: calport kit install BOX/%s", name, name)
	}
	return loc, nil
}

func orcaHooks(root string) kit.OrcaHooks {
	s, _ := OrcaScripts(root)
	return kit.OrcaHooks{Setup: s.Setup, Archive: s.Archive}
}

func (b *Box) kitTools(w http.ResponseWriter, r *http.Request) error {
	loc, err := b.kitLocation(r.Context(), r.URL.Query().Get("location"))
	if err != nil {
		return err
	}
	writeJSON(w, KitTools{Root: loc.Path, Tools: b.Kit.Tools(r.Context(), loc.Path, orcaHooks(loc.Path))})
	return nil
}

func (b *Box) setUpKitTools(w http.ResponseWriter, r *http.Request) error {
	var req KitToolsRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	loc, err := b.kitLocation(r.Context(), req.Location)
	if err != nil {
		return err
	}
	written, err := b.Kit.SetUpTools(r.Context(), loc.Path, req.Tools, orcaHooks(loc.Path))
	if err != nil {
		return badRequest("%v", err)
	}
	writeJSON(w, KitTools{Root: loc.Path, Tools: b.Kit.Tools(r.Context(), loc.Path, orcaHooks(loc.Path)), Written: written})
	return nil
}

// toolRunsKitHooks reports whether the tool making or removing a worktree at
// loc runs the location's hooks itself, so calport must not run them too.
func (b *Box) toolRunsKitHooks(ctx context.Context, tool string, loc Location) bool {
	if tool == "orca" && loc.Scripts.From == "orca" {
		return true
	}
	return loc.Scripts.Setup == kit.SetupHook && b.Kit != nil && b.Kit.RunsHooks(ctx, tool, loc.Path)
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
		if status.Config.Root != l.Path {
			continue
		}
		for _, t := range b.Kit.Tools(ctx, l.Path, orcaHooks(l.Path)) {
			fix := "calport kit tools BOX/" + l.Name + " --setup --tool " + t.Tool
			switch {
			case t.Tool == "orca" && status.Config.Orca != "" && t.State == kit.ToolMissing:
				checks = append(checks, doctor.Check{Area: area, Name: "Orca hooks for " + l.Name, Status: doctor.Warn,
					Detail: "worktrees made in Orca's app skip setup",
					Fix:    fix + "  (or set Worktree Hooks in Orca's settings to " + kit.SetupHook + " and " + kit.ArchiveHook + ")"})
			case t.Tool != "orca" && t.Installed && t.State == kit.ToolMissing:
				checks = append(checks, doctor.Check{Area: area, Name: t.Name + " hooks for " + l.Name, Status: doctor.Info,
					Detail: t.Name + " is on this box, but worktrees it makes skip setup", Fix: fix})
			}
		}
	}
	return checks
}

func (c *Client) KitTools(ctx context.Context, location string) (out KitTools, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/kit/tools?location="+url.QueryEscape(location), nil, &out)
}

func (c *Client) SetUpKitTools(ctx context.Context, req KitToolsRequest) (out KitTools, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/kit/tools", req, &out)
}

func (c *Client) Kit(ctx context.Context) (out kit.Status, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/kit", nil, &out)
}

func (c *Client) InstallKit(ctx context.Context, req KitRequest) (out KitResult, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/kit", req, &out)
}

func (c *Client) Stats(ctx context.Context) (out Stats, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/stats", nil, &out)
}

// reclaimRemoved frees a Cal.com worktree's database, port and services
// once calport itself removed its folder; it runs in the background.
func (b *Box) reclaimRemoved(loc Location, dir string) {
	if b.Kit == nil || !loc.Cal || !b.Kit.Status().Installed {
		return
	}
	go b.reclaim(context.Background(), kit.ReclaimOptions{Paths: []string{dir}}, "calport")
}

func (b *Box) reclaim(ctx context.Context, opts kit.ReclaimOptions, from string) (kit.ReclaimReport, error) {
	report, err := b.Kit.Reclaim(ctx, opts)
	if err != nil {
		log.Printf("kit reclaim: %v", err)
		return report, err
	}
	if !opts.DryRun && (len(report.Reclaimed) > 0 || len(report.Templates) > 0) {
		b.Events.Publish(events.Event{Type: "kit.reclaimed", Box: b.Name, Origin: from, Data: map[string]any{
			"worktrees": len(report.Reclaimed), "templates": len(report.Templates), "bytes": report.Bytes,
		}})
	}
	return report, nil
}

// SweepKit reclaims, every interval, what worktrees whose folder has been
// gone past the kit's grace period still hold.
func (b *Box) SweepKit(ctx context.Context, interval time.Duration) {
	if b.Kit == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if b.Kit.Status().Installed {
			b.reclaim(ctx, kit.ReclaimOptions{}, "calport")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (b *Box) handleReclaim(w http.ResponseWriter, r *http.Request) error {
	if b.Kit == nil {
		return httpError{http.StatusNotImplemented, "the Cal.com kit needs a Linux box with systemd"}
	}
	var opts kit.ReclaimOptions
	if err := decode(r, &opts); err != nil {
		return err
	}
	report, err := b.reclaim(r.Context(), opts, origin(r))
	if err != nil {
		return badRequest("%v", err)
	}
	writeJSON(w, report)
	return nil
}

func (c *Client) Reclaim(ctx context.Context, opts kit.ReclaimOptions) (out kit.ReclaimReport, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/kit/reclaim", opts, &out)
}
