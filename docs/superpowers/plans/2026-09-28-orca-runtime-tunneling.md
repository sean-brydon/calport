# Orca Runtime Tunneling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a box's Orca runtime reachable from the laptop, so the local Orca app lists and drives environments that live on a box.

**Architecture:** A generic layer installs and supervises a managed user unit on a box (`internal/box/units.go`, routes under `/v1/units`) and pins a stable local port for a saved forward (`Forward.Pin`). A thin adapter (`internal/orca`) adds Orca's protocol: validate the runtime's ready record, pair a local environment against its pairing code, verify runtime identity, persist the route. Orca adds no box route — it drives `/v1/units`.

**Tech Stack:** Go 1.27, stdlib only. `internal/service` (launchd/systemd rendering), `internal/wire` (authenticated box transport), `internal/statefile` (atomic private state), `net/url` for endpoint validation.

**Spec:** `docs/superpowers/specs/2026-09-28-orca-runtime-tunneling-design.md`

## Global Constraints

- Go 1.27.0 as declared in `go.mod`. Stdlib only — no new dependencies.
- The pairing URL is a credential. It must never reach a log, an error message, an event, `calport doctor`, or `--json` output. Errors name which check failed, never the offending value.
- Failure reporting never tails the journal: `orca serve` writes its pairing URL into its own output. Report unit state and exit status only.
- `advertisedEndpoint` must be `ws`, loopback host, no userinfo, no query, no fragment, path empty or `/`, and its port must equal the pinned local port.
- `boundEndpoint` must be `ws` with a valid port. Its **host is deliberately unconstrained**: `orca serve` binds `0.0.0.0` (verified on devl), so requiring loopback would reject every real record.
- The runtime is started as `orca serve --json --pairing-address ws://127.0.0.1:<pinned>`, with no `--port`; the bound port comes back in the record.
- `pairing.endpoint` must equal `advertisedEndpoint` exactly.
- Port allocation base is `16768`, persisted on first connect and never recomputed.
- Ready-record read is bounded to the last 1 MiB (1048576 bytes).
- Ready-record wait is bounded to 60 seconds.
- `forwards.json` must stay backward-compatible: existing entries have no `pin` key and must still load.
- Test fakes follow `internal/box/providers_test.go`: a shell script on `PATH` via `t.Setenv`, never a mock framework.

---

### Task 1: Tool lookup finds Homebrew binaries

`internal/doctor.Tool` resolves via `exec.LookPath` plus `~/.local/bin`. macOS GUI apps do not inherit a shell `PATH`, so a Homebrew-installed `orca` reads as absent when checks run from Calport.app. `internal/orca` depends on finding the local `orca` CLI, so this lands first.

