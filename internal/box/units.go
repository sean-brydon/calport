package box

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// installed answers "is the unit on disk exactly this spec?";
	// installedByName answers "is there a unit with this name at all?". See
	// the doc comments on service.Installed and service.InstalledByName.
	installed       func(service.Spec) bool
	installedByName func(string) bool
	// running answers "is it up right now?", which installed cannot: a unit
	// can be written, enabled and dead.
	running func(service.Spec) bool
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
		install:         service.Install,
		start:           service.Start,
		uninstall:       service.Uninstall,
		installed:       service.Installed,
		installedByName: service.InstalledByName,
		running:         service.Running,
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
		// A managed unit exists to stay up, and the programs it runs can shut
		// themselves down cleanly. on-failure would read that as success.
		RestartAlways: true,
	}, nil
}

func (u *Units) logPath(name string) string { return filepath.Join(u.Dir, name+".log") }

// Install writes the unit, starts it, and reports it. Installing a unit whose
// spec changed replaces it, so a changed program or argument takes effect.
func (u *Units) Install(ctx context.Context, req UnitRequest) (Unit, error) {
	spec, err := u.spec(req)
	if err != nil {
		return Unit{}, err
	}
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return Unit{}, err
	}
	// List discovers units by their log file, not by asking the service
	// manager for every possible name, so the file must exist as soon as a
	// unit is installed rather than waiting for the unit to produce output.
	logFile, err := os.OpenFile(u.logPath(req.Name), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Unit{}, err
	}
	logFile.Close()
	ops := u.ops()
	// Writing the unit reloads it, which on both platforms means stopping the
	// running program and starting it again. When the file on disk is already
	// byte-for-byte this spec there is nothing to apply, so skip it: callers
	// install to make sure a unit is running, and a healthy long-lived program
	// must not be killed just because someone asked for it again. start is
	// still called - it is a no-op on a running unit and brings back a stopped
	// one.
	if !ops.installed(spec) {
		if _, err := ops.install(spec); err != nil {
			return Unit{}, fmt.Errorf("installing unit %s: %w", req.Name, err)
		}
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
	// A name is all this call has: the spec the unit was written from is not
	// reconstructible here, so this asks whether a unit of that name exists,
	// not whether it matches a spec.
	ops := u.ops()
	if !ops.installedByName(name) {
		return Unit{}, ErrUnknownUnit
	}
	// Installed and running are different questions, and only the second
	// answers "is it working". Reporting installed for a dead unit is how a
	// stopped Orca runtime looked healthy while nothing was listening.
	state := "stopped"
	if ops.running(service.Spec{Name: name}) {
		state = "running"
	}
	return Unit{Name: name, LogPath: u.logPath(name), State: state}, nil
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

// Restart stops and starts the unit without rewriting it, for a unit whose
// program died in a way its restart policy did not cover, or which needs to
// pick up something outside its own definition.
func (u *Units) Restart(name string) (Unit, error) {
	if _, err := u.Get(name); err != nil {
		return Unit{}, err
	}
	if err := u.ops().start(service.Spec{Name: name}); err != nil {
		return Unit{}, fmt.Errorf("restarting unit %s: %w", name, err)
	}
	return u.Get(name)
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
	// A single Read is not guaranteed to fill buf, and the caller keeps the
	// last parseable record from this tail: a short read that clips the final
	// line would silently hand back a stale or truncated record. ReadFull
	// retries until buf is full or the file runs out. If the file shrank
	// between Stat and Read, that end-of-file is not an error here: return
	// whatever bytes were actually read.
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}
