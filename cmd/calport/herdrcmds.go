package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/herdr"
)

const herdrUsage = `Usage:
  calport herdr [status] [--json]        Which boxes are in this computer's Herdr, and what is in the way
  calport herdr setup [BOX...] [--json]  Add boxes (default: every one that is ready) to this computer's Herdr
  calport herdr update BOX [--json]      Update Herdr on a box; refuses while an agent runs in it
  calport herdr open                     Run Herdr here: this computer and every added box

Herdr reaches other machines over SSH. setup writes an SSH host per box,
calport-BOX, that travels over calport's own network, and includes them from
~/.ssh/config. Logging in stays yours: if a box wants a key, put it under
Host calport-BOX in ~/.ssh/config.
`

// What stands between a box and this computer's Herdr, one state per box.
const (
	herdrReady    = "ready"    // added, and Herdr reaches it
	herdrAdd      = "add"      // calport herdr setup adds it
	herdrSSH      = "ssh"      // SSH to it does not log in without a prompt
	herdrUpdate   = "update"   // its Herdr predates saved machines: calport herdr update
	herdrPackaged = "packaged" // as update, but a package manager owns its Herdr
	herdrMissing  = "missing"  // no Herdr on the box
	herdrUpgrade  = "upgrade"  // its calportd predates Herdr support: calport upgrade
	herdrOffline  = "offline"
	herdrError    = "error"
)