**Files:**
- Modify: `internal/doctor/doctor.go:36-49`
- Test: `internal/doctor/doctor_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `doctor.Tool(name string) (string, bool)` — unchanged signature, now also searching `/opt/homebrew/bin` and `/usr/local/bin`.

- [ ] **Step 1: Write the failing test**

Append to `internal/doctor/doctor_test.go`:

```go
func TestToolFindsBinariesOutsidePath(t *testing.T) {
	brew := t.TempDir()
	if err := os.WriteFile(filepath.Join(brew, "faketool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDirs := extraToolDirs
	extraToolDirs = []string{brew}
	t.Cleanup(func() { extraToolDirs = oldDirs })
	t.Setenv("PATH", t.TempDir())

	path, ok := Tool("faketool")
	if !ok || path != filepath.Join(brew, "faketool") {
		t.Fatalf("Tool(faketool) = %q, %v; want the copy in the extra directory", path, ok)
	}
}

func TestToolReportsMissingToolAsAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	oldDirs := extraToolDirs
	extraToolDirs = []string{t.TempDir()}
	t.Cleanup(func() { extraToolDirs = oldDirs })

	if path, ok := Tool("definitely-not-installed"); ok {
		t.Fatalf("Tool = %q, true; want absent", path)
	}
}
```

Ensure the file imports `os` and `path/filepath`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/doctor/ -run TestToolFinds -v`
Expected: FAIL — `undefined: extraToolDirs`

- [ ] **Step 3: Write minimal implementation**

In `internal/doctor/doctor.go`, replace the `Tool` function and add the variable above it:

```go
// extraToolDirs are searched after PATH. macOS GUI apps inherit a minimal
// PATH, so a Homebrew install is invisible to a check that trusts PATH alone.
// A variable so tests can redirect it.
var extraToolDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// Tool finds an executable on PATH, in ~/.local/bin, or in a Homebrew
// prefix, where Orca, Herdr, cloudflared, and agent CLIs install themselves.
func Tool(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	dirs := extraToolDirs
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, ".local", "bin")}, dirs...)
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/doctor/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/doctor/doctor.go internal/doctor/doctor_test.go
git commit -m "fix(doctor): find tools installed under a Homebrew prefix"
```

---

### Task 2: Linux units honour LogPath

`service.Render` writes `StandardOutPath` only on macOS. The `case "linux"` branch ignores `LogPath`, so a unit's output goes to the journal. The Orca runtime's ready record must land in a file calportd owns, never the journal, because it contains a pairing credential.

**Files:**
- Modify: `internal/service/service.go:97-108` (the `case "linux"` branch of `Render`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `service.Render(Spec)` — a Linux unit with `LogPath` set now contains `StandardOutput=append:<path>` and `StandardError=append:<path>`.

- [ ] **Step 1: Write the failing test**

Append to `internal/service/service_test.go`:

```go
func TestLinuxUnitsWriteOutputToTheLogPath(t *testing.T) {
	stub(t, "linux")
	unit, err := Render(Spec{
		Name:        "calport-orca",
		Description: "Orca runtime",
		Program:     "/usr/bin/orca",
		Args:        []string{"serve"},
		LogPath:     "/home/alex/.config/calport/units/calport-orca.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"StandardOutput=append:/home/alex/.config/calport/units/calport-orca.log",
		"StandardError=append:/home/alex/.config/calport/units/calport-orca.log",
	} {
		if !strings.Contains(string(unit), want) {
			t.Fatalf("unit is missing %q:\n%s", want, unit)
		}
	}
}

func TestLinuxUnitsWithoutALogPathRedirectNothing(t *testing.T) {
	stub(t, "linux")
	unit, err := Render(Spec{Name: "calport-agent", Program: "/usr/bin/calport", Args: []string{"agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "StandardOutput=") {
		t.Fatalf("unit redirects output with no LogPath set:\n%s", unit)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestLinuxUnits -v`
Expected: FAIL — `unit is missing "StandardOutput=append:..."`

- [ ] **Step 3: Write minimal implementation**

In `internal/service/service.go`, inside `case "linux":`, immediately after the `Environment=` loop and before `b.WriteString("Restart=on-failure\nRestartSec=5\n")`:

```go
		// systemd 240+. Keeping a unit's output in a file calportd owns keeps
		// credentials a program prints out of the journal, which is readable
		// by the box user and persists.
		if s.LogPath != "" {
			fmt.Fprintf(&b, "StandardOutput=append:%s\nStandardError=append:%s\n", s.LogPath, s.LogPath)
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): honour LogPath on Linux units"
```

---

### Task 3: Forwards can be pinned

A forward is identified by a random ID today, so nothing can find "the Orca tunnel for devl" across restarts. Add an optional stable key. `ForwardStatus` embeds `Forward`, so the field appears in agent status with no further change.

**Files:**
- Modify: `internal/agent/forwards.go:16-21` (the `Forward` type)
- Modify: `internal/agent/agent.go:569` (`addForward`)
- Modify: `internal/agent/api.go:31-48` (the `POST /v1/forwards` handler)
- Modify: `internal/agent/client.go:55-59` (`AddForward`)
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `agent.Forward` gains `Pin string` with JSON tag `pin,omitempty`.
  - `(*agent.Client).AddPinnedForward(ctx context.Context, box string, local, remote int, pin string) (Forward, error)`
  - `(*agent.Client).AddForward(ctx context.Context, box string, local, remote int) (Forward, error)` — unchanged signature, delegates with an empty pin.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/agent_test.go`:

```go
func TestPinnedForwardsRoundTripThroughTheStore(t *testing.T) {
	dir := t.TempDir()
	s := forwardStore{path: filepath.Join(dir, "forwards.json")}
	if _, err := s.add(Forward{Box: "devl", Local: 16769, Remote: 41001, Pin: "orca/devl"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Pin != "orca/devl" {
		t.Fatalf("list() = %+v; want one forward pinned orca/devl", all)
	}
}

func TestForwardsSavedBeforePinsStillLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forwards.json")
	old := `[{"id":"abcd1234","box":"devl","local":3000,"remote":3000}]`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	all, err := forwardStore{path: path}.list()
	if err != nil {
		t.Fatalf("list() on a file written before pins existed: %v", err)
	}
	if len(all) != 1 || all[0].Pin != "" || all[0].Local != 3000 {
		t.Fatalf("list() = %+v; want the existing forward with an empty pin", all)
	}
}
```

Ensure the file imports `os` and `path/filepath`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestPinnedForwards -v`
Expected: FAIL — `unknown field Pin in struct literal`

- [ ] **Step 3: Add the field**

In `internal/agent/forwards.go`, replace the `Forward` type:

```go
// Forward keeps a local port on the laptop pointed at a port on a box. Pin is
// a stable key for a forward something else needs to find again after a
// restart, such as a tool's runtime tunnel; it is empty for ordinary forwards.
type Forward struct {
	ID     string `json:"id"`
	Box    string `json:"box"`
	Local  int    `json:"local"`
	Remote int    `json:"remote"`
	Pin    string `json:"pin,omitempty"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -run "TestPinnedForwards|TestForwardsSaved" -v`
Expected: PASS

- [ ] **Step 5: Thread the pin through the agent and API**

In `internal/agent/agent.go`, change `addForward`'s signature and the `Forward` it stores:

```go
func (a *Agent) addForward(ctx context.Context, box string, local, remote int, pin string) (Forward, error) {
	f, err := a.forwards.add(Forward{Box: box, Local: local, Remote: remote, Pin: pin})
```

Leave the rest of the function body unchanged. In `internal/agent/api.go`, add `Pin` to the request struct and pass it:

```go
		var req struct {
			Box    string `json:"box"`
			Local  int    `json:"local"`
			Remote int    `json:"remote"`
			Pin    string `json:"pin"`
		}
```

```go
		f, err := a.addForward(a.runCtx(), req.Box, req.Local, req.Remote, req.Pin)
```

In `internal/agent/client.go`, replace `AddForward` with a delegating pair:

```go
func (c *Client) AddForward(ctx context.Context, box string, local, remote int) (Forward, error) {
	return c.AddPinnedForward(ctx, box, local, remote, "")
}

// AddPinnedForward saves a forward under a stable key, so a later call can
// find it again instead of remembering a random id.
func (c *Client) AddPinnedForward(ctx context.Context, box string, local, remote int, pin string) (Forward, error) {
	var f Forward
	req := map[string]any{"box": box, "local": local, "remote": remote, "pin": pin}
	return f, c.call(ctx, http.MethodPost, "/v1/forwards", req, &f)
}
```

- [ ] **Step 6: Fix remaining callers and run the package tests**

Run: `go build ./... && go test ./internal/agent/ -v`
Expected: PASS. Any other caller of `addForward` must pass `""` as the new argument.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/
git commit -m "feat(agent): let a forward carry a stable pin"
```

---

### Task 4: Managed units on a box

The generic half: install, start, report and remove a user unit on a box, and read the last ready record a unit wrote. Sits beside `sessions.go` and `shares.go` as a peer capability.

**Files:**
- Create: `internal/box/units.go`
- Test: `internal/box/units_test.go`

**Interfaces:**
- Consumes: `service.Spec`, `service.Install`, `service.Start`, `service.Uninstall`, `service.Installed` from Task 2.
- Produces:
  - `box.Unit` — `{Name, State, LogPath, Error string}` with JSON tags `name`, `state`, `log_path`, `error,omitempty`. `State` is `installed` or `removed` only; there is deliberately no liveness field (see the note in `Get`).
  - `box.UnitRequest` — `{Name, Program string; Args []string; Env map[string]string}` with JSON tags `name`, `program`, `args`, `env`.
  - `box.Units` — `{Dir string}`, with methods:
    - `(*Units) Install(ctx context.Context, req UnitRequest) (Unit, error)`
    - `(*Units) List() ([]Unit, error)`
    - `(*Units) Get(name string) (Unit, error)`
    - `(*Units) Remove(name string) (Unit, error)`
    - `(*Units) Tail(name string, limit int64) ([]byte, error)`
  - `box.ErrUnknownUnit` — returned by `Get`, `Remove` and `Tail` for a name with no installed unit.

- [ ] **Step 1: Write the failing test**

Create `internal/box/units_test.go`:

```go
package box

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean-brydon/calport/internal/service"
)

// fakeService records what would have been installed. Tests never call the
// real service manager: service.Install writes a launchd plist or systemd unit
// and loads it, which would leave a running unit on the machine under test.
func fakeService() (*serviceOps, *[]string) {
	installed := map[string]bool{}
	calls := &[]string{}
	return &serviceOps{
		install: func(s service.Spec) (string, error) {
			installed[s.Name] = true
			*calls = append(*calls, "install "+s.Name+" "+s.Program)
			return "/fake/" + s.Name, nil
		},
		start: func(s service.Spec) error {
			*calls = append(*calls, "start "+s.Name)
			return nil
		},
		uninstall: func(s service.Spec) (string, error) {
			delete(installed, s.Name)
			*calls = append(*calls, "uninstall "+s.Name)
			return "/fake/" + s.Name, nil
		},
		installed: func(s service.Spec) bool { return installed[s.Name] },
	}, calls
}

func TestInstallStartsAUnitAndReportsIt(t *testing.T) {
	svc, calls := fakeService()
	u := &Units{Dir: t.TempDir(), svc: svc}
	got, err := u.Install(context.Background(), UnitRequest{
		Name:    "calport-probe",
		Program: "/bin/sh",
		Args:    []string{"-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "calport-probe" {
		t.Fatalf("Install().Name = %q; want calport-probe", got.Name)
	}
	if want := filepath.Join(u.Dir, "calport-probe.log"); got.LogPath != want {
		t.Fatalf("Install().LogPath = %q; want %q", got.LogPath, want)
	}
	if got.State != "installed" {
		t.Fatalf("Install().State = %q; want installed", got.State)
	}
	all, err := u.List()
	if err != nil || len(all) != 1 || all[0].Name != "calport-probe" {
		t.Fatalf("List() = %+v, %v; want the installed unit", all, err)
	}
	if len(*calls) != 2 || !strings.HasPrefix((*calls)[0], "install calport-probe") || (*calls)[1] != "start calport-probe" {
		t.Fatalf("service calls = %v; want one install then one start", *calls)
	}
}

func TestUnitNamesMayNotEscapeTheUnitDirectory(t *testing.T) {
	svc, _ := fakeService()
	u := &Units{Dir: t.TempDir(), svc: svc}
	for _, name := range []string{"../escape", "has/slash", "", "has space", strings.Repeat("n", 129)} {
		if _, err := u.Install(context.Background(), UnitRequest{Name: name, Program: "/bin/sh"}); err == nil {
			t.Fatalf("Install(%q) was allowed; want a rejected name", name)
		}
	}
}

func TestGetReportsAnUnknownUnit(t *testing.T) {
	svc, _ := fakeService()
	u := &Units{Dir: t.TempDir(), svc: svc}
	if _, err := u.Get("calport-missing"); !errors.Is(err, ErrUnknownUnit) {
		t.Fatalf("Get() error = %v; want ErrUnknownUnit", err)
	}
}

func TestInstallRefusesAProgramTheBoxDoesNotHave(t *testing.T) {
	svc, _ := fakeService()
	u := &Units{Dir: t.TempDir(), svc: svc}
	t.Setenv("PATH", t.TempDir())
	_, err := u.Install(context.Background(), UnitRequest{Name: "calport-probe", Program: "definitely-not-installed"})
	if err == nil {
		t.Fatal("Install() accepted a program that is not on the box; want an error naming it")
	}
	if !strings.Contains(err.Error(), "definitely-not-installed") {
		t.Fatalf("Install() error = %v; want it to name the missing program", err)
	}
}

func TestInstallResolvesTheProgramToAnAbsolutePath(t *testing.T) {
	bin := t.TempDir()
	prog := filepath.Join(bin, "faketool")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	svc, _ := fakeService()
	u := &Units{Dir: t.TempDir(), svc: svc}
	spec, err := u.spec(UnitRequest{Name: "calport-probe", Program: "faketool"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Program != prog {
		t.Fatalf("spec.Program = %q; want the absolute path %q, which systemd requires in ExecStart", spec.Program, prog)
	}
}

func TestTailReturnsTheEndOfALargeLog(t *testing.T) {
	dir := t.TempDir()
	svc, _ := fakeService()
	u := &Units{Dir: dir, svc: svc}
	body := strings.Repeat("x", 4096) + "\nLAST RECORD\n"
	if err := os.WriteFile(filepath.Join(dir, "calport-probe.log"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := u.Tail("calport-probe", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 32 || !strings.Contains(string(out), "LAST RECORD") {
		t.Fatalf("Tail() = %q; want the last 32 bytes, which hold the last record", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/box/ -run TestInstallStarts -v`
Expected: FAIL — `undefined: Units`

- [ ] **Step 3: Write the implementation**

Create `internal/box/units.go`:

```go
package box

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/sean-brydon/calport/internal/doctor"
	"github.com/sean-brydon/calport/internal/service"
)

// Unit is a long-lived program calportd runs on the box under the platform's
// service manager, so it survives reboots and restarts on failure. Its output
// goes to a file calportd owns rather than the journal, because a program's
// output can contain credentials.
type Unit struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	LogPath string `json:"log_path"`
	Error   string `json:"error,omitempty"`
}

type UnitRequest struct {
	Name    string            `json:"name"`
	Program string            `json:"program"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

var (
	ErrUnknownUnit = errors.New("no unit with that name")
	// A unit name becomes a file name and a service-manager label, so it is
	// restricted rather than escaped.
	unitName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
)

// serviceOps is the platform service manager, indirected so a test never
// installs a real launchd agent or systemd unit on the machine running it.
type serviceOps struct {
	install   func(service.Spec) (string, error)
	start     func(service.Spec) error
	uninstall func(service.Spec) (string, error)
	installed func(service.Spec) bool
}

// Units installs and reports calportd's managed units. Dir holds one log per
// unit, named after it.
type Units struct {
	Dir string
	// svc is nil in production, where the real service manager is used.
	svc *serviceOps
}

func (u *Units) ops() serviceOps {
	if u.svc != nil {
		return *u.svc
	}
	return serviceOps{
		install:   service.Install,
		start:     service.Start,
		uninstall: service.Uninstall,
		installed: service.Installed,
	}
}

func (u *Units) spec(req UnitRequest) (service.Spec, error) {
	if !unitName.MatchString(req.Name) {
		return service.Spec{}, badRequest("unit name %q must be lowercase letters, digits and dashes", req.Name)
	}
	if req.Program == "" {
		return service.Spec{}, badRequest("a unit needs a program to run")
	}
	// systemd requires an absolute ExecStart, and resolving here turns "the
	// program is not installed on this box" into one clear error instead of a
	// unit that installs and then fails to execute.
	program := req.Program
	if !filepath.IsAbs(program) {
		path, ok := doctor.Tool(program)
		if !ok {
			return service.Spec{}, badRequest("%s is not installed on this box", program)
		}
		program = path
	}
	return service.Spec{
		Name:        req.Name,
		Description: "calport managed unit " + req.Name,
		Program:     program,
		Args:        req.Args,
		Env:         req.Env,
		LogPath:     u.logPath(req.Name),
	}, nil
}

func (u *Units) logPath(name string) string { return filepath.Join(u.Dir, name+".log") }

// Install writes the unit, starts it, and reports it. Installing a unit that
// already exists replaces it, so a changed program or argument takes effect.
func (u *Units) Install(ctx context.Context, req UnitRequest) (Unit, error) {
	spec, err := u.spec(req)
	if err != nil {
		return Unit{}, err
	}
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return Unit{}, err
	}
	ops := u.ops()
	if _, err := ops.install(spec); err != nil {
		return Unit{}, fmt.Errorf("installing unit %s: %w", req.Name, err)
	}
	if err := ops.start(spec); err != nil {
		return Unit{}, fmt.Errorf("starting unit %s: %w", req.Name, err)
	}
	return u.Get(req.Name)
}

func (u *Units) Get(name string) (Unit, error) {
	if !unitName.MatchString(name) {
		return Unit{}, badRequest("unit name %q must be lowercase letters, digits and dashes", name)
	}
	// State is what the service manager can tell us without a status call,
	// which internal/service does not expose. Whether the runtime inside a
	// unit is usable is answered by its ready record, not by this field.
	if !u.ops().installed(service.Spec{Name: name}) {
		return Unit{}, ErrUnknownUnit
	}
	return Unit{Name: name, LogPath: u.logPath(name), State: "installed"}, nil
}

func (u *Units) List() ([]Unit, error) {
	entries, err := os.ReadDir(u.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Unit
	for _, e := range entries {
		name, ok := logName(e.Name())
		if !ok {
			continue
		}
		unit, err := u.Get(name)
		if errors.Is(err, ErrUnknownUnit) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, unit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func logName(file string) (string, bool) {
	if filepath.Ext(file) != ".log" {
		return "", false
	}
	name := file[:len(file)-len(".log")]
	return name, unitName.MatchString(name)
}

// Remove stops and uninstalls the unit. Its log is left behind, because a
// failed unit's log is what explains the failure.
func (u *Units) Remove(name string) (Unit, error) {
	unit, err := u.Get(name)
	if err != nil {
		return Unit{}, err
	}
	if _, err := u.ops().uninstall(service.Spec{Name: name}); err != nil {
		return Unit{}, err
	}
	unit.State = "removed"
	return unit, nil
}

// Tail returns at most limit bytes from the end of the unit's log. Callers
// parse records from it, so the end is what matters: a long-lived unit appends
// and the newest record is last.
func (u *Units) Tail(name string, limit int64) ([]byte, error) {
	if !unitName.MatchString(name) {
		return nil, badRequest("unit name %q must be lowercase letters, digits and dashes", name)
	}
	f, err := os.Open(u.logPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrUnknownUnit
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, 0); err != nil {
			return nil, err
		}
	}
	buf := make([]byte, min(info.Size(), limit))
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/box/ -run "TestInstallStarts|TestUnitNames|TestGetReports|TestTailReturns" -v`
Expected: PASS

- [ ] **Step 5: Add ErrUnknownUnit to the status mapping**

In `internal/box/api.go:114`, add `ErrUnknownUnit` to the `StatusNotFound` case so a missing unit is a 404 rather than a 400:

```go
	case errors.Is(err, ErrUnknownLocation), errors.Is(err, ErrUnknownWorktree), errors.Is(err, ErrUnknownSession), errors.Is(err, ErrUnknownShare), errors.Is(err, ErrUnknownUnit):
		return http.StatusNotFound
```

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/box/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/box/units.go internal/box/units_test.go internal/box/api.go
git commit -m "feat(box): install and report managed units"
```

---

### Task 5: Unit routes and client

Expose Task 4 over the wire, following the `route(...)` table and `Client` method style already in the package.

**Files:**
- Modify: `internal/box/api.go` (the `Box` struct, `Mount`, and new handlers)
- Modify: `internal/box/client.go` (new methods after `RemoveShare`)
- Modify: `cmd/calportd/main.go` (wire `Units` into the `Box`)
- Test: `internal/box/api_test.go`

**Interfaces:**
- Consumes: `box.Units`, `box.Unit`, `box.UnitRequest`, `box.ErrUnknownUnit` from Task 4.
- Produces:
  - `Box.Units *Units` field.
  - `(*Client).Units(ctx) ([]Unit, error)`
  - `(*Client).AddUnit(ctx, req UnitRequest) (Unit, error)`
  - `(*Client).Unit(ctx, name string) (Unit, error)`
  - `(*Client).RemoveUnit(ctx, name string) (Unit, error)`
  - `(*Client).UnitLog(ctx, name string, limit int64) ([]byte, error)`

- [ ] **Step 1: Write the failing test**

First give the existing harness a `Units`. In `internal/box/api_test.go:41`, add the field to the `&Box{...}` literal inside `servedBox`:

```go
	units := &Units{Dir: filepath.Join(dir, "units")}
	units.svc, _ = fakeService() // never install a real unit from a test
	(&Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Sessions: sessions, Shares: &Shares{}, Units: units, Events: bus}).Mount(srv)
```

`fakeService` comes from `units_test.go` (Task 4) — same package, so it is already available here.

Then append to `internal/box/api_test.go`:

```go
func TestUnitRoutesInstallListAndRemove(t *testing.T) {
	wc, _ := servedBox(t)
	c := NewClient(wc)
	ctx := context.Background()

	got, err := c.AddUnit(ctx, UnitRequest{Name: "calport-probe", Program: "/bin/sh", Args: []string{"-c", "true"}})
	if err != nil || got.Name != "calport-probe" {
		t.Fatalf("AddUnit() = %+v, %v", got, err)
	}
	all, err := c.Units(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("Units() = %+v, %v; want one unit", all, err)
	}
	if _, err := c.RemoveUnit(ctx, "calport-probe"); err != nil {
		t.Fatalf("RemoveUnit() = %v", err)
	}
}

func TestUnitRoutesReportAMissingUnitAsNotFound(t *testing.T) {
	wc, _ := servedBox(t)
	if _, err := NewClient(wc).Unit(context.Background(), "calport-missing"); err == nil {
		t.Fatal("Unit() on a missing unit succeeded; want an error")
	}
}

func TestUnitLogRoundTripsArbitraryBytes(t *testing.T) {
	wc, _ := servedBox(t)
	c := NewClient(wc)
	ctx := context.Background()
	if _, err := c.AddUnit(ctx, UnitRequest{Name: "calport-probe", Program: "/bin/sh", Args: []string{"-c", "true"}}); err != nil {
		t.Fatal(err)
	}
	// The log is read as bytes, not text: a runtime can write anything.
	got, err := c.UnitLog(ctx, "calport-probe", 1<<20)
	if err != nil {
		t.Fatalf("UnitLog() = %v", err)
	}
	if got == nil {
		t.Fatal("UnitLog() returned nil; want the log's bytes, even if empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/box/ -run TestUnitRoutes -v`
Expected: FAIL — `b.Units undefined`

- [ ] **Step 3: Add the field, routes and handlers**

In `internal/box/api.go`, add to the `Box` struct beside `Kit`:

```go
	// Units runs calportd's managed units; nil where they cannot run.
	Units *Units
```

In `Mount`, beside the share routes:

```go
	route("GET /v1/units", b.listUnits)
	route("POST /v1/units", b.addUnit)
	route("GET /v1/units/{name}", b.getUnit)
	route("DELETE /v1/units/{name}", b.removeUnit)
	route("GET /v1/units/{name}/log", b.unitLog)
```

Add the handlers beside the share handlers:

```go
func (b *Box) units() (*Units, error) {
	if b.Units == nil {
		return nil, badRequest("this box cannot run managed units")
	}
	return b.Units, nil
}

func (b *Box) listUnits(w http.ResponseWriter, r *http.Request) error {
	u, err := b.units()
	if err != nil {
		return err
	}
	all, err := u.List()
	if err != nil {
		return err
	}
	writeJSON(w, all)
	return nil
}

func (b *Box) addUnit(w http.ResponseWriter, r *http.Request) error {
	u, err := b.units()
	if err != nil {
		return err
	}
	var req UnitRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	unit, err := u.Install(r.Context(), req)
	if err != nil {
		return err
	}
	// The unit's name only; its arguments can carry credentials.
	b.publish(r, "unit.started", map[string]any{"name": unit.Name})
	writeJSON(w, unit)
	return nil
}

func (b *Box) getUnit(w http.ResponseWriter, r *http.Request) error {
	u, err := b.units()
	if err != nil {
		return err
	}
	unit, err := u.Get(r.PathValue("name"))
	if err != nil {
		return err
	}
	writeJSON(w, unit)
	return nil
}

func (b *Box) removeUnit(w http.ResponseWriter, r *http.Request) error {
	u, err := b.units()
	if err != nil {
		return err
	}
	unit, err := u.Remove(r.PathValue("name"))
	if err != nil {
		return err
	}
	b.publish(r, "unit.stopped", map[string]any{"name": unit.Name})
	writeJSON(w, unit)
	return nil
}

func (b *Box) unitLog(w http.ResponseWriter, r *http.Request) error {
	u, err := b.units()
	if err != nil {
		return err
	}
	limit := int64(1 << 20)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n > 1<<20 {
			return badRequest("limit must be between 1 and %d", 1<<20)
		}
		limit = n
	}
	out, err := u.Tail(r.PathValue("name"), limit)
	if err != nil {
		return err
	}
	// A []byte marshals as base64, so a log holding arbitrary bytes survives
	// the round trip that a plain string would corrupt.
	writeJSON(w, map[string][]byte{"log": out})
	return nil
}
```

Ensure `api.go` imports `strconv`.

- [ ] **Step 4: Add the client methods**

In `internal/box/client.go`, after `RemoveShare`:

```go
func (c *Client) Units(ctx context.Context) (out []Unit, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/units", nil, &out)
}

func (c *Client) AddUnit(ctx context.Context, req UnitRequest) (out Unit, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/units", req, &out)
}

func (c *Client) Unit(ctx context.Context, name string) (out Unit, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/units/"+url.PathEscape(name), nil, &out)
}

func (c *Client) RemoveUnit(ctx context.Context, name string) (out Unit, err error) {
	return out, c.call(ctx, http.MethodDelete, "/v1/units/"+url.PathEscape(name), nil, &out)
}

// UnitLog returns the end of a unit's log. A unit's output can contain
// credentials, so callers parse it and must not log what they read.
func (c *Client) UnitLog(ctx context.Context, name string, limit int64) ([]byte, error) {
	var out struct {
		Log []byte `json:"log"`
	}
	path := fmt.Sprintf("/v1/units/%s/log?limit=%d", url.PathEscape(name), limit)
	if err := c.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Log == nil {
		out.Log = []byte{}
	}
	return out.Log, nil
}
```

`call` (`internal/box/client.go:38`) always JSON-decodes its `out`, which is why the route returns the log in a JSON field rather than as a raw body. Ensure `client.go` imports `fmt`.

- [ ] **Step 5: Wire Units into calportd**

In `cmd/calportd/main.go`, where the `box.Box` is constructed with `Kit`, `Shares` and the rest, set:

```go
		Units: &box.Units{Dir: filepath.Join(home, "units")},
```

using the same `home` the daemon already uses for its state directory.

- [ ] **Step 6: Run the tests**

Run: `go build ./... && go test ./internal/box/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/box/api.go internal/box/client.go internal/box/api_test.go cmd/calportd/main.go
git commit -m "feat(box): expose managed units over the wire"
```

---

### Task 6: Ready-record parsing and validation

The security boundary. The record's advertised endpoint decides what the local Orca app connects to, so it is validated as untrusted input even though it arrives over an authenticated channel.

**Files:**
- Create: `internal/orca/ready.go`
- Test: `internal/orca/ready_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `orca.Ready` — the parsed record: `{RuntimeID string; Advertised string; PairingURL string; LocalPort int; RemotePort int}`.
  - `orca.ParseReady(log []byte, pinnedPort int) (Ready, error)` — returns the last valid record, or an error naming the failed check.

- [ ] **Step 1: Write the failing test**

Create `internal/orca/ready_test.go`:

```go
package orca

import (
	"encoding/json"
	"strings"
	"testing"
)

// record builds a ready log line. Fields are overridden by the caller to make
// one field invalid at a time.
func record(t *testing.T, edit func(m map[string]any)) string {
	t.Helper()
	m := map[string]any{
		"type":          "orca_server_ready",
		"schemaVersion": 1,
		"runtimeId":     "runtime-example",
		"boundEndpoint": "ws://127.0.0.1:41001",
		"pairing": map[string]any{
			"available": true,
			"url":       "https://orca.example/pair/SECRET-TOKEN",
			"endpoint":  "ws://127.0.0.1:16769",
		},
		"advertisedEndpoint": "ws://127.0.0.1:16769",
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseReadyAcceptsAValidRecord(t *testing.T) {
	got, err := ParseReady([]byte(record(t, func(map[string]any) {})), 16769)
	if err != nil {
		t.Fatalf("ParseReady() = %v; want the record accepted", err)
	}
	if got.RuntimeID != "runtime-example" || got.LocalPort != 16769 || got.RemotePort != 41001 {
		t.Fatalf("ParseReady() = %+v; want runtime-example, 16769, 41001", got)
	}
}

func TestParseReadyKeepsTheLastRecord(t *testing.T) {
	stale := record(t, func(m map[string]any) { m["runtimeId"] = "runtime-stale" })
	current := record(t, func(m map[string]any) { m["runtimeId"] = "runtime-current" })
	log := "noise that is not json\n" + stale + "\n" + current + "\n"

	got, err := ParseReady([]byte(log), 16769)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeID != "runtime-current" {
		t.Fatalf("RuntimeID = %q; want the last record's runtime", got.RuntimeID)
	}
}

func TestParseReadyRejectsUnusableRecords(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"wrong schema":            func(m map[string]any) { m["schemaVersion"] = 2 },
		"no runtime id":           func(m map[string]any) { m["runtimeId"] = "" },
		"pairing unavailable":     func(m map[string]any) { m["pairing"].(map[string]any)["available"] = false },
		"no pairing url":          func(m map[string]any) { m["pairing"].(map[string]any)["url"] = "" },
		"pairing endpoint differs": func(m map[string]any) {
			m["pairing"].(map[string]any)["endpoint"] = "ws://127.0.0.1:16770"
		},
		"advertised not loopback": func(m map[string]any) { m["advertisedEndpoint"] = "ws://example.com:16769" },
		"advertised has userinfo": func(m map[string]any) { m["advertisedEndpoint"] = "ws://secret@127.0.0.1:16769" },
		"advertised has path":     func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:16769/path" },
		"advertised has query":    func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:16769?a=b" },
		"advertised has fragment": func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:16769#f" },
		"advertised not ws":       func(m map[string]any) { m["advertisedEndpoint"] = "http://127.0.0.1:16769" },
		"advertised port zero":    func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:0" },
		"advertised port too big": func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:70000" },
		"bound not ws":            func(m map[string]any) { m["boundEndpoint"] = "http://127.0.0.1:41001" },
		"bound port too big":      func(m map[string]any) { m["boundEndpoint"] = "ws://0.0.0.0:70000" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			line := record(t, func(m map[string]any) {
				edit(m)
				// A changed advertised endpoint must stay consistent with
				// pairing.endpoint, so each case fails for its own reason.
				if name != "pairing endpoint differs" {
					m["pairing"].(map[string]any)["endpoint"] = m["advertisedEndpoint"]
				}
			})
			if _, err := ParseReady([]byte(line), 16769); err == nil {
				t.Fatalf("ParseReady accepted a record with %s", name)
			}
		})
	}
}

func TestParseReadyAcceptsARuntimeBoundOnEveryAddress(t *testing.T) {
	// orca serve has no flag to choose a bind address and listens on 0.0.0.0,
	// so a record reporting that is normal, not suspect. calport reaches the
	// port over the box's loopback regardless.
	line := record(t, func(m map[string]any) { m["boundEndpoint"] = "ws://0.0.0.0:6768" })
	got, err := ParseReady([]byte(line), 16769)
	if err != nil {
		t.Fatalf("ParseReady() = %v; want a 0.0.0.0 bound endpoint accepted", err)
	}
	if got.RemotePort != 6768 {
		t.Fatalf("RemotePort = %d; want 6768", got.RemotePort)
	}
}

func TestParseReadyRejectsAPortItDidNotAskFor(t *testing.T) {
	line := record(t, func(m map[string]any) {
		m["advertisedEndpoint"] = "ws://127.0.0.1:16770"
		m["pairing"].(map[string]any)["endpoint"] = "ws://127.0.0.1:16770"
	})
	if _, err := ParseReady([]byte(line), 16769); err == nil {
		t.Fatal("ParseReady accepted a record advertising a port other than the pinned one")
	}
}

func TestParseReadyErrorsNeverCarryThePairingURL(t *testing.T) {
	const sentinel = "SECRET-TOKEN"
	for _, line := range []string{
		record(t, func(m map[string]any) { m["schemaVersion"] = 2 }),
		record(t, func(m map[string]any) { m["advertisedEndpoint"] = "ws://example.com:16769" }),
		record(t, func(m map[string]any) { m["boundEndpoint"] = "ws://0.0.0.0:41001" }),
	} {
		_, err := ParseReady([]byte(line), 16769)
		if err == nil {
			t.Fatal("want an error")
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error leaks the pairing credential: %v", err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orca/ -v`
Expected: FAIL — `undefined: ParseReady`

- [ ] **Step 3: Write the implementation**

Create `internal/orca/ready.go`:

```go
// Package orca connects this laptop's Orca app to a runtime on a box. calportd
// runs the runtime as a managed unit; this package validates what the runtime
// advertises, pairs a local environment with it, and keeps the route.
package orca

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Ready is a runtime's ready record, after validation.
type Ready struct {
	RuntimeID  string
	Advertised string
	// PairingURL is a credential. It must not be logged, wrapped into an
	// error, published as an event, or printed.
	PairingURL string
	LocalPort  int
	RemotePort int
}

type readyRecord struct {
	Type       string `json:"type"`
	Schema     int    `json:"schemaVersion"`
	RuntimeID  string `json:"runtimeId"`
	Bound      string `json:"boundEndpoint"`
	Advertised string `json:"advertisedEndpoint"`
	Pairing    struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Endpoint  string `json:"endpoint"`
	} `json:"pairing"`
}

// ParseReady returns the last usable record in a runtime's log. A restarted
// runtime appends, so the last record describes the runtime running now.
//
// The record is validated as untrusted input even though it reaches us over an
// authenticated channel: its advertised endpoint decides what the local Orca
// app connects to, so a misconfigured or compromised box must not be able to
// point it somewhere else. Errors name the check that failed and never the
// value, because the record carries a credential.
func ParseReady(log []byte, pinnedPort int) (Ready, error) {
	var last readyRecord
	found := false
	for _, line := range strings.Split(string(log), "\n") {
		var r readyRecord
		if json.Unmarshal([]byte(line), &r) == nil && r.Type == "orca_server_ready" {
			last, found = r, true
		}
	}
	if !found {
		return Ready{}, fmt.Errorf("the runtime has not reported that it is ready")
	}
	if last.Schema != 1 {
		return Ready{}, fmt.Errorf("the runtime reported schema version %d; this calport understands 1", last.Schema)
	}
	if last.RuntimeID == "" {
		return Ready{}, fmt.Errorf("the runtime reported no runtime id")
	}
	if !last.Pairing.Available || last.Pairing.URL == "" {
		return Ready{}, fmt.Errorf("the runtime is not offering to pair")
	}
	if last.Pairing.Endpoint != last.Advertised {
		return Ready{}, fmt.Errorf("the runtime pairs on a different endpoint than it advertises")
	}
	local, err := advertisedPort(last.Advertised)
	if err != nil {
		return Ready{}, fmt.Errorf("the runtime's advertised endpoint is unusable: %w", err)
	}
	if local != pinnedPort {
		return Ready{}, fmt.Errorf("the runtime advertises port %d; calport asked it to advertise %d", local, pinnedPort)
	}
	remote, err := boundPort(last.Bound)
	if err != nil {
		return Ready{}, fmt.Errorf("the runtime's bound endpoint is unusable: %w", err)
	}
	return Ready{
		RuntimeID:  last.RuntimeID,
		Advertised: last.Advertised,
		PairingURL: last.Pairing.URL,
		LocalPort:  local,
		RemotePort: remote,
	}, nil
}

// advertisedPort accepts only ws:// on a loopback address with nothing but a
// port. This is the endpoint the local Orca app will connect to, so it is the
// strict one: userinfo would smuggle a credential into a URL that reads as
// local, and a query, fragment or path would change what the client requests.
func advertisedPort(endpoint string) (int, error) {
	u, err := wsURL(endpoint)
	if err != nil {
		return 0, err
	}
	if u.User != nil {
		return 0, fmt.Errorf("it carries userinfo")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return 0, fmt.Errorf("it carries a query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return 0, fmt.Errorf("it carries a path")
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1":
	default:
		return 0, fmt.Errorf("its host is not a loopback address")
	}
	return portOf(u)
}

// boundPort reads the port the runtime listens on. Its host is not checked:
// orca serve offers no way to choose a bind address and listens on every one,
// so requiring loopback here would reject every real record. calport reaches
// the port through calportd over the box's own loopback either way.
func boundPort(endpoint string) (int, error) {
	u, err := wsURL(endpoint)
	if err != nil {
		return 0, err
	}
	return portOf(u)
}

func wsURL(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("it is not a URL")
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("its scheme is not ws")
	}
	return u, nil
}

func portOf(u *url.URL) (int, error) {
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("its port is not between 1 and 65535")
	}
	return port, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/orca/ -v`
Expected: PASS, including every subtest of `TestParseReadyRejectsUnusableRecords`

- [ ] **Step 5: Commit**

```bash
git add internal/orca/
git commit -m "feat(orca): validate a runtime's ready record"
```

---

### Task 7: Routes, pairing and identity verification

Persist a route per box, allocate its pinned port once, pair the local environment, verify identity, and undo only what a failed call created.

**Files:**
- Create: `internal/orca/routes.go`
- Create: `internal/orca/cli.go`
- Test: `internal/orca/routes_test.go`
- Test: `internal/orca/cli_test.go`

**Interfaces:**
- Consumes: `orca.Ready`, `orca.ParseReady` from Task 6.
- Produces:
  - `orca.Route` — `{Environment, Runtime string; LocalPort, RemotePort int}` with JSON tags `environment_id`, `runtime_id`, `local_port`, `remote_port`.
  - `orca.Store` — `{Path string}`, with `Read() (map[string]Route, error)`, `Save(map[string]Route) error`, and `PortFor(box string) (int, error)` which returns an existing pinned port or allocates the first free from 16768 and persists it.
  - `orca.CLI` — `{Exe string}`, with `Environments(ctx) ([]Environment, error)`, `AddEnvironment(ctx, name, pairingURL string) (string, error)`, `Verify(ctx, environment, runtime string) error`, `RemoveEnvironment(ctx, environment string) error`, `Exec(ctx, environment string, args []string) ([]byte, error)`.
  - `orca.Environment` — `{ID, Name string; Endpoints []string}`.
  - `orca.ErrEnvironmentEndpointDiffers` — returned when an environment named for the box exists but advertises a different endpoint.

- [ ] **Step 1: Write the failing store test**

Create `internal/orca/routes_test.go`:

```go
package orca

import (
	"path/filepath"
	"testing"
)

func TestPortForAllocatesFromTheBaseAndKeepsIt(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "orca.json")}

	first, err := s.PortFor("devl")
	if err != nil || first != portBase {
		t.Fatalf("PortFor(devl) = %d, %v; want %d", first, err, portBase)
	}
	second, err := s.PortFor("omarchy")
	if err != nil || second != portBase+1 {
		t.Fatalf("PortFor(omarchy) = %d, %v; want %d", second, err, portBase+1)
	}
	again, err := s.PortFor("devl")
	if err != nil || again != first {
		t.Fatalf("PortFor(devl) = %d, %v; want the port it already had, %d", again, err, first)
	}
}

func TestRoutesRoundTrip(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "orca.json")}
	want := map[string]Route{"devl": {Environment: "env-1", Runtime: "runtime-1", LocalPort: 16769, RemotePort: 41001}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got["devl"] != want["devl"] {
		t.Fatalf("Read()[devl] = %+v; want %+v", got["devl"], want["devl"])
	}
}

func TestReadOnAMissingFileIsEmpty(t *testing.T) {
	got, err := Store{Path: filepath.Join(t.TempDir(), "orca.json")}.Read()
	if err != nil || len(got) != 0 {
		t.Fatalf("Read() = %+v, %v; want an empty set and no error", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orca/ -run "TestPortFor|TestRoutes|TestReadOn" -v`
Expected: FAIL — `undefined: Store`

- [ ] **Step 3: Write the store**

Create `internal/orca/routes.go`:

```go
package orca

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/sean-brydon/calport/internal/statefile"
)

// portBase is where pinned local ports start. A route's port is part of the
// pairing code the local Orca app stored, so it is allocated once and never
// recomputed: moving it means pairing again.
const portBase = 16768

// Route is what this laptop remembers about one box's runtime.
type Route struct {
	Environment string `json:"environment_id"`
	Runtime     string `json:"runtime_id"`
	LocalPort   int    `json:"local_port"`
	RemotePort  int    `json:"remote_port"`
}

// Store persists routes by box name.
type Store struct{ Path string }

func (s Store) Read() (map[string]Route, error) {
	routes := map[string]Route{}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return routes, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &routes); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.Path, err)
	}
	if routes == nil {
		routes = map[string]Route{}
	}
	return routes, nil
}

func (s Store) Save(routes map[string]Route) error {
	b, err := json.MarshalIndent(routes, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(s.Path, append(b, '\n'))
}

// PortFor returns the box's pinned local port, allocating and saving the
// lowest free one from portBase the first time.
func (s Store) PortFor(box string) (int, error) {
	unlock, err := statefile.Lock(s.Path)
	if err != nil {
		return 0, err
	}
	defer unlock()
	routes, err := s.Read()
	if err != nil {
		return 0, err
	}
	if r, ok := routes[box]; ok && r.LocalPort != 0 {
		return r.LocalPort, nil
	}
	taken := map[int]bool{}
	for _, r := range routes {
		taken[r.LocalPort] = true
	}
	port := portBase
	for taken[port] {
		port++
	}
	r := routes[box]
	r.LocalPort = port
	routes[box] = r
	if err := s.Save(routes); err != nil {
		return 0, err
	}
	return port, nil
}
```

- [ ] **Step 4: Run the store tests**

Run: `go test ./internal/orca/ -run "TestPortFor|TestRoutes|TestReadOn" -v`
Expected: PASS

- [ ] **Step 5: Write the failing CLI test**

Create `internal/orca/cli_test.go`:

```go
package orca

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeOrca writes a script named orca that answers each subcommand, following
// the pattern in internal/box/providers_test.go. It returns the path of a file
// the script appends its arguments to, so a test can assert what was called.
func fakeOrca(t *testing.T, body string) (cli CLI, calls string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "orca")
	// Derived from the executable so another test file can find it with
	// callsFile without being handed the path.
	calls = exe + ".calls"
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\n" + body
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLI{Exe: exe}, calls
}

// callsFile is where fakeOrca's script appends the arguments it was called
// with.
func callsFile(t *testing.T, c CLI) string {
	t.Helper()
	return c.Exe + ".calls"
}

func TestAddEnvironmentReturnsItsID(t *testing.T) {
	cli, _ := fakeOrca(t, `
case "$1 $2" in
"environment add") echo '{"ok":true,"result":{"environment":{"id":"env-new"}}}'; exit 0;;
esac
exit 1
`)
	id, err := cli.AddEnvironment(context.Background(), "devl", "https://orca.example/pair/SECRET-TOKEN")
	if err != nil || id != "env-new" {
		t.Fatalf("AddEnvironment() = %q, %v; want env-new", id, err)
	}
}

func TestVerifyRejectsAnotherRuntime(t *testing.T) {
	cli, _ := fakeOrca(t, `
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-other","reachable":true}}}'; exit 0;;
esac
exit 1
`)
	if err := cli.Verify(context.Background(), "env-1", "runtime-expected"); err == nil {
		t.Fatal("Verify() accepted a different runtime id; want an error")
	}
}

func TestVerifyRejectsAnUnreachableRuntime(t *testing.T) {
	cli, _ := fakeOrca(t, `
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-expected","reachable":false}}}'; exit 0;;
esac
exit 1
`)
	if err := cli.Verify(context.Background(), "env-1", "runtime-expected"); err == nil {
		t.Fatal("Verify() accepted an unreachable runtime; want an error")
	}
}

func TestVerifyAcceptsTheExpectedRuntime(t *testing.T) {
	cli, _ := fakeOrca(t, `
case "$1" in
status) echo '{"ok":true,"result":{"runtime":{"runtimeId":"runtime-expected","reachable":true}}}'; exit 0;;
esac
exit 1
`)
	if err := cli.Verify(context.Background(), "env-1", "runtime-expected"); err != nil {
		t.Fatalf("Verify() = %v; want the runtime accepted", err)
	}
}

func TestCLIErrorsNeverCarryThePairingURL(t *testing.T) {
	const sentinel = "SECRET-TOKEN"
	cli, _ := fakeOrca(t, `
echo '{"ok":false,"error":{"code":"nope","message":"refused"}}'
exit 1
`)
	_, err := cli.AddEnvironment(context.Background(), "devl", "https://orca.example/pair/"+sentinel)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error leaks the pairing credential: %v", err)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/orca/ -run "TestAddEnvironment|TestVerify|TestCLIErrors" -v`
Expected: FAIL — `undefined: CLI`

- [ ] **Step 7: Write the CLI wrapper**

Create `internal/orca/cli.go`:

```go
package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/doctor"
)

// ErrEnvironmentEndpointDiffers means an environment named for the box already
// exists and points somewhere else. Repairing it silently would move a
// working environment, so the caller is told instead.
var ErrEnvironmentEndpointDiffers = errors.New("an Orca environment for this box already uses a different endpoint")

const cliTimeout = 45 * time.Second

// Environment is one of the local Orca app's environments.
type Environment struct {
	ID        string
	Name      string
	Endpoints []string
}

// CLI runs the local Orca CLI. Exe is empty in production, where the binary is
// found on PATH or in a known install prefix; tests set it to a fake.
type CLI struct{ Exe string }

func (c CLI) exe() (string, error) {
	if c.Exe != "" {
		return c.Exe, nil
	}
	if path, ok := doctor.Tool("orca"); ok {
		return path, nil
	}
	return "", fmt.Errorf("the Orca CLI is not installed on this computer")
}

type reply struct {
	OK     bool `json:"ok"`
	Result struct {
		Environment  environment   `json:"environment"`
		Environments []environment `json:"environments"`
		Runtime      struct {
			ID        string `json:"runtimeId"`
			Reachable bool   `json:"reachable"`
		} `json:"runtime"`
	} `json:"result"`
}

type environment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Endpoints []struct {
		Endpoint string `json:"endpoint"`
	} `json:"endpoints"`
}

// run calls the CLI and reports only which request failed. A pairing code is
// passed as an argument, so neither argv nor raw output may appear in an
// error.
func (c CLI) run(ctx context.Context, args ...string) (reply, error) {
	exe, err := c.exe()
	if err != nil {
		return reply{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append(args, "--json")...)
	// Never inherit routing to an unrelated paired runtime.
	for _, e := range cmd.Environ() {
		if !strings.HasPrefix(e, "ORCA_ENVIRONMENT=") && !strings.HasPrefix(e, "ORCA_PAIRING_CODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return reply{}, fmt.Errorf("the Orca request %q failed; check the runtime and the installed CLI", args[0])
	}
	var r reply
	if json.Unmarshal(out, &r) != nil || !r.OK {
		return reply{}, fmt.Errorf("Orca gave an unsuccessful or unreadable answer to %q", args[0])
	}
	return r, nil
}

func (c CLI) Environments(ctx context.Context) ([]Environment, error) {
	r, err := c.run(ctx, "environment", "list")
	if err != nil {
		return nil, err
	}
	out := make([]Environment, 0, len(r.Result.Environments))
	for _, e := range r.Result.Environments {
		env := Environment{ID: e.ID, Name: e.Name}
		for _, ep := range e.Endpoints {
			env.Endpoints = append(env.Endpoints, ep.Endpoint)
		}
		out = append(out, env)
	}
	return out, nil
}

// AddEnvironment pairs a new environment. pairingURL is a credential: it is
// passed to Orca and never returned, logged, or wrapped into an error.
func (c CLI) AddEnvironment(ctx context.Context, name, pairingURL string) (string, error) {
	r, err := c.run(ctx, "environment", "add", "--name", name, "--pairing-code", pairingURL)
	if err != nil {
		return "", err
	}
	if r.Result.Environment.ID == "" {
		return "", fmt.Errorf("Orca paired the environment but returned no id")
	}
	return r.Result.Environment.ID, nil
}

func (c CLI) RemoveEnvironment(ctx context.Context, environment string) error {
	_, err := c.run(ctx, "environment", "rm", "--environment", environment)
	return err
}

// Verify refuses unless the environment reaches the runtime we recorded. A box
// that was rebuilt gets a new runtime id, and driving it through a stale
// environment would act on the wrong machine.
func (c CLI) Verify(ctx context.Context, environment, runtime string) error {
	r, err := c.run(ctx, "status", "--environment", environment)
	if err != nil {
		return err
	}
	if !r.Result.Runtime.Reachable {
		return fmt.Errorf("the Orca runtime for this environment is not reachable")
	}
	if r.Result.Runtime.ID != runtime {
		return fmt.Errorf("the Orca runtime answering this environment is not the one calport paired with")
	}
	return nil
}

func (c CLI) Exec(ctx context.Context, environment string, args []string) ([]byte, error) {
	exe, err := c.exe()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	full := append([]string{"--environment", environment}, args...)
	out, err := exec.CommandContext(ctx, exe, full...).Output()
	if err != nil {
		return out, fmt.Errorf("the Orca command failed on this runtime")
	}
	return out, nil
}
```

- [ ] **Step 8: Run the CLI tests**

Run: `go test ./internal/orca/ -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/orca/
git commit -m "feat(orca): persist routes and pair environments"
```

---

### Task 8: Connect, with rollback

Tie the pieces together: serve, validate, forward, pair, verify, persist — and undo only what this call created if a later step fails.

**Files:**
- Create: `internal/orca/connect.go`
- Test: `internal/orca/connect_test.go`

**Interfaces:**
- Consumes: everything from Tasks 4–7, plus `agent.Client.AddPinnedForward` from Task 3 and `box.Client.AddUnit`/`UnitLog` from Task 5.
- Produces:
  - `orca.Connector` — `{Store Store; CLI CLI; Units UnitClient; Forwards ForwardClient}`.
  - `(*Connector).Serve(ctx context.Context, boxName string) (Ready, error)`
  - `orca.UnitClient` — interface `{AddUnit(ctx, box.UnitRequest) (box.Unit, error); UnitLog(ctx, string, int64) ([]byte, error)}`.
  - `orca.ForwardClient` — interface `{AddPinnedForward(ctx, box string, local, remote int, pin string) (agent.Forward, error); RemoveForward(ctx, id string) (agent.Forward, error); Forwards(ctx) ([]agent.ForwardStatus, error)}`.
  - `(*Connector).Connect(ctx context.Context, boxName string) (Route, error)`
  - `orca.Pin(boxName string) string` — returns `"orca/" + boxName`.

- [ ] **Step 1: Write the failing test**

Create `internal/orca/connect_test.go`. Use fakes that record calls, because the assertions are about what was undone:

```go
package orca

import (
	"context"
	"encoding/json"
	"fmt"
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
```

`fakeOrca` and `callsFile` already exist in `cli_test.go` from Task 7 — same package, so use them as they are. Do not redefine either.

`connect_test.go` imports `context`, `encoding/json`, `errors`, `fmt`, `os`, `path/filepath`, `strings`, `testing`, plus `internal/agent` and `internal/box`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/orca/ -run TestConnect -v`
Expected: FAIL — `undefined: Connector`

- [ ] **Step 3: Write the implementation**

Create `internal/orca/connect.go`:

```go
package orca

import (
	"context"
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
func (c *Connector) Connect(ctx context.Context, boxName string) (Route, error) {
	ready, err := c.Serve(ctx, boxName)
	if err != nil {
		return Route{}, err
	}
	route := Route{Runtime: ready.RuntimeID, LocalPort: ready.LocalPort, RemotePort: ready.RemotePort}

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
		if createdEnvironment != "" {
			c.CLI.RemoveEnvironment(ctx, createdEnvironment)
		}
		if createdForward {
			c.Forwards.RemoveForward(ctx, fwdID)
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
```

- [ ] **Step 4: Add Forwards to the agent client**

`ForwardClient` needs `Forwards(ctx)`, which `internal/agent/client.go` does not have. `Status` is at `client.go:44`; add this beside `AddPinnedForward`:

```go
func (c *Client) Forwards(ctx context.Context) ([]ForwardStatus, error) {
	s, err := c.Status(ctx)
	return s.Forwards, err
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/orca/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/orca/ internal/agent/client.go
git commit -m "feat(orca): connect a box runtime to the local Orca app"
```

---

### Task 9: Command surface

Expose the generic unit verbs where the other box commands live, and the Orca verbs on the laptop.

**Files:**
- Modify: `internal/boxcmd/boxcmd.go` (new cases beside `shares`)
- Create: `cmd/calport/orcacmds.go`
- Modify: `cmd/calport/main.go` (dispatch and usage text)
- Test: `internal/boxcmd/boxcmd_test.go`

**Interfaces:**
- Consumes: `box.Client` unit methods from Task 5; `orca.Connector`, `orca.Store`, `orca.CLI`, `orca.Pin` from Tasks 7–8.
- Produces: the commands `calport units`, `calport unit add|get|rm`, and `calport orca serve|connect|status|exec`.

- [ ] **Step 1: Write the failing test**

Append to `internal/boxcmd/boxcmd_test.go`, using its existing `run(t, reply, args...)` harness (`boxcmd_test.go:34`), which returns a recorder and the command's output:

```go
func TestUnitsListsWhatTheBoxReports(t *testing.T) {
	reply := `[{"name":"calport-orca","state":"installed","log_path":"/home/sean/.config/calport/units/calport-orca.log"}]`
	rec, out := run(t, reply, "units")
	if rec.path != "/v1/units" {
		t.Fatalf("called %q; want /v1/units", rec.path)
	}
	if !strings.Contains(out, "calport-orca") || !strings.Contains(out, "installed") {
		t.Fatalf("units output = %q; want the unit and its state", out)
	}
}

func TestUnitAddSendsTheCommandAfterDoubleDash(t *testing.T) {
	reply := `{"name":"calport-orca","state":"installed","log_path":"/tmp/calport-orca.log"}`
	rec, _ := run(t, reply, "unit", "add", "calport-orca", "--", "orca", "serve")
	if rec.body["program"] != "orca" {
		t.Fatalf("program = %v; want orca", rec.body["program"])
	}
	args, ok := rec.body["args"].([]any)
	if !ok || len(args) != 1 || args[0] != "serve" {
		t.Fatalf("args = %v; want [serve]", rec.body["args"])
	}
}
```

`recorder` (`boxcmd_test.go:17-21`) exposes `method`, `path`, `origin` and `body`, where `body` is the request JSON decoded into a `map[string]any`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/boxcmd/ -run TestUnits -v`
Expected: FAIL — unknown command `units`

- [ ] **Step 3: Add the unit commands**

In `internal/boxcmd/boxcmd.go`, beside `case "shares":`:

```go
	case "units":
		fs, asJSON := flags(rest)
		parse(fs, rest)
		all, err := c.Units(ctx)
		if err != nil {
			return err
		}
		return show(out, *asJSON, all, func() {
			if len(all) == 0 {
				fmt.Fprintln(out, "No managed units.")
				return
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tLOG")
			for _, u := range all {
				fmt.Fprintf(w, "%s\t%s\t%s\n", u.Name, u.State, u.LogPath)
			}
			w.Flush()
		})
	case "unit add":
		if len(rest) < 2 {
			return usageErr("unit add NAME -- COMMAND...")
		}
		name, command := rest[0], rest[1:]
		if command[0] == "--" {
			command = command[1:]
		}
		if len(command) == 0 {
			return usageErr("unit add NAME -- COMMAND...")
		}
		u, err := c.AddUnit(ctx, box.UnitRequest{Name: name, Program: command[0], Args: command[1:]})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Unit %s is %s; its output goes to %s\n", u.Name, u.State, u.LogPath)
		return nil
	case "unit get":
		fs, asJSON := flags(rest)
		pos, err := parse(fs, rest)
		if err != nil || len(pos) != 1 {
			return usageErr("unit get NAME")
		}
		u, err := c.Unit(ctx, pos[0])
		if err != nil {
			return err
		}
		return show(out, *asJSON, u, func() {
			fmt.Fprintf(out, "%s is %s; its output goes to %s\n", u.Name, u.State, u.LogPath)
		})
	case "unit rm":
		if len(rest) != 1 {
			return usageErr("unit rm NAME")
		}
		u, err := c.RemoveUnit(ctx, rest[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Stopped and removed unit %s; its log is still at %s\n", u.Name, u.LogPath)
		return nil
```

Add `unit add`, `unit get` and `unit rm` to whichever table in this package lists two-word commands, next to how `location add` and `session new` are registered. Ensure the file imports `box`.

- [ ] **Step 4: Add the Orca commands**

Create `cmd/calport/orcacmds.go`:

```go
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
```

`l.boxClient(name)` returns a `*wire.Client` for a paired box and `box.NewClient(wc)` wraps it — the same pair `cmd/calport/kit.go:35-39` uses. Do not add a second way to reach a box.

- [ ] **Step 5: Dispatch and document the command**

In `cmd/calport/main.go`, beside `case "integrations":`:

```go
	case "orca":
		return orcaCommand(l, rest)
```

Add to the `usage` string, in the section that lists `kit` and `route`:

```
  calport orca connect BOX                 Let this computer's Orca app reach the box's runtime
  calport orca serve|status|exec BOX       Run, check, or drive that runtime
  calport units BOX [--json]               Managed units on a box
  calport unit add BOX/NAME -- COMMAND...  Install and start a unit
```

- [ ] **Step 6: Run the tests and build**

Run: `go build ./... && go test ./... `
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/boxcmd/ cmd/calport/
git commit -m "feat(cli): add unit and orca commands"
```

---

### Task 10: Documentation

**Files:**
- Modify: `docs/integrations.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the commands from Task 9.
- Produces: no code.

- [ ] **Step 1: Document the Orca runtime flow**

In `docs/integrations.md`, in the Orca section, add a subsection covering: what `calport orca connect BOX` does, that Orca holds the pairing credential while calport stores only the route and runtime identity, that the runtime listens on all interfaces (`orca serve` has no flag to restrict its bind address, verified against a live runtime) so the host-firewall consideration REMAINS and calport does not eliminate it -- the docs must not claim otherwise -- and that a box's pinned local port is fixed once paired because the pairing code embeds it.

- [ ] **Step 2: Add the quick-start line**

In `README.md`, beside the existing `bin/calport` examples:

```sh
bin/calport orca connect devl             # this computer's Orca app now reaches devl
```

- [ ] **Step 3: Run the full suite once more**

Run: `go vet ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add docs/integrations.md README.md
git commit -m "docs: describe Orca runtime tunneling"
```

---

## Not in this plan

`calport` has no box rename, so parity fixes need forget + re-pair. Unrelated to this work; worth its own change.

Two release-packaging gaps also stand separate: the release CLI ships without `calportd-linux-*` beside it, so `calport add ssh` fails from `~/.local/bin`; and `add ssh`'s 1Password fallback did not authenticate where `ssh` with `SSH_AUTH_SOCK` pointed at 1Password's socket succeeded.
