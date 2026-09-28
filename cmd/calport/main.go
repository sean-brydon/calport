// Command calport runs on a laptop: it pairs with boxes, runs the background
// agent, and manages forwards and URLs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/boxcmd"
	"github.com/sean-brydon/calport/internal/events"
	"github.com/sean-brydon/calport/internal/identity"
	"github.com/sean-brydon/calport/internal/integrations"
	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/statefile"
	"github.com/sean-brydon/calport/internal/trust"
	"github.com/sean-brydon/calport/internal/wire"
)

const usage = `calport — connect this laptop to development boxes

Boxes
  calport add ssh [user@]HOST [--name N] [--network NET]
                                           Install calportd on a box over SSH (once) and pair
  calport network login NAME               Join another tailnet (e.g. a personal one) to reach its boxes
  calport networks [--json]                List joined networks
  calport pair '<link>' [--name N] [--network NET]
                                           Pair with a box (link from calportd pair)
  calport boxes [--json]                   List paired boxes and whether they are online
  calport ping <box>                       Check a box answers and still trusts you
  calport upgrade <box>                    Upgrade the box's daemon over calport (no SSH)
  calport forget <box>                     Remove a box from this laptop

Reaching services
  calport url <box> <port|service>         Print the private URL for a service
  calport open <box> <port|service>        Open that URL in your browser
  calport forward <box> <ports> [--json]   Forward local ports: 3000, 8080:3000, 3000-3005
  calport forwards [--json]                List forwards
  calport unforward <id>                   Stop and forget a forward
  calport discover [--network NET]            Machines on the tailnet that could be boxes
  calport kit install BOX/LOCATION            Set up Cal.com worktrees on a box: own port, database and URL each
  calport kit check BOX/LOCATION              Make a throwaway worktree, check setup, URL and archive, end to end
  calport route add '*.x.localhost' BOX PORT  Send every matching host to a box port, Host unchanged
  calport routes [--json]                  List routes (calport route rm PATTERN removes one)

Sessions
  calport attach BOX/SESSION               Attach this terminal to an agent session (detach: Ctrl-b d)
  calport terminal BOX/SESSION             Open a new terminal window attached to a session
  calport emit TYPE [key=value...]         Announce an event on this laptop, e.g. agent.finished

Agent
  calport status [--json]                  Boxes, forwards and the proxy at a glance
  calport doctor [BOX] [--json]            Check this computer (or a box) and how to fix it
  calport events [--json]                  Stream events as they happen
  calport agent                            Run the agent in the foreground
  calport agent install|uninstall|status   Run the agent at login, restart it on crashes
  calport setup port80 [--remove]          Drop :1355 from URLs (asks for your admin password once)
  calport stop                             Stop the agent (and every forward)
  calport id                               Print this laptop's fingerprint

CALPORT_HOME overrides the state directory.
`

func main() {
	// ssh runs SSH_ASKPASS with the prompt as its only argument.
	if os.Getenv(askpassMarker) == "1" && len(os.Args) == 2 {
		if err := askpass(os.Args[1:]); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Print(usage)
		fmt.Println()
		fmt.Print(boxcmd.Usage("calport", "BOX/"))
		fmt.Println()
		fmt.Printf(integrations.Usage, "calport")
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "calport:", err)
		os.Exit(1)
	}
}

type laptop struct {
	dir string
}

