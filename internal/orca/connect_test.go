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
	return box.Unit{Name: "calport-orca", State: "Running"}, f.err
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
