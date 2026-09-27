package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/doctor"
	"github.com/sean-brydon/calport/internal/pfredirect"
	"github.com/sean-brydon/calport/internal/service"
)

// runDoctor checks this laptop, or with a box name, asks that box for its own
// report. It never changes anything.
func runDoctor(l laptop, args []string) error {
	fs, asJSON, err := flags("doctor", args, nil)
	if err != nil {
		return err
	}
	var checks []doctor.Check
	if fs.NArg() == 1 {
		wc, err := l.boxClient(fs.Arg(0))
		if err != nil {
			return err
		}
		defer wc.Reset()
		if checks, err = box.NewClient(wc).Doctor(context.Background()); err != nil {
			return fmt.Errorf("%s: %w", fs.Arg(0), err)
		}
	} else {
		checks = laptopChecks(l)
	}
	if asJSON {
		return printJSON(checks)
	}
	if problems := doctor.Print(os.Stdout, checks); problems > 0 {
		fmt.Printf("\n%d thing(s) to fix.\n", problems)
	} else {
		fmt.Println("\nAll good.")
	}
	return nil
}

func laptopChecks(l laptop) []doctor.Check {
	const mac = "This computer"
	var checks []doctor.Check
	c := agent.NewClient(l.socket())
	if spec, err := agentService(l); err == nil && service.Installed(spec) {
		checks = append(checks, doctor.Check{Area: mac, Name: "starts at login", Status: doctor.OK, Detail: "background agent installed"})
	} else {
		checks = append(checks, doctor.Check{Area: mac, Name: "starts at login", Status: doctor.Warn, Detail: "the agent only runs while something starts it", Fix: "calport agent install  (or open the Calport app)"})
	}
	status, err := c.Status(context.Background())
	if err != nil {
		return append(checks, doctor.Check{Area: mac, Name: "agent", Status: doctor.Fail, Detail: "not running", Fix: "calport status  (starts it)"})
	}
	checks = append(checks, doctor.Check{Area: mac, Name: "agent", Status: doctor.OK, Detail: "running"})
	if status.Proxy.Error != "" {
		checks = append(checks, doctor.Check{Area: mac, Name: "local URLs", Status: doctor.Fail, Detail: status.Proxy.Error, Fix: "Free port 1355, then: calport stop && calport status"})
	} else {
		checks = append(checks, doctor.Check{Area: mac, Name: "local URLs", Status: doctor.OK, Detail: serviceURLFor("PORT", "BOX", status.Proxy.URLPort)})
	}
	if runtime.GOOS == "darwin" {
		switch {
		case !pfredirect.Installed(status.Proxy.Port):
			checks = append(checks, doctor.Check{Area: mac, Name: "short URLs", Status: doctor.Info, Detail: "URLs include :1355", Fix: "calport setup port80"})
		default:
			checks = append(checks, port80Checks(mac)...)
		}
	}

	var nets []map[string]any
	c.Call(context.Background(), "GET", "/v1/networks", nil, &nets)
	for _, n := range nets {
		name, _ := n["name"].(string)
		state, _ := n["state"].(string)
		check := doctor.Check{Area: "Networks", Name: name, Status: doctor.OK, Detail: fmt.Sprintf("%v", n["tailnet"])}
		switch state {
		case "Running":
		case "NeedsLogin":
			check.Status, check.Detail, check.Fix = doctor.Fail, "signed out", "calport network login "+name
		default:
			check.Status, check.Detail = doctor.Warn, state
		}
		checks = append(checks, check)
	}

	if len(status.Boxes) == 0 {
		checks = append(checks, doctor.Check{Area: "Boxes", Name: "boxes", Status: doctor.Info, Detail: "none paired", Fix: "calport add ssh HOST"})
	}
	for _, b := range status.Boxes {
		check := doctor.Check{Area: "Boxes", Name: b.Name, Status: doctor.OK, Detail: fmt.Sprintf("online, %dms", b.LatencyMs)}
		switch b.State {
		case agent.StateOffline:
			check.Status, check.Detail, check.Fix = doctor.Warn, "offline: "+b.Error, "Check the box is on, then on it: calportd doctor"
		case agent.StateUntrusted:
			check.Status, check.Detail, check.Fix = doctor.Fail, "revoked this laptop", "calport add ssh "+b.Name+"  (pairs again)"
		case agent.StateConnecting:
			check.Status, check.Detail = doctor.Info, "connecting"
		}
		checks = append(checks, check)
	}

	home, _ := os.UserHomeDir()
	integration := func(name, file, marker, fix string) doctor.Check {
		b, err := os.ReadFile(filepath.Join(home, file))
		if err == nil && bytes.Contains(b, []byte(marker)) {
			return doctor.Check{Area: "Tools on this computer", Name: name, Status: doctor.OK, Detail: "connected"}
		}
		return doctor.Check{Area: "Tools on this computer", Name: name, Status: doctor.Info, Detail: "not connected", Fix: fix}
	}
	checks = append(checks,
		integration("Claude Code", ".claude/settings.json", "hook claude Stop", "calport integrations install claude"),
		integration("Cursor", ".cursor/hooks.json", "hook cursor stop", "calport integrations install cursor"),
		integration("Codex", ".codex/skills/calport/SKILL.md", "name: calport", "calport integrations install codex"),
	)
	return append(checks, doctor.OrcaChecks(context.Background(), "Orca on this computer", nil)...)
}

// port80Checks asks port 80 on each loopback address who answers, because an
// installed redirect proves nothing until requests actually arrive.
func port80Checks(area string) []doctor.Check {
	var checks []doctor.Check
	for _, addr := range []string{"[::1]:80", "127.0.0.1:80"} {
		name := "short URLs via " + strings.Trim(strings.TrimSuffix(addr, ":80"), "[]")
		switch who := whoAnswers(addr); who {
		case "calport":
			checks = append(checks, doctor.Check{Area: area, Name: name, Status: doctor.OK, Detail: "reaches calport"})
		case "":
			checks = append(checks, doctor.Check{Area: area, Name: name, Status: doctor.Warn, Detail: "nothing answers, so browsers fall back to the other address", Fix: "calport setup port80"})
		default:
			checks = append(checks, doctor.Check{Area: area, Name: name, Status: doctor.Warn, Detail: "answered by another program: " + who, Fix: "Stop that program, then: calport setup port80"})
		}
	}
	return checks
}

func whoAnswers(addr string) string {
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Host = "localhost"
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if strings.Contains(string(body), "<h1 style=\"font-size:20px\">calport</h1>") {
		return "calport"
	}
	if strings.Contains(string(body), "Cal.com worktree") {
		return "tailmux's worktree proxy (io.tailmux.cal-worktrees)"
	}
	if s := resp.Header.Get("Server"); s != "" {
		return s
	}
	return "an unknown web server"
}
