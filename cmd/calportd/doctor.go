package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/doctor"
	"github.com/sean-brydon/calport/internal/service"
)

// daemonChecks runs inside calportd serve, where the listen address is known.
func daemonChecks(b boxHome, listening string) []doctor.Check {
	const area = "calportd"
	checks := []doctor.Check{}
	host, _, _ := net.SplitHostPort(listening)
	switch ip := net.ParseIP(host); {
	case ip != nil && ip.IsUnspecified():
		checks = append(checks, doctor.Check{Area: area, Name: "listening", Status: doctor.Warn,
			Detail: listening + " answers on every interface, including the internet",
			Fix:    "calportd install  (listens on the tailnet address only)"})
	case ip != nil && ip.IsLoopback():
		checks = append(checks, doctor.Check{Area: area, Name: "listening", Status: doctor.OK, Detail: listening + " (this box only; reach it through a tunnel)"})
	default:
		checks = append(checks, doctor.Check{Area: area, Name: "listening", Status: doctor.OK, Detail: listening})
	}
	if service.Installed(daemonService(b, listening)) || service.Installed(daemonService(b, "")) {
		checks = append(checks, doctor.Check{Area: area, Name: "starts at boot", Status: doctor.OK, Detail: "installed as a user service"})
	} else {
		checks = append(checks, doctor.Check{Area: area, Name: "starts at boot", Status: doctor.Warn, Detail: "calportd runs, but not as a service", Fix: "calportd install"})
	}
	if runtime.GOOS == "linux" {
		if lingering() {
			checks = append(checks, doctor.Check{Area: area, Name: "survives logout", Status: doctor.OK, Detail: "user lingering is on"})
		} else {
			checks = append(checks, doctor.Check{Area: area, Name: "survives logout", Status: doctor.Warn, Detail: "calportd stops when you log out", Fix: "sudo loginctl enable-linger " + currentUser()})
		}
	}
	peers, err := b.clients().List()
	switch {
	case err != nil:
		checks = append(checks, doctor.Check{Area: area, Name: "paired laptops", Status: doctor.Fail, Detail: err.Error()})
	case len(peers) == 0:
		checks = append(checks, doctor.Check{Area: area, Name: "paired laptops", Status: doctor.Warn, Detail: "none yet", Fix: "calportd pair"})
	default:
		checks = append(checks, doctor.Check{Area: area, Name: "paired laptops", Status: doctor.OK, Detail: fmt.Sprintf("%d", len(peers))})
	}
	return checks
}

// runDoctor asks the running daemon for its report; without one, it reports
// that first and still checks the tools on this box.
func runDoctor(b boxHome, args []string) error {
	asJSON := len(args) > 0 && args[0] == "--json"
	var checks []doctor.Check
	if _, err := os.Stat(b.socket()); err == nil {
		checks, err = box.NewClient(box.NewLocal(b.socket())).Doctor(context.Background())
		if err != nil {
			return err
		}
	} else {
		checks = append(checks, doctor.Check{Area: "calportd", Name: "running", Status: doctor.Fail, Detail: "calportd serve is not running", Fix: "calportd install"})
		for _, t := range []string{"git", "tmux"} {
			checks = append(checks, doctor.ToolCheck("Worktrees and sessions", t, t, "Install "+t+" with your package manager", true))
		}
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(checks)
	}
	if problems := doctor.Print(os.Stdout, checks); problems > 0 {
		fmt.Printf("\n%d thing(s) to fix.\n", problems)
	} else {
		fmt.Println("\nAll good.")
	}
	return nil
}