type herdrBox struct {
	Box     string `json:"box"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
	Host    string `json:"host"`
	Version string `json:"version,omitempty"`
}

type herdrReport struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// Supported is false when this computer's Herdr predates saved machines.
	Supported bool       `json:"supported"`
	Boxes     []herdrBox `json:"boxes"`
}

func herdrCommand(l laptop, args []string) error {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	if action == "help" {
		fmt.Print(herdrUsage)
		return nil
	}
	fs, asJSON, err := flags("herdr "+action, args, nil)
	if err != nil {
		return err
	}
	names := fs.Args()
	ctx, cancel := signalContext()
	defer cancel()
	switch action {
	case "status":
		sc, err := scanHerdr(ctx, l)
		if err != nil {
			return err
		}
		return printHerdr(sc.report, asJSON)
	case "setup":
		report, err := setUpHerdr(ctx, l, names)
		if err != nil {
			return err
		}
		return printHerdr(report, asJSON)
	case "update":
		if len(names) != 1 {
			return errors.New("usage: calport herdr update BOX")
		}
		wc, err := l.boxClient(names[0])
		if err != nil {
			return err
		}
		u, err := box.NewClient(wc).UpdateHerdr(ctx)
		if err != nil {
			return err
		}
		if asJSON {
			return printJSON(u)
		}
		fmt.Printf("Updated Herdr on %s from %s to %s.\n", names[0], u.From, u.Status.Version)
		if len(u.Stopped) > 0 {
			fmt.Printf("Stopped its idle sessions (%s); each starts again when something attaches.\n", strings.Join(u.Stopped, ", "))
		}
		return nil
	case "open":
		cli, err := herdr.Find()
		if err != nil {
			return err
		}
		// Replace this process rather than opening another window: herdr is a
		// terminal program, and the terminal someone typed this in is the one
		// they meant. Opening a second one leaves them looking at the wrong
		// window, with their shell's directory and environment left behind.
		return syscall.Exec(cli.Path, []string{cli.Path}, os.Environ())
	}
	return fmt.Errorf("unknown herdr action %q; run calport herdr help", action)
}

// herdrScan is the report plus what setup needs to act on it.
type herdrScan struct {
	report herdrReport
	cli    herdr.CLI
	saved  map[string]bool // SSH targets this computer's Herdr has saved
	boxes  map[string]agent.BoxStatus
	remote map[string]box.HerdrStatus
}

func scanHerdr(ctx context.Context, l laptop) (*herdrScan, error) {
	c, err := ensureAgent(l)
	if err != nil {
		return nil, err
	}
	st, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}
	sc := &herdrScan{report: herdrReport{Boxes: []herdrBox{}}, saved: map[string]bool{}, boxes: map[string]agent.BoxStatus{}, remote: map[string]box.HerdrStatus{}}
	if cli, err := herdr.Find(); err == nil {
		sc.cli, sc.report.Installed = cli, true
		if v, err := cli.Version(ctx); err == nil {
			sc.report.Version, sc.report.Supported = v, !herdr.Older(v, herdr.MinVersion)
		}
	}
	statuses := map[string]herdr.MachineStatus{}
	if sc.report.Supported {
		machines, err := sc.cli.Machines(ctx)
		if err != nil {
			return nil, err
		}
		byTarget := map[string]string{}
		for _, m := range machines {
			sc.saved[m.Target], byTarget[m.ID] = true, m.Target
		}
		if len(machines) > 0 {
			byID, err := sc.cli.MachineStatuses(ctx)
			if err != nil {
				return nil, err
			}
			for id, s := range byID {
				statuses[byTarget[id]] = s
			}
		}
	}

	rows := make([]herdrBox, len(st.Boxes))
	remote := make([]box.HerdrStatus, len(st.Boxes))
	var wg sync.WaitGroup
	for i, b := range st.Boxes {
		wg.Go(func() { rows[i], remote[i] = boxHerdr(ctx, l, b) })
	}
	wg.Wait()
	for i, b := range st.Boxes {
		row := rows[i]
		sc.boxes[b.Name], sc.remote[b.Name] = b, remote[i]
		if row.State == herdrAdd && sc.saved[row.Host] {
			if s := statuses[row.Host]; s.Status == "reachable" {
				row.State = herdrReady
			} else {
				row.State, row.Detail = herdrSSH, sshHint(row.Host, cmpOr(s.Error, s.Status, "Herdr could not reach it"))
			}
		}
		sc.report.Boxes = append(sc.report.Boxes, row)
	}
	return sc, nil
}

// boxHerdr asks one box about its Herdr and says what that means here.
func boxHerdr(ctx context.Context, l laptop, b agent.BoxStatus) (herdrBox, box.HerdrStatus) {
	row := herdrBox{Box: b.Name, Host: herdr.Host(b.Name)}
	if b.State != "online" {
		row.State, row.Detail = herdrOffline, fmt.Sprintf("%s is %s.", b.Name, b.State)
		return row, box.HerdrStatus{}
	}
	wc, err := l.boxClient(b.Name)
	if err != nil {
		row.State, row.Detail = herdrError, err.Error()
		return row, box.HerdrStatus{}
	}
	s, err := box.NewClient(wc).Herdr(ctx)
	row.Version = s.Version
	switch {
	case errors.Is(err, box.ErrHerdrUnsupported):
		row.State, row.Detail = herdrUpgrade, "Its calportd cannot report Herdr yet; upgrade the box's daemon."
	case err != nil:
		row.State, row.Detail = herdrError, err.Error()
	case !s.Installed:
		row.State, row.Detail = herdrMissing, fmt.Sprintf("Herdr is not installed on %s; see herdr.dev.", b.Name)
	case herdr.Older(s.Version, herdr.MinVersion) && s.Updatable:
		row.State, row.Detail = herdrUpdate, fmt.Sprintf("Herdr %s predates saved machines, which need %s.", s.Version, herdr.MinVersion)
	case herdr.Older(s.Version, herdr.MinVersion):
		row.State, row.Detail = herdrPackaged, fmt.Sprintf("Herdr %s at %s predates saved machines and belongs to a package manager; update it with that.", s.Version, s.Path)
	default:
		row.State = herdrAdd
	}
	return row, s
}

// setUpHerdr adds the named boxes, or every box ready to add, to this
// computer's Herdr. A box that cannot be added keeps the state that says why.
func setUpHerdr(ctx context.Context, l laptop, names []string) (herdrReport, error) {
	sc, err := scanHerdr(ctx, l)
	if err != nil {
		return herdrReport{}, err
	}
	switch {
	case !sc.report.Installed:
		return herdrReport{}, errors.New("herdr is not installed on this computer; see herdr.dev")
	case !sc.report.Supported:
		return herdrReport{}, fmt.Errorf("this computer's Herdr %s predates saved machines; run herdr update", sc.report.Version)
	}
	want := map[string]bool{}
	for _, n := range names {
		if _, ok := sc.boxes[n]; !ok {
			return herdrReport{}, fmt.Errorf("no paired box named %q; see calport boxes", n)
		}
		want[n] = true
	}
	cfg, err := herdr.DefaultConfig()
	if err != nil {
		return herdrReport{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return herdrReport{}, err
	}
	for i := range sc.report.Boxes {
		row := &sc.report.Boxes[i]
		// Ready boxes are rewritten too, so their hosts follow a moved box or a
		// calport that now lives somewhere else.
		if (len(want) > 0 && !want[row.Box]) || (row.State != herdrAdd && row.State != herdrSSH && row.State != herdrReady) {
			continue
		}
		b := sc.boxes[row.Box]
		address, _, err := net.SplitHostPort(b.Address)
		if err != nil {
			address = b.Address
		}
		if err := cfg.Write(herdr.SSHHost{Box: row.Box, Address: address, User: sc.remote[row.Box].User, Network: b.Network, Calport: exe}); err != nil {
			return herdrReport{}, err
		}
		if err := cfg.EnsureInclude(); err != nil {
			return herdrReport{}, err
		}
		if err := herdr.CheckSSH(ctx, row.Host); err != nil {
			row.State, row.Detail = herdrSSH, sshHint(row.Host, err.Error())
			continue
		}
		if !sc.saved[row.Host] {
			if err := sc.cli.AddMachine(ctx, row.Host, row.Box, box.HerdrSessionName); err != nil {
				row.State, row.Detail = herdrError, err.Error()
				continue
			}
		}
		row.State, row.Detail = herdrReady, ""
	}
	return sc.report, nil
}

func sshHint(host, why string) string {
	return fmt.Sprintf("SSH to %s does not log in without a prompt (%s). Put your key for it under Host %s in ~/.ssh/config.", host, strings.TrimSuffix(why, "."), host)
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func printHerdr(r herdrReport, asJSON bool) error {
	if asJSON {
		return printJSON(r)
	}
	switch {
	case !r.Installed:
		fmt.Println("Herdr is not installed on this computer; see herdr.dev.")
	case !r.Supported:
		fmt.Printf("Herdr %s on this computer predates saved machines; run herdr update.\n", r.Version)
	default:
		fmt.Printf("Herdr %s on this computer.\n", r.Version)
	}
	if len(r.Boxes) == 0 {
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BOX\tSTATE\tHERDR\tDETAIL")
	adding := false
	for _, b := range r.Boxes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Box, b.State, cmpOr(b.Version, "-"), b.Detail)
		adding = adding || b.State == herdrAdd
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if adding && r.Supported {
		fmt.Println("Add them: calport herdr setup")
	}
	return nil
}
