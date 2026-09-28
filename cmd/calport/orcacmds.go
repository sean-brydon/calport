package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/orca"
)

const orcaUsage = `Usage:
  calport orca serve BOX          Install and start the runtime unit on the box
  calport orca connect BOX        Serve if needed, then tunnel it and pair this computer
  calport orca status BOX         Restore the tunnel if it is gone, verify, and report
  calport orca exec BOX -- ARGS   Run an orca command against that box's runtime

connect is the whole flow; serve exists for running a runtime without pairing
to it from here. Orca stores the pairing credential; calport stores only the
route and the runtime's identity.
`

func orcaCommand(l laptop, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(orcaUsage)
		return nil
	}
	if len(args) < 2 {
		return errors.New("name an action and a box; run calport orca help")
	}
	action, boxName, rest := args[0], args[1], args[2:]
	ctx, cancel := signalContext()
	defer cancel()

	conn, err := newConnector(l, boxName)
	if err != nil {
		return err
	}
	switch action {
	case "serve":
		if _, err := conn.Serve(ctx, boxName); err != nil {
			return err
		}
		fmt.Printf("The Orca runtime is serving on %s.\n", boxName)
		return nil
	case "connect":
		route, err := conn.Connect(ctx, boxName)
		if err != nil {
			return err
		}
		fmt.Printf("Orca on this computer now reaches %s at ws://127.0.0.1:%d.\n", boxName, route.LocalPort)
		return nil
	case "status":
		route, err := conn.Connect(ctx, boxName)
		if err != nil {
			return err
		}
		fmt.Printf("%s: runtime reachable at ws://127.0.0.1:%d\n", boxName, route.LocalPort)
		return nil
	case "exec":
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return errors.New("name the orca command to run after --")
		}
		routes, err := conn.Store.Read()
		if err != nil {
			return err
		}
		route, ok := routes[boxName]
		if !ok || route.Environment == "" {
			return fmt.Errorf("%s is not paired with this computer's Orca; run: calport orca connect %s", boxName, boxName)
		}
		if err := conn.CLI.Verify(ctx, route.Environment, route.Runtime); err != nil {
			return err
		}
		out, err := conn.CLI.Exec(ctx, route.Environment, rest)
		os.Stdout.Write(out)
		return err
	}
	return fmt.Errorf("unknown orca action %q; run calport orca help", action)
}

// newConnector wires the laptop's agent and the box's daemon into a Connector.
// CLI is left zero so it finds the Orca CLI itself; only tests set it.
func newConnector(l laptop, boxName string) (*orca.Connector, error) {
	a, err := ensureAgent(l)
	if err != nil {
		return nil, err
	}
	wc, err := l.boxClient(boxName)
	if err != nil {
		return nil, err
	}
	return &orca.Connector{
		Store:    orca.Store{Path: filepath.Join(l.dir, "orca.json")},
		CLI:      orca.CLI{},
		Units:    box.NewClient(wc),
		Forwards: a,
	}, nil
}

var _ orca.UnitClient = (*box.Client)(nil)
