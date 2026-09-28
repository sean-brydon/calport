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

func TestTailReturnsWhateverIsThereWhenTheLogIsSmallerThanTheLimit(t *testing.T) {
	dir := t.TempDir()
	svc, _ := fakeService()
	u := &Units{Dir: dir, svc: svc}
	body := "short log\n"
	if err := os.WriteFile(filepath.Join(dir, "calport-probe.log"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := u.Tail("calport-probe", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != body {
		t.Fatalf("Tail() = %q; want the whole file %q, not an error or a short read", out, body)
	}
}
