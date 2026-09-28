package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/box"
)

type fakeUnits struct {
	log []byte
	err error
}

func (f *fakeUnits) AddUnit(context.Context, box.UnitRequest) (box.Unit, error) {
	return box.Unit{Name: "calport-orca", State: "installed"}, f.err
}
func (f *fakeUnits) UnitLog(context.Context, string, int64) ([]byte, error) { return f.log, nil }

type fakeForwards struct {
	added    []agent.Forward
	removed  []string
	existing []agent.ForwardStatus
	next     int
}

func (f *fakeForwards) AddPinnedForward(_ context.Context, boxName string, local, remote int, pin string) (agent.Forward, error) {
	f.next++
	fwd := agent.Forward{ID: fmt.Sprintf("fwd-%d", f.next), Box: boxName, Local: local, Remote: remote, Pin: pin}
	f.added = append(f.added, fwd)
	return fwd, nil
}
func (f *fakeForwards) RemoveForward(_ context.Context, id string) (agent.Forward, error) {
	f.removed = append(f.removed, id)
	return agent.Forward{ID: id}, nil
}
func (f *fakeForwards) Forwards(context.Context) ([]agent.ForwardStatus, error) {
	return f.existing, nil
}

func readyLog(t *testing.T, port int, runtime string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": "orca_server_ready", "schemaVersion": 1, "runtimeId": runtime,
		"boundEndpoint":      "ws://127.0.0.1:41001",
		"advertisedEndpoint": fmt.Sprintf("ws://127.0.0.1:%d", port),
		"pairing": map[string]any{
			"available": true, "url": "https://orca.example/pair/SECRET-TOKEN",
			"endpoint": fmt.Sprintf("ws://127.0.0.1:%d", port),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func connector(t *testing.T, cliBody string, log []byte) (*Connector, *fakeForwards) {
	t.Helper()
	cli, _ := fakeOrca(t, cliBody)
	fwd := &fakeForwards{}
	return &Connector{
		Store:    Store{Path: filepath.Join(t.TempDir(), "orca.json")},
		CLI:      cli,
		Units:    &fakeUnits{log: log},
		Forwards: fwd,
	}, fwd
}

func TestConnectPairsAndSavesTheRoute(t *testing.T) {
	c, fwd := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[]}}'; exit 0;;
"environment add") echo '{"ok":true,"result":{"environment":{"id":"env-new"}}}'; exit 0;;
esac
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-1","reachable":true}}}'; exit 0;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))

	got, err := c.Connect(context.Background(), "devl")
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	if got.Environment != "env-new" || got.Runtime != "runtime-1" || got.LocalPort != portBase {
		t.Fatalf("Connect() = %+v; want env-new on runtime-1 at %d", got, portBase)
	}
	if len(fwd.added) != 1 || fwd.added[0].Pin != Pin("devl") {
		t.Fatalf("forwards added = %+v; want one pinned %q", fwd.added, Pin("devl"))
	}
	saved, err := c.Store.Read()
	if err != nil || saved["devl"] != got {
		t.Fatalf("saved route = %+v, %v; want %+v", saved["devl"], err, got)
	}
}

func TestConnectReplacesATunnelPointingAtAnOldRuntimePort(t *testing.T) {
	c, fwd := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[]}}'; exit 0;;
"environment add") echo '{"ok":true,"result":{"environment":{"id":"env-new"}}}'; exit 0;;
esac
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-1","reachable":true}}}'; exit 0;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))
	// A tunnel left over from a previous run, pointing at the port the runtime
	// used last time. The store rejects a second forward on the same local
	// port, so the stale one must be removed rather than worked around.
	fwd.existing = []agent.ForwardStatus{{Forward: agent.Forward{
		ID: "fwd-stale", Box: "devl", Local: portBase, Remote: 40999, Pin: Pin("devl"),
	}}}

	got, err := c.Connect(context.Background(), "devl")
	if err != nil {
		t.Fatalf("Connect() = %v; want the stale tunnel replaced", err)
	}
	if len(fwd.removed) != 1 || fwd.removed[0] != "fwd-stale" {
		t.Fatalf("forwards removed = %v; want the stale tunnel removed", fwd.removed)
	}
	if len(fwd.added) != 1 || fwd.added[0].Remote != got.RemotePort {
		t.Fatalf("forwards added = %+v; want one pointing at the runtime's current port %d", fwd.added, got.RemotePort)
	}
}

