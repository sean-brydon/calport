package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/kit"
)

// kitCheck handles `calport kit check BOX/LOCATION [--provider P] [--keep]`:
// it makes a throwaway worktree the way Orca's app does, waits for the kit's
// setup to bring its app up, loads it through this laptop, then archives it
// and checks the app stopped. It proves the whole path, hooks included.
func kitCheck(l laptop, args []string) error {
	fs := flag.NewFlagSet("kit check", flag.ContinueOnError)
	provider := fs.String("provider", "", "create the worktree with orca, herdr or git (default: orca when the box has it)")
	keep := fs.Bool("keep", false, "leave the worktree running afterwards")
	pos, err := parseAnywhere(fs, args)
	if err != nil || len(pos) != 1 {
		return errors.New("usage: calport kit check BOX/LOCATION [--provider orca|herdr|git] [--keep]")
	}
	boxName, location, ok := strings.Cut(pos[0], "/")
	if !ok || location == "" {
		return errors.New("usage: calport kit check BOX/LOCATION")
	}
	ctx := context.Background()
	wc, err := l.boxClient(boxName)
	if err != nil {
		return err
	}
	c := box.NewClient(wc)
	ag, err := ensureAgent(l)
	if err != nil {
		return err
	}
	started := time.Now()
	step := func(format string, a ...any) {
		fmt.Printf("[%5.0fs] %s\n", time.Since(started).Seconds(), fmt.Sprintf(format, a...))
	}

	st, err := c.Kit(ctx)
	if err != nil {
		return err
	}
	if !st.Installed {
		return fmt.Errorf("the Cal.com kit is not installed on %s; run: calport kit install %s", boxName, pos[0])
	}
	status, err := ag.Status(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(status.Routes, func(r agent.Route) bool { return r.Pattern == st.Pattern }) {
		return fmt.Errorf("this laptop has no route for %s; run: calport kit install %s", st.Pattern, pos[0])
	}
	locs, err := c.Locations(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(locs, func(x box.Location) bool { return x.Name == location })
	if i < 0 {
		return fmt.Errorf("%s has no location %q", boxName, location)
	}
	loc := locs[i]
	if *provider == "" {
		*provider = "git"
		if info, err := c.Info(ctx); err == nil && slices.Contains(info.Tools, "orca") {
			*provider = "orca"
		}
	}
	runs := "calport runs the location's scripts"
	if *provider == "orca" && loc.Scripts.From == "orca" {
		runs = "Orca runs its own hooks, as for worktrees made in its app"
	}
	step("Kit on %s serves %s; %s: setup %s", boxName, st.Pattern, runs, loc.Scripts.Setup)

	suffix := make([]byte, 3)
	rand.Read(suffix)
	name := "calport-check-" + hex.EncodeToString(suffix)
	wt, err := c.AddWorktree(ctx, location, box.WorktreeRequest{Name: name, Provider: *provider})
	if err != nil {
		return fmt.Errorf("creating %s with %s: %w", name, *provider, err)
	}
	step("Created %s/%s with %s at %s", location, name, *provider, wt.Path)
	cleanup := func() {
		if *keep {
			return
		}
		step("Archiving %s…", name)
		if err := c.RemoveWorktree(ctx, location, name, true); err != nil {
			step("✗ archiving failed: %v", err)
		}
	}

	web := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(host, path string) (int, []byte) {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(status.Proxy.Port)+path, nil)
		req.Host = host
		resp, err := web.Do(req)
		if err != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, b
	}

	// The kit registers the worktree once its setup starts.
	var rec kit.Worktree
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if s, err := c.Kit(ctx); err == nil {
			if j := slices.IndexFunc(s.Worktrees, func(w kit.Worktree) bool { return w.Path == wt.Path }); j >= 0 {
				rec = s.Worktrees[j]
				break
			}
		}
		if time.Now().After(deadline) {
			cleanup()
			return fmt.Errorf("✗ setup never started for %s: the setup hook did not run. Check the repository's Worktree Hooks in Orca (calport doctor %s)", name, boxName)
		}
		time.Sleep(3 * time.Second)
	}
	step("✓ Setup started: http://%s (port %d)", rec.Host, rec.Port)

	// Follow the setup log through the router until the app answers.
	seen := 0
	deadline = time.Now().Add(25 * time.Minute)
	for {
		if code, body := get(rec.Host, "/__worktree/logs/data"); code == http.StatusOK {
			var logs struct{ Setup string }
			if json.Unmarshal(body, &logs) == nil {
				lines := strings.Split(strings.TrimSpace(logs.Setup), "\n")
				for _, line := range lines[min(seen, len(lines)):] {
					if setupMilestone(line) {
						step("  %s", strings.TrimSpace(line))
					}
				}
				seen = len(lines)
			}
		}
		if code, _ := get(rec.Host, "/auth/login"); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			cleanup()
			return fmt.Errorf("✗ the app never answered at http://%s/auth/login; see http://%s/__worktree/logs", rec.Host, rec.Host)
		}
		time.Sleep(5 * time.Second)
	}
	step("✓ App answers through this laptop: http://%s/auth/login → 200", rec.Host)
	if *keep {
		step("Kept %s running. Remove it with: calport worktree rm %s/%s/%s", name, boxName, location, name)
		return nil
	}

	cleanup()
	deadline = time.Now().Add(5 * time.Minute)
	for {
		stopped := false
		if s, err := c.Kit(ctx); err == nil {
			j := slices.IndexFunc(s.Worktrees, func(w kit.Worktree) bool { return w.Path == wt.Path })
			stopped = j < 0 || !s.Worktrees[j].Active
		}
		if code, _ := get(rec.Host, "/auth/login"); stopped && code == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("✗ %s still runs after archiving: the archive hook did not stop it", name)
		}
		time.Sleep(3 * time.Second)
	}
	step("✓ Archived: the app stopped and its URL no longer answers")
	fmt.Printf("\nAll good: %s creates, serves and archives Cal.com worktrees end to end.\n", boxName)
	return nil
}

// setupMilestone picks the kit's own progress lines ("Apply migrations:
// 28.4s", "READY: …") out of a setup log that also holds yarn and Prisma.
func setupMilestone(line string) bool {
	if strings.ContainsAny(line, "\x1b➤") || strings.Contains(line, "YN0") {
		return false
	}
	label, took, ok := strings.Cut(line, ": ")
	if ok && label != "" && strings.HasSuffix(took, "s") {
		if _, err := strconv.ParseFloat(strings.TrimSuffix(took, "s"), 64); err == nil {
			return true
		}
	}
	return strings.HasPrefix(line, "READY") || strings.HasPrefix(line, "App startup") || strings.Contains(line, "Error")
}
