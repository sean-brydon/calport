// Command calportd runs on a box: it holds the box identity, issues pairing
// links, and serves paired laptops.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/boxcmd"
	"github.com/sean-brydon/calport/internal/doctor"
	"github.com/sean-brydon/calport/internal/events"
	"github.com/sean-brydon/calport/internal/hooks"
	"github.com/sean-brydon/calport/internal/identity"
	"github.com/sean-brydon/calport/internal/integrations"
	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/service"
	"github.com/sean-brydon/calport/internal/statefile"
	"github.com/sean-brydon/calport/internal/trust"
	"github.com/sean-brydon/calport/internal/wire"
)

const (
	defaultPort = "7443"
	defaultTTL  = 10 * time.Minute
)

const usage = `calportd — the calport daemon for a development box

  calportd serve [--listen ADDR]            Serve paired laptops (default: tailnet address only)
  calportd install [--listen ADDR]          Run serve as a user service (systemd/launchd)
  calportd uninstall                        Remove that service
  calportd pair [--address HOST[:PORT]] [--ttl 10m]
                                            Print a single-use pairing link
  calportd clients                          List paired laptops
  calportd revoke <name|fingerprint>        Stop trusting a laptop
  calportd id                               Print this box's fingerprint
  calportd doctor [--json]                  Check this box's setup and how to fix it
  calportd session attach NAME              Attach to a session in this terminal

Hooks run from <state>/box/hooks.json; see docs/integrations.md.
CALPORT_HOME overrides the state directory.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "calportd:", err)
		os.Exit(1)
	}
}

type boxHome struct {
	dir string
}

func (b boxHome) socket() string { return filepath.Join(b.dir, "calportd.sock") }

func (b boxHome) identity() (*identity.Identity, error) {
	return identity.LoadOrCreate(filepath.Join(b.dir, "identity.pem"))
}
func (b boxHome) clients() *trust.Store { return trust.NewStore(filepath.Join(b.dir, "clients.json")) }
func (b boxHome) pending() *pairing.Pending {
	return pairing.NewPending(filepath.Join(b.dir, "pairing.json"))
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		fmt.Println()
		fmt.Print(boxcmd.Usage("calportd", ""))
		fmt.Println()
		fmt.Printf(integrations.Usage, "calportd")
		return nil
	}
	home, err := statefile.Home()
	if err != nil {
		return err
	}
	b := boxHome{dir: filepath.Join(home, "box")}
	switch args[0] {
	case "serve":
		return serve(b, args[1:])
	case "pair":
		return pair(b, args[1:])
	case "install":
		return install(b, args[1:])
	case "uninstall":
		path, err := service.Uninstall(daemonService(b, ""))
		if err != nil {
			return err
		}
		if path == "" {
			fmt.Println("calportd is not installed as a service.")
		} else {
			fmt.Println("Removed " + path)
		}
		return nil
	case "clients":
		return listClients(b)
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: calportd revoke <name|fingerprint>")
		}
		p, err := b.clients().Remove(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Revoked %s (%s)\n", p.Name, p.Fingerprint.Short())
		return nil
	case "id":
		id, err := b.identity()
		if err != nil {
			return err
		}
		fmt.Println(id.Fingerprint())
		return nil
	}
	switch args[0] {
	case "doctor":
		return runDoctor(b, args[1:])
	case "hook":
		integrations.Hook(args[1:], os.Stdin, os.Stdout, os.Stderr, func(e events.Event) error {
			if _, err := os.Stat(b.socket()); err != nil {
				return nil
			}
			c := box.NewClient(box.NewLocal(b.socket()))
			c.Origin = e.Origin
			return c.Emit(context.Background(), e.Type, e.Data)
		})
		return nil
	case "kit":
		return runKit(b, args[1:])
	case "integrations":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return integrations.Install(args[1:], exe, os.Stdout)
	}
	if len(args) >= 2 && args[0] == "session" && args[1] == "attach" {
		return attachLocal(args[2:])
	}
	if _, ok := boxcmd.Commands[args[0]]; ok {
		return runLocal(b, args)
	}
	return fmt.Errorf("unknown command %q; run calportd help", args[0])
}

func serve(b boxHome, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to listen on (default: this box's tailnet address only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *listen == "" {
		addr, err := defaultListen(interfaceIPs())
		if err != nil {
			return err
		}
		*listen = addr
	}
	id, err := b.identity()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	hostname = trust.NameFromHostname(hostname, "box")
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// pair reads this to advertise the address that actually answers.
	if err := statefile.Write(filepath.Join(b.dir, "listen"), []byte(ln.Addr().String())); err != nil {
		ln.Close()
		return err
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	s := &wire.Server{
		Identity: id,
		Clients:  b.clients(),
		Pending:  b.pending(),
		Name:     hostname,
		Log:      logger,
	}
	sessions, err := box.NewSessions(b.dir)
	if err != nil {
		ln.Close()
		return err
	}
	bus := &events.Bus{}
	shares := &box.Shares{OnStop: func(sh box.Share) {
		bus.Publish(events.Event{Type: "share.stopped", Box: hostname, Error: sh.Error, Data: map[string]any{"id": sh.ID, "port": sh.Port, "url": sh.URL}})
	}}
	// Nothing may stay public once the daemon managing it is gone.
	defer shares.StopAll()
	locations := box.NewLocations(filepath.Join(b.dir, "locations.json"))
	watcher := &box.Watcher{Locations: locations, Events: bus, Box: hostname}
	go watcher.Run(ctx)
	exe, err := os.Executable()
	if err != nil {
		ln.Close()
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	(&box.Box{
		Name:         hostname,
		Locations:    locations,
		Sessions:     sessions,
		Shares:       shares,
		Events:       bus,
		Watcher:      watcher,
		DaemonChecks: func() []doctor.Check { return daemonChecks(b, ln.Addr().String()) },
		LogDir:       filepath.Join(b.dir, "logs"),
		Kit:          kitInstaller(exe),
		Update: &box.SelfUpdate{
			Executable:    exe,
			Fingerprint:   id.Fingerprint().String(),
			BeforeRestart: func() { shares.StopAll(); ln.Close() },
		},
	}).Mount(s)

	os.Remove(b.socket())
	local, err := net.Listen("unix", b.socket())
	if err != nil {
		ln.Close()
		return err
	}
	defer os.Remove(b.socket())
	if err := os.Chmod(b.socket(), 0o600); err != nil {
		ln.Close()
		local.Close()
		return err
	}
	go s.ServeLocal(ctx, local)
	go (&hooks.Runner{Path: filepath.Join(b.dir, "hooks.json"), Log: logger}).Run(ctx, bus)

	logger.Printf("calportd serving %s as %q (%s); local API %s", ln.Addr(), hostname, id.Fingerprint().Short(), b.socket())
	return s.Serve(ctx, ln)
}

// runLocal runs a box command against this box's own daemon.
func runLocal(b boxHome, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := os.Stat(b.socket()); err != nil {
		return errors.New("calportd serve is not running on this box; start it with calportd install")
	}
	return boxcmd.Run(ctx, box.NewClient(box.NewLocal(b.socket())), args, os.Stdout)
}

// attachLocal replaces this process with a tmux client for the session, so
// attaching on the box itself needs no stream at all.
func attachLocal(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: calportd session attach NAME")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	return syscall.Exec(tmux, []string{"tmux", "-L", "calport", "attach-session", "-t", "=" + args[0]}, os.Environ())
}

func pair(b boxHome, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	address := fs.String("address", "", "address laptops should dial (default: best guess, port "+defaultPort+")")
	ttl := fs.Duration("ttl", defaultTTL, "how long the link stays valid")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ttl <= 0 || *ttl > time.Hour {
		return errors.New("--ttl must be between 0 and 1h")
	}
	target := *address
	if target == "" {
		hostname, _ := os.Hostname()
		listening, _ := os.ReadFile(filepath.Join(b.dir, "listen"))
		target = advertise(string(listening), interfaceIPs(), hostname)
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, defaultPort)
	}
	id, err := b.identity()
	if err != nil {
		return err
	}
	code, err := b.pending().Issue(*ttl, time.Now())
	if err != nil {
		return err
	}
	link := pairing.Token{Address: target, Fingerprint: id.Fingerprint(), Code: code}.String()
	fmt.Printf("Pairing link (single use, valid for %s):\n\n  %s\n\n", ttl, link)
	fmt.Printf("On your laptop:  calport pair '%s'\n\n", link)
	fmt.Println("Laptops will dial " + target + "; pass --address if that is not reachable.")
	fmt.Println("calportd serve must be running on this box to accept the pairing.")
	return nil
}

func listClients(b boxHome) error {
	peers, err := b.clients().List()
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		fmt.Println("No paired laptops. Run calportd pair to add one.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tFINGERPRINT\tPAIRED")
	for _, p := range peers {
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Fingerprint.Short(), p.PairedAt.Local().Format("2006-01-02 15:04"))
	}
	return w.Flush()
}
