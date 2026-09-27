package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// askpass answers one of ssh's questions when there is no terminal to ask on,
// as when the app runs `calport add ssh`: ssh runs `calport askpass PROMPT`
// and reads the answer from stdout. A new host's key is a yes/no question with
// its fingerprint; anything else (a password, a key passphrase) is secret.
func askpass(args []string) error {
	prompt := strings.TrimSpace(strings.Join(args, " "))
	if runtime.GOOS != "darwin" {
		return errors.New("no terminal to ask on; run calport add ssh from a terminal")
	}
	var script string
	if hostKeyQuestion(prompt) {
		script = `display dialog ` + appleString(prompt) + ` with title "Calport: trust this box?" buttons {"Cancel", "Trust and connect"} default button "Cancel" cancel button "Cancel" with icon caution
"yes"`
	} else {
		script = `text returned of (display dialog ` + appleString(prompt) + ` with title "Calport: SSH" default answer "" with hidden answer buttons {"Cancel", "OK"} default button "OK" cancel button "Cancel")`
	}
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		// ssh treats a failed askpass as "no", which is what Cancel means.
		return errors.New("cancelled")
	}
	fmt.Print(strings.TrimRight(string(out), "\n") + "\n")
	return nil
}

// hostKeyQuestion reports whether ssh is asking to trust an unknown host key.
func hostKeyQuestion(prompt string) bool {
	return strings.Contains(prompt, "(yes/no")
}

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// askpassMarker tells calport it was started by ssh as SSH_ASKPASS.
const askpassMarker = "CALPORT_ASKPASS"

// askpassEnv makes ssh ask through `calport askpass` when this process has no
// terminal. With one, ssh prompts on it as usual.
func askpassEnv(exe string) []string {
	env := os.Environ()
	if isTerminal(os.Stdin) {
		return env
	}
	return append(env, "SSH_ASKPASS="+exe, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=:0", askpassMarker+"=1")
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
