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

// filteredEnv returns cmd's environment with ORCA_ENVIRONMENT and
// ORCA_PAIRING_CODE stripped, so a value inherited from the parent process can
// never route a request to an unrelated paired runtime. Every place that
// shells out to the Orca CLI must use this, not its own copy of the loop, so
// the call sites cannot drift apart.
func filteredEnv(cmd *exec.Cmd) []string {
	src := cmd.Environ()
	env := make([]string, 0, len(src))
	for _, e := range src {
		if !strings.HasPrefix(e, "ORCA_ENVIRONMENT=") && !strings.HasPrefix(e, "ORCA_PAIRING_CODE=") {
			env = append(env, e)
		}
	}
	return env
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
	cmd.Env = filteredEnv(cmd)
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
	cmd := exec.CommandContext(ctx, exe, full...)
	cmd.Env = filteredEnv(cmd)
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("the Orca command failed on this runtime")
	}
	return out, nil
}
