package orca

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/box"
)

// unitName is the managed unit the runtime runs as on every box.
const unitName = "calport-orca"

// readyWait bounds how long a runtime has to report that it is ready.
const readyWait = 60 * time.Second

// readLimit is how much of the runtime's log is read. Records are appended, so
// the end is what matters.
const readLimit = 1 << 20

// cleanupTimeout bounds rollback. It runs on a context detached from the
// caller's, because the caller's ctx being done is often exactly why rollback
// is running — reusing it would make cleanup fail instantly and silently.
const cleanupTimeout = 15 * time.Second

// Pin is the forward key for a box's runtime tunnel.
func Pin(boxName string) string { return "orca/" + boxName }

// UnitClient is the part of a box client this package needs.
type UnitClient interface {
	AddUnit(ctx context.Context, req box.UnitRequest) (box.Unit, error)
	UnitLog(ctx context.Context, name string, limit int64) ([]byte, error)
}

// ForwardClient is the part of the agent client this package needs.
type ForwardClient interface {
	AddPinnedForward(ctx context.Context, boxName string, local, remote int, pin string) (agent.Forward, error)
	RemoveForward(ctx context.Context, id string) (agent.Forward, error)
	Forwards(ctx context.Context) ([]agent.ForwardStatus, error)
}

// Connector serves a box's Orca runtime and pairs this laptop's Orca app with
// it.
type Connector struct {
	Store    Store
	CLI      CLI
	Units    UnitClient
	Forwards ForwardClient
}

// Serve installs and starts the runtime unit, then waits for its ready
// record. The record carries a credential, so it is returned to the caller and
// never logged.
func (c *Connector) Serve(ctx context.Context, boxName string) (Ready, error) {
	port, err := c.Store.PortFor(boxName)
	if err != nil {
		return Ready{}, err
	}
	// --pairing-address sets only the client-advertised address, which is what
	// the pairing code embeds. No --port: the runtime picks its own and reports
	// it as boundEndpoint, which is authoritative. If something already holds
	// that port the unit fails to start, which is the right outcome.
	if _, err := c.Units.AddUnit(ctx, box.UnitRequest{
		Name:    unitName,
		Program: "orca",
		Args:    []string{"serve", "--json", "--pairing-address", fmt.Sprintf("ws://127.0.0.1:%d", port)},
	}); err != nil {
		return Ready{}, err
	}
	deadline := time.Now().Add(readyWait)
	var last error
	for {
		log, err := c.Units.UnitLog(ctx, unitName, readLimit)
		if err == nil {
			ready, perr := ParseReady(log, port)
			if perr == nil {
				return ready, nil
			}
			last = perr
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			return Ready{}, fmt.Errorf("the runtime on %s did not report that it is ready within %v: %w; see: calport unit get %s/%s", boxName, readyWait, last, boxName, unitName)
		}
		select {
		case <-ctx.Done():
			return Ready{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Connect serves the runtime, tunnels it, pairs a local environment with it,
// verifies the runtime is the one paired with, and saves the route. A failure
// after something was created undoes only what this call created: an
// environment that already existed is never removed.
//
// The original failure is often the caller's ctx being cancelled or timing
// out, which is exactly when rollback must still run. So rollback uses its
// own bounded context detached from ctx, and any rollback failure is folded
// into the returned error rather than swallowed: silence here would leave a
// paired environment - holding a pairing credential - orphaned with nobody
// told.
func (c *Connector) Connect(ctx context.Context, boxName string) (route Route, err error) {
	ready, err := c.Serve(ctx, boxName)
	if err != nil {
		return Route{}, err
	}
	route = Route{Runtime: ready.RuntimeID, LocalPort: ready.LocalPort, RemotePort: ready.RemotePort}

	fwdID, createdForward, err := c.tunnel(ctx, boxName, route)
	if err != nil {
		return Route{}, err
	}
	createdEnvironment := ""
	done := false
	defer func() {
		if done {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		var cleanupErrs []error
		if createdEnvironment != "" {
			if rmErr := c.CLI.RemoveEnvironment(cleanupCtx, createdEnvironment); rmErr != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("an Orca environment for %s was left paired and could not be removed automatically; remove it manually: %w", boxName, rmErr))
			}
		}
		if createdForward {
			if _, rmErr := c.Forwards.RemoveForward(cleanupCtx, fwdID); rmErr != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("a tunnel forward for %s was left behind and could not be removed automatically: %w", boxName, rmErr))
			}
		}
		if len(cleanupErrs) > 0 {
			err = errors.Join(append([]error{err}, cleanupErrs...)...)
		}
	}()

	route.Environment, createdEnvironment, err = c.environment(ctx, boxName, ready)
	if err != nil {
		return Route{}, err
	}
	if err := c.CLI.Verify(ctx, route.Environment, route.Runtime); err != nil {
		return Route{}, err
	}
	routes, err := c.Store.Read()
	if err != nil {
		return Route{}, err
	}
	routes[boxName] = route
	if err := c.Store.Save(routes); err != nil {
		return Route{}, err
	}
	done = true
	return route, nil
}

// tunnel reuses a forward that already points where this route needs, so
// connecting twice does not stack forwards.
func (c *Connector) tunnel(ctx context.Context, boxName string, route Route) (id string, created bool, err error) {
	existing, err := c.Forwards.Forwards(ctx)
	if err != nil {
		return "", false, err
	}
	for _, f := range existing {
		if f.Pin != Pin(boxName) {
			continue
		}
		if f.Local == route.LocalPort && f.Remote == route.RemotePort {
			return f.ID, false, nil
		}
		// The runtime came back on a different bound port. The local port is
		// fixed by the pairing code, so the stale mapping has to go: the store
		// rejects a second forward on a local port already in use.
		if _, err := c.Forwards.RemoveForward(ctx, f.ID); err != nil {
			return "", false, err
		}
	}
	f, err := c.Forwards.AddPinnedForward(ctx, boxName, route.LocalPort, route.RemotePort, Pin(boxName))
	if err != nil {
		return "", false, err
	}
	return f.ID, true, nil
}

// environment finds the environment for this box or pairs a new one. It
// returns the id, and the id again in created when this call paired it, so a
// failure afterwards removes only what it made.
func (c *Connector) environment(ctx context.Context, boxName string, ready Ready) (id, created string, err error) {
	all, err := c.CLI.Environments(ctx)
	if err != nil {
		return "", "", err
	}
	for _, env := range all {
		if env.Name != boxName {
			continue
		}
		for _, ep := range env.Endpoints {
			if ep == ready.Advertised {
				return env.ID, "", nil
			}
		}
		return "", "", fmt.Errorf("%w: %s", ErrEnvironmentEndpointDiffers, boxName)
	}
	id, err = c.CLI.AddEnvironment(ctx, boxName, ready.PairingURL)
	if err != nil {
		return "", "", err
	}
	return id, id, nil
}
