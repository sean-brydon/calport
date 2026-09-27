package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/service"
)

func agentService(l laptop) (service.Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Spec{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, err := filepath.Abs(filepath.Dir(l.dir))
	if err != nil {
		return service.Spec{}, err
	}
	name := "calport-agent"
	if runtime.GOOS == "darwin" {
		name = "com.calcom.calport.agent"
	}
	return service.Spec{
		Name:        name,
		Description: "calport agent",
		Program:     exe,
		Args:        []string{"agent"},
		Env:         map[string]string{"CALPORT_HOME": home},
		LogPath:     filepath.Join(l.dir, "agent.log"),
	}, nil
}

// ensureAgent returns a client for a running agent, starting one if needed:
// through the supervisor when installed, otherwise as a detached process.
func ensureAgent(l laptop) (*agent.Client, error) {
	c := agent.NewClient(l.socket())
	if c.Running(context.Background()) {
		return c, nil
	}
	spec, err := agentService(l)
	if err != nil {
		return nil, err
	}
	if service.Installed(spec) {
		if err := service.Start(spec); err != nil {
			return nil, err
		}
	} else if err := spawnAgent(l, spec.Program); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Running(context.Background()) {
			return c, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("the calport agent did not start; see %s", filepath.Join(l.dir, "agent.log"))
}

func spawnAgent(l laptop, exe string) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(l.dir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "agent")
	cmd.Env = append(os.Environ(), "CALPORT_HOME="+filepath.Dir(l.dir))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runAgent is `calport agent`: the agent in the foreground, as a supervisor
// runs it. Losing the singleton race exits cleanly so it is not restarted.
func runAgent(l laptop) error {
	ctx, stop := signalContext()
	defer stop()
	err := agent.Run(ctx, agent.Config{Dir: l.dir, Socket: l.socket()})
	if errors.Is(err, agent.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "calport: an agent is already running for this home; exiting")
		return nil
	}
	return err
}

func agentCommand(l laptop, args []string) error {
	if len(args) == 0 {
		return runAgent(l)
	}
	spec, err := agentService(l)
	if err != nil {
		return err
	}
	switch args[0] {
	case "install":
		// The supervisor starts its own agent; a running one would hold the lock.
		if c := agent.NewClient(l.socket()); c.Running(context.Background()) {
			c.Stop(context.Background())
			time.Sleep(200 * time.Millisecond)
		}
		path, err := service.Install(spec)
		if err != nil {
			return err
		}
		fmt.Printf("Installed %s\nThe agent now starts at login and restarts after a crash; calport stop still stops it.\n", path)
		return nil
	case "uninstall":
		path, err := service.Uninstall(spec)
		if err != nil {
			return err
		}
		if path == "" {
			fmt.Println("The agent service is not installed.")
			return nil
		}
		fmt.Printf("Removed %s\n", path)
		return nil
	case "status":
		running := agent.NewClient(l.socket()).Running(context.Background())
		if len(args) > 1 && args[1] == "--json" {
			return printJSON(map[string]bool{"installed": service.Installed(spec), "running": running})
		}
		fmt.Printf("service installed: %v\nagent running: %v\n", service.Installed(spec), running)
		return nil
	}
	return errors.New("usage: calport agent [install|uninstall|status]")
}
