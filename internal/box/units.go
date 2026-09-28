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
	// List discovers units by their log file, not by asking the service
	// manager for every possible name, so the file must exist as soon as a
	// unit is installed rather than waiting for the unit to produce output.
	logFile, err := os.OpenFile(u.logPath(req.Name), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Unit{}, err
	}
	logFile.Close()
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
