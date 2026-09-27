package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/pfredirect"
)

// serviceURLFor is the private URL of a service; the port is left out once
// the port 80 redirect makes it unnecessary.
func serviceURLFor(service, box string, urlPort int) string {
	if urlPort == 80 {
		return fmt.Sprintf("http://%s.%s.localhost/", service, box)
	}
	return fmt.Sprintf("http://%s.%s.localhost:%s/", service, box, strconv.Itoa(urlPort))
}

// setup handles `calport setup port80 [--remove]`. The work needs root, so a
// normal run asks macOS for an administrator password and reruns itself.
func setup(args []string) error {
	if len(args) == 0 || args[0] != "port80" {
		return errors.New("usage: calport setup port80 [--remove]")
	}
	if runtime.GOOS != "darwin" {
		return errors.New("the port 80 redirect is macOS only; on Linux, grant the agent CAP_NET_BIND_SERVICE instead")
	}
	remove := len(args) > 1 && args[1] == "--remove"
	if os.Geteuid() != 0 {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		command := shellQuote(exe) + " setup port80"
		if remove {
			command += " --remove"
		}
		prompt := "Calport wants to redirect localhost port 80 so your box URLs need no port."
		script := fmt.Sprintf("do shell script %q with prompt %q with administrator privileges", command, prompt)
		out, err := exec.Command("osascript", "-e", script).CombinedOutput()
		text := strings.TrimSpace(string(out))
		if err != nil {
			if strings.Contains(text, "(-128)") {
				return errors.New("the administrator prompt was cancelled; nothing was changed")
			}
			// osascript reports the command's own error after "execution error: ".
			if _, reason, ok := strings.Cut(text, "execution error: "); ok {
				text = reason
			}
			return fmt.Errorf("the administrator step failed: %s", text)
		}
		fmt.Println(text)
		return nil
	}
	if remove {
		if err := pfredirect.Remove(); err != nil {
			return err
		}
		fmt.Println("Removed the port 80 redirect; URLs use port 1355 again.")
		return nil
	}
	ipv4, err := pfredirect.Install(agent.DefaultProxyPort)
	if err != nil {
		return err
	}
	fmt.Println("Box URLs no longer need a port: http://3000.<box>.localhost/")
	if !ipv4 {
		fmt.Println("127.0.0.1:80 belongs to another program, so only ::1 is redirected. Browsers try ::1 first, so URLs still work; rerun this once that program stops to cover IPv4 too.")
	}
	return nil
}