func TestConnectUndoesANewEnvironmentWhenVerifyFails(t *testing.T) {
	c, fwd := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[]}}'; exit 0;;
"environment add") echo '{"ok":true,"result":{"environment":{"id":"env-new"}}}'; exit 0;;
"environment rm") echo '{"ok":true,"result":{}}'; exit 0;;
esac
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-other","reachable":true}}}'; exit 0;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))

	if _, err := c.Connect(context.Background(), "devl"); err == nil {
		t.Fatal("Connect() succeeded with a mismatched runtime; want an error")
	}
	if len(fwd.removed) != 1 {
		t.Fatalf("forwards removed = %v; want the forward this call created to be gone", fwd.removed)
	}
	saved, _ := c.Store.Read()
	if r, ok := saved["devl"]; ok && r.Environment != "" {
		t.Fatalf("saved route = %+v; want no environment saved after a failure", r)
	}
	body, err := os.ReadFile(callsFile(t, c.CLI))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "environment rm") {
		t.Fatal("Connect did not remove the environment it created")
	}
}

func TestConnectKeepsAReusedEnvironmentWhenVerifyFails(t *testing.T) {
	c, _ := connector(t, fmt.Sprintf(`
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[{"id":"env-existing","name":"devl","endpoints":[{"endpoint":"ws://127.0.0.1:%d"}]}]}}'; exit 0;;
"environment rm") echo '{"ok":true,"result":{}}'; exit 0;;
esac
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-other","reachable":true}}}'; exit 0;;
esac
exit 1
`, portBase), readyLog(t, portBase, "runtime-1"))

	if _, err := c.Connect(context.Background(), "devl"); err == nil {
		t.Fatal("Connect() succeeded with a mismatched runtime; want an error")
	}
	body, err := os.ReadFile(callsFile(t, c.CLI))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "environment rm") {
		t.Fatal("Connect removed an environment it did not create")
	}
}

func TestConnectRefusesAnEnvironmentOnAnotherEndpoint(t *testing.T) {
	c, _ := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[{"id":"env-existing","name":"devl","endpoints":[{"endpoint":"ws://127.0.0.1:19999"}]}]}}'; exit 0;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))

	if _, err := c.Connect(context.Background(), "devl"); !errors.Is(err, ErrEnvironmentEndpointDiffers) {
		t.Fatalf("Connect() = %v; want ErrEnvironmentEndpointDiffers", err)
	}
}

func TestConnectErrorsNeverCarryThePairingURL(t *testing.T) {
	c, _ := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[]}}'; exit 0;;
"environment add") echo '{"ok":false,"error":{"message":"refused"}}'; exit 1;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))

	_, err := c.Connect(context.Background(), "devl")
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error leaks the pairing credential: %v", err)
	}
}

func TestConnectReportsAnOrphanedEnvironmentWhenCleanupFails(t *testing.T) {
	// The runtime mismatch (like TestConnectUndoesANewEnvironmentWhenVerifyFails)
	// triggers rollback, but this time "environment rm" itself fails - so the
	// environment calport just paired is stuck. The caller must be told, in
	// recoverable terms, rather than only seeing the original mismatch.
	c, _ := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[]}}'; exit 0;;
"environment add") echo '{"ok":true,"result":{"environment":{"id":"env-new"}}}'; exit 0;;
"environment rm") echo '{"ok":false,"error":{"message":"refused"}}'; exit 1;;
esac
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-other","reachable":true}}}'; exit 0;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))

	_, err := c.Connect(context.Background(), "devl")
	if err == nil {
		t.Fatal("Connect() succeeded with a mismatched runtime; want an error")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error leaks the pairing credential: %v", err)
	}
	if !strings.Contains(err.Error(), "devl") || !strings.Contains(err.Error(), "left paired") {
		t.Fatalf("Connect() = %v; want it to name the box and say an environment was left paired", err)
	}
	if !strings.Contains(err.Error(), "not the one calport paired with") {
		t.Fatalf("Connect() = %v; want the original verify failure to still be reported", err)
	}
}