func (l laptop) identity() (*identity.Identity, error) {
	return identity.LoadOrCreate(filepath.Join(l.dir, "identity.pem"))
}
func (l laptop) boxes() *trust.Store { return trust.NewStore(filepath.Join(l.dir, "boxes.json")) }
func (l laptop) socket() string      { return filepath.Join(l.dir, "agent.sock") }

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	home, err := statefile.Home()
	if err != nil {
		return err
	}
	l := laptop{dir: filepath.Join(home, "client")}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "pair":
		return pair(l, rest)
	case "setup":
		return setup(rest)
	case "doctor":
		return runDoctor(l, rest)
	case "route":
		return routeCommand(l, rest)
	case "kit":
		return kitCommand(l, rest)
	case "discover":
		return discover(l, rest)
	case "routes":
		return listRoutes(l, rest)
	case "network":
		return networkCommand(l, rest)
	case "upgrade":
		return upgrade(l, rest)
	case "networks":
		return listNetworks(l, rest)
	case "add":
		if len(rest) == 0 || rest[0] != "ssh" {
			return errors.New("usage: calport add ssh [user@]HOST [--name N] [--listen ADDR] [--address ADDR] [-- SSH OPTIONS]")
		}
		return addSSH(l, rest[1:])
	case "boxes":
		return listBoxes(l, rest)
	case "ping":
		return ping(l, rest)
	case "forget":
		return forget(l, rest)
	case "url", "open":
		return serviceURL(l, cmd == "open", rest)
	case "forward":
		return addForward(l, rest)
	case "forwards":
		return listForwards(l, rest)
	case "unforward":
		return removeForward(l, rest)
	case "status":
		return status(l, rest)
	case "events":
		return streamEvents(l, rest)
	case "agent":
		return agentCommand(l, rest)
	case "stop":
		c := agent.NewClient(l.socket())
		if !c.Running(context.Background()) {
			fmt.Println("The calport agent is not running.")
			return nil
		}
		if err := c.Stop(context.Background()); err != nil {
			return err
		}
		fmt.Println("Stopped the calport agent.")
		return nil
	case "id":
		id, err := l.identity()
		if err != nil {
			return err
		}
		fmt.Println(id.Fingerprint())
		return nil
	case "attach":
		return attach(l, rest)
	case "terminal":
		return openTerminal(rest)
	case "hook":
		integrations.Hook(rest, os.Stdin, os.Stdout, os.Stderr, func(e events.Event) error {
			c := agent.NewClient(l.socket())
			if !c.Running(context.Background()) {
				return nil
			}
			return c.Call(context.Background(), "POST", "/v1/events", map[string]any{"type": e.Type, "origin": e.Origin, "data": e.Data}, nil)
		})
		return nil
	case "integrations":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return integrations.Install(rest, exe, os.Stdout)
	case "emit":
		// An event for this laptop, unless it names a paired box first.
		if len(rest) > 0 {
			if _, ok, _ := l.boxes().ByName(rest[0]); ok {
				return runOnBox(l, args)
			}
		}
		return emitLocal(l, rest)
	}
	if _, ok := boxcmd.Commands[cmd]; ok {
		return runOnBox(l, args)
	}
	return fmt.Errorf("unknown command %q; run calport help", cmd)
}

// flags parses the common --json flag plus any command-specific ones. Flags
// may come before or after positional arguments.
func flags(name string, args []string, extra func(*flag.FlagSet)) (*flag.FlagSet, bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if extra != nil {
		extra(fs)
	}
	err := fs.Parse(flagsFirst(fs, args))
	return fs, *asJSON, err
}

// flagsFirst moves flags (with their values) ahead of positional arguments,
// because the flag package stops at the first positional one.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flagArgs = append(flagArgs, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !(ok && b.IsBoolFlag()) && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return append(append(flagArgs, "--"), positional...)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func pair(l laptop, args []string) error {
	var name, via *string
	fs, asJSON, err := flags("pair", args, func(fs *flag.FlagSet) {
		name = fs.String("name", "", "local name for the box (default: the name the box reports)")
		via = fs.String("network", "", "reach the box through this network (see calport networks)")
	})
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: calport pair '<link>' [--name NAME]")
	}
	tok, err := pairing.ParseToken(fs.Arg(0))
	if err != nil {
		return err
	}
	boxes := l.boxes()
	// Check the requested name before spending the single-use code on the box.
	if *name != "" {
		if err := checkName(*name); err != nil {
			return err
		}
		if existing, ok, err := boxes.ByName(*name); err != nil {
			return err
		} else if ok && existing.Fingerprint != tok.Fingerprint {
			return fmt.Errorf("a different box is already paired as %q; choose another --name", *name)
		}
	}
	id, err := l.identity()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	dial, err := networkDialer(l, *via)
	if err != nil {
		return err
	}
	reported, err := wire.PairVia(context.Background(), id, tok, trust.NameFromHostname(hostname, "laptop"), dial)
	if err != nil {
		return err
	}
	peer := trust.Peer{Address: tok.Address, Network: *via, Fingerprint: tok.Fingerprint, PairedAt: time.Now().UTC()}
	if *name != "" {
		peer.Name = *name
		err = boxes.Add(peer)
	} else {
		peer.Name = trust.NameFromHostname(reported, "box")
		peer.Name, err = boxes.AddWithFreeName(peer)
	}
	if err != nil {
		return fmt.Errorf("the box accepted this laptop, but saving it locally failed: %w; run calportd pair again", err)
	}
	// Let a running agent pick the box up now rather than at its next check.
	if c := agent.NewClient(l.socket()); c.Running(context.Background()) {
		c.Refresh(context.Background())
	}
	if asJSON {
		return printJSON(peer)
	}
	fmt.Printf("Paired with %s at %s (%s)\n", peer.Name, peer.Address, peer.Fingerprint.Short())
	return nil
}

func listBoxes(l laptop, args []string) error {
	_, asJSON, err := flags("boxes", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s.Boxes)
	}
	if len(s.Boxes) == 0 {
		fmt.Println("No paired boxes. On a box, run calportd pair, then calport pair '<link>' here.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tADDRESS\tLATENCY\tFINGERPRINT")
	for _, b := range s.Boxes {
		latency := "-"
		if b.LatencyMs > 0 {
			latency = strconv.FormatInt(b.LatencyMs, 10) + "ms"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.Name, b.State, b.Address, latency, b.Fingerprint[:12])
	}
	return w.Flush()
}

