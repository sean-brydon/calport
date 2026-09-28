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

// TestFilteredEnvStripsPairingVarsFromRunAndExec is a permanent regression
// guard: filteredEnv strips ORCA_ENVIRONMENT and ORCA_PAIRING_CODE from the
// child's environment, but the wiring in run and Exec is what actually
// assigns cmd.Env — drop that one assignment and the filter silently does
// nothing. Cover both call sites so they cannot drift apart again.
func TestFilteredEnvStripsPairingVarsFromRunAndExec(t *testing.T) {
	t.Setenv("ORCA_ENVIRONMENT", "leaked-env")
	t.Setenv("ORCA_PAIRING_CODE", "leaked-code")

	cli, _ := fakeOrca(t, `
env > "$0.env"
echo '{"ok":true,"result":{"environments":[]}}'
`)

	assertNoPairingEnvLeaked := func(t *testing.T) {
		t.Helper()
		b, err := os.ReadFile(cli.Exe + ".env")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "ORCA_ENVIRONMENT=") || strings.Contains(string(b), "ORCA_PAIRING_CODE=") {
			t.Fatalf("child process env carries a pairing var:\n%s", b)
		}
	}

	if _, err := cli.Environments(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertNoPairingEnvLeaked(t)

	if _, err := cli.Exec(context.Background(), "env-1", []string{"status"}); err != nil {
		t.Fatal(err)
	}
	assertNoPairingEnvLeaked(t)
}