func TestServeReportsAUnitThatWouldNotInstall(t *testing.T) {
	c, fwd := connector(t, "exit 1", readyLog(t, portBase, "runtime-1"))
	c.Units = &fakeUnits{log: readyLog(t, portBase, "runtime-1"), err: errors.New("the box refused the unit")}

	if _, err := c.Serve(context.Background(), "devl"); err == nil || !strings.Contains(err.Error(), "refused the unit") {
		t.Fatalf("Serve() = %v; want the box's install failure surfaced", err)
	}
	// Connect must not tunnel to a runtime that was never started: a forward
	// created here would outlive the failure with nothing on the far end.
	if _, err := c.Connect(context.Background(), "devl"); err == nil {
		t.Fatal("Connect() succeeded although the unit would not install")
	}
	if len(fwd.added) != 0 {
		t.Fatalf("forwards added = %+v; want none when the unit never started", fwd.added)
	}
}

func TestDisconnectDropsTheRouteAndTheTunnel(t *testing.T) {
	c, fwd := connector(t, `
case "$1 $2" in
"environment list") echo '{"ok":true,"result":{"environments":[]}}'; exit 0;;
"environment add") echo '{"ok":true,"result":{"environment":{"id":"env-new"}}}'; exit 0;;
esac
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-1","reachable":true}}}'; exit 0;;
esac
exit 1
`, readyLog(t, portBase, "runtime-1"))

	route, err := c.Connect(context.Background(), "devl")
	if err != nil {
		t.Fatal(err)
	}
	fwd.existing = []agent.ForwardStatus{{Forward: fwd.added[0]}}

	got, err := c.Disconnect(context.Background(), "devl")
	if err != nil {
		t.Fatalf("Disconnect() = %v", err)
	}
	if !got.HadRoute || got.Route != route {
		t.Fatalf("Disconnect() = %+v; want the route it dropped, %+v", got, route)
	}
	if !got.RemovedForward || len(fwd.removed) != 1 || fwd.removed[0] != fwd.added[0].ID {
		t.Fatalf("forwards removed = %v; want the pinned tunnel gone", fwd.removed)
	}
	saved, err := c.Store.Read()
	if err != nil || len(saved) != 0 {
		t.Fatalf("saved routes = %+v, %v; want the box forgotten, pinned port included", saved, err)
	}
}

func TestDisconnectOnAnUnpairedBoxSaysSo(t *testing.T) {
	c, fwd := connector(t, "exit 1", nil)
	got, err := c.Disconnect(context.Background(), "devl")
	if err != nil {
		t.Fatalf("Disconnect() = %v; want a quiet no-op", err)
	}
	if got.HadRoute || got.RemovedForward || len(fwd.removed) != 0 {
		t.Fatalf("Disconnect() = %+v, removed %v; want nothing dropped", got, fwd.removed)
	}
}

// A port is pinned on the first attempt, before anything pairs. If that port
// is already taken on this laptop the box is stuck on it, so disconnect must
// free it for a different one.
func TestDisconnectFreesThePinnedPortForReallocation(t *testing.T) {
	c, _ := connector(t, "exit 1", nil)
	first, err := c.Store.PortFor("devl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Disconnect(context.Background(), "devl"); err != nil {
		t.Fatal(err)
	}
	// The freed port goes to whoever asks next, and devl is pinned again from
	// scratch - which is the point: a port it could not listen on is not the
	// port it gets back.
	taken, err := c.Store.PortFor("omarchy")
	if err != nil || taken != first {
		t.Fatalf("PortFor(omarchy) = %d, %v; want the freed port %d", taken, err, first)
	}
	again, err := c.Store.PortFor("devl")
	if err != nil || again == first {
		t.Fatalf("PortFor(devl) = %d, %v; want a port other than the one it was pinned to", again, err)
	}
}