func ping(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: calport ping <box>")
	}
	client, err := l.boxClient(args[0])
	if err != nil {
		return err
	}
	defer client.Reset()
	box := client.Box()
	start := time.Now()
	if _, err := client.Ping(context.Background()); err != nil {
		return fmt.Errorf("%s: %w", box.Name, err)
	}
	fmt.Printf("%s: ok (%s)\n", box.Name, time.Since(start).Round(time.Millisecond))
	return nil
}

func forget(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: calport forget <box>")
	}
	p, err := l.boxes().Remove(args[0])
	if err != nil {
		return err
	}
	if c := agent.NewClient(l.socket()); c.Running(context.Background()) {
		c.Refresh(context.Background())
	}
	fmt.Printf("Forgot %s. On the box, calportd revoke removes this laptop's access too.\n", p.Name)
	return nil
}

func serviceURL(l laptop, open bool, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: calport url|open <box> <port|service>")
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if s.Proxy.Port == 0 {
		return fmt.Errorf("the local proxy is not running: %s", s.Proxy.Error)
	}
	url := serviceURLFor(args[1], args[0], s.Proxy.URLPort)
	fmt.Println(url)
	if !open {
		return nil
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, url).Run()
}

func addForward(l laptop, args []string) error {
	fs, asJSON, err := flags("forward", args, nil)
	if err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: calport forward <box> <ports>  (3000, 8080:3000, 3000-3005)")
	}
	maps, err := parsePorts(fs.Arg(1))
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	var added []agent.Forward
	for _, m := range maps {
		f, err := c.AddForward(context.Background(), fs.Arg(0), m.local, m.remote)
		if err != nil {
			return fmt.Errorf("localhost:%d: %w", m.local, err)
		}
		added = append(added, f)
		if !asJSON {
			fmt.Printf("%s  localhost:%d → %s:%d\n", f.ID, f.Local, f.Box, f.Remote)
		}
	}
	if asJSON {
		return printJSON(added)
	}
	return nil
}

func listForwards(l laptop, args []string) error {
	_, asJSON, err := flags("forwards", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s.Forwards)
	}
	if len(s.Forwards) == 0 {
		fmt.Println("No forwards. Add one with calport forward <box> <ports>.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLOCAL\tBOX\tREMOTE\tSTATE")
	for _, f := range s.Forwards {
		state := f.State
		if f.Error != "" {
			state += ": " + f.Error
		}
		fmt.Fprintf(w, "%s\tlocalhost:%d\t%s\t%d\t%s\n", f.ID, f.Local, f.Box, f.Remote, state)
	}
	return w.Flush()
}

func removeForward(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: calport unforward <id>")
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	f, err := c.RemoveForward(context.Background(), args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Stopped localhost:%d → %s:%d\n", f.Local, f.Box, f.Remote)
	return nil
}

func status(l laptop, args []string) error {
	_, asJSON, err := flags("status", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	s, err := c.Refresh(context.Background())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(s)
	}
	if s.Proxy.Error != "" {
		fmt.Printf("proxy: %s\n", s.Proxy.Error)
	} else {
		fmt.Printf("proxy: %s\n", serviceURLFor("<port>", "<box>", s.Proxy.URLPort))
	}
	fmt.Println()
	if err := listBoxes(l, nil); err != nil {
		return err
	}
	if len(s.Forwards) > 0 {
		fmt.Println()
		return listForwards(l, nil)
	}
	return nil
}

func streamEvents(l laptop, args []string) error {
	_, asJSON, err := flags("events", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	enc := json.NewEncoder(os.Stdout)
	return c.Events(ctx, func(e agent.Event) {
		if asJSON {
			enc.Encode(e)
			return
		}
		line := boxcmd.Describe(e)
		if local, ok := e.Data["local"]; ok {
			line += fmt.Sprintf("  localhost:%v → %v", local, e.Data["remote"])
		}
		fmt.Println(line)
	})
}

// emitLocal publishes an event on this laptop's agent, for tools running on
// the laptop (Cursor, a local Orca) to announce what they did.
func emitLocal(l laptop, args []string) error {
	if len(args) == 0 || !strings.Contains(args[0], ".") {
		return errors.New("usage: calport emit TYPE [key=value...]   (or calport emit BOX TYPE ... for a box)")
	}
	data := map[string]any{}
	origin := os.Getenv("CALPORT_ORIGIN")
	for _, kv := range args[1:] {
		if o, ok := strings.CutPrefix(kv, "--origin="); ok {
			origin = o
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}
		data[k] = v
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	return c.Call(context.Background(), "POST", "/v1/events", map[string]any{"type": args[0], "origin": origin, "data": data}, nil)
}

// agentIfRunning returns a client for the agent when one is running, without
// starting one.
func agentIfRunning(l laptop) *agent.Client {
	c := agent.NewClient(l.socket())
	if c.Running(context.Background()) {
		return c
	}
	return nil
}
