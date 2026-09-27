package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/trust"
	"github.com/sean-brydon/calport/internal/wire"
)

// daemonFor maps `uname -sm` output to the calportd build for that box.
func daemonFor(uname string) (string, error) {
	f := strings.Fields(strings.ToLower(uname))
	if len(f) != 2 {
		return "", fmt.Errorf("could not read the box's platform from %q", uname)
	}
	arch := map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[f[1]]
	if arch == "" || (f[0] != "linux" && f[0] != "darwin") {
		return "", fmt.Errorf("calportd does not support %s", uname)
	}
	return "calportd-" + f[0] + "-" + arch, nil
}

var linkPattern = regexp.MustCompile(`calport://\S+`)

// findLink pulls the pairing link out of `calportd pair` output.
func findLink(out []byte) (string, error) {
	link := linkPattern.Find(out)
	if link == nil {
		return "", errors.New("the box did not print a pairing link")
	}
	return strings.TrimRight(string(link), "'\""), nil
}

// addSSH installs calportd on a machine you can already SSH to and pairs
// with it. SSH is used for this one setup only; afterwards calport talks to
// the box directly and never needs your SSH agent again.
func addSSH(l laptop, args []string) error {
	var sshArgs []string
	for i, a := range args {
		if a == "--" {
			sshArgs = args[i+1:]
			args = args[:i]
			break
		}
	}
	fs := flag.NewFlagSet("add ssh", flag.ContinueOnError)
	name := fs.String("name", "", "local name for the box")
	listen := fs.String("listen", "", "where calportd listens (default: the box's tailnet address only)")
	address := fs.String("address", "", "address this laptop dials, when it differs from --listen")
	via := fs.String("network", "", "reach the box through this network, for SSH and afterwards")
	pos, err := parseAnywhere(fs, args)
	if err != nil || len(pos) != 1 {
		return errors.New("usage: calport add ssh [user@]HOST [--name N] [--network NET] [--listen ADDR] [--address ADDR] [-- SSH OPTIONS]")
	}
	target := pos[0]
	if err := checkName(*name); err != nil {
		return err
	}
	if *via != "" {
		// SSH to the box through the same network calport will use.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		sshArgs = append([]string{"-o", fmt.Sprintf("ProxyCommand=%s network proxy %s %%h %%p", shellQuote(exe), *via)}, sshArgs...)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The steps share one connection, so a password or host key question is
	// asked once. /tmp keeps the socket path under macOS's 104-byte limit.
	control, err := os.MkdirTemp("/tmp", "cpssh")
	if err != nil {
		return err
	}
	defer os.RemoveAll(control)
	sshArgs = append([]string{"-o", "ControlPath=" + filepath.Join(control, "%C")}, sshArgs...)
	env := askpassEnv(exe)
	if err := openMaster(sshArgs, target, env, control); err != nil {
		return err
	}
	defer exec.Command("ssh", append(append([]string{}, sshArgs...), "-O", "exit", target)...).Run()
	ssh := func(stdin []byte, remote string) ([]byte, error) {
		cmd := exec.Command("ssh", append(append([]string{}, sshArgs...), target, remote)...)
		cmd.Env = env
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return out, sshError(target, err, stderr.String())
		}
		return out, nil
	}

	fmt.Printf("Checking %s…\n", target)
	uname, err := ssh(nil, "uname -sm")
	if err != nil {
		return err
	}
	daemon, err := daemonFor(string(uname))
	if err != nil {
		return err
	}
	binary, err := readDaemon(exe, daemon)
	if err != nil {
		return err
	}

	fmt.Printf("Installing %s (%d MB)…\n", daemon, len(binary)>>20)
	upload := "mkdir -p ~/.local/bin && cat > ~/.local/bin/calportd.new && chmod +x ~/.local/bin/calportd.new && mv ~/.local/bin/calportd.new ~/.local/bin/calportd"
	if _, err := ssh(binary, upload); err != nil {
		return err
	}
	install := "~/.local/bin/calportd install"
	if *listen != "" {
		install += " --listen " + shellQuote(*listen)
	}
	out, err := ssh(nil, install)
	if err != nil {
		return err
	}
	fmt.Print(indent(string(out)))

	pair := "sleep 1; ~/.local/bin/calportd pair"
	if *address != "" {
		pair += " --address " + shellQuote(*address)
	}
	out, err = ssh(nil, pair)
	if err != nil {
		return err
	}
	link, err := findLink(out)
	if err != nil {
		return err
	}
	tok, err := pairing.ParseToken(link)
	if err != nil {
		return err
	}
	id, err := l.identity()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dial, err := networkDialer(l, *via)
	if err != nil {
		return err
	}
	reported, err := wire.PairVia(ctx, id, tok, trust.NameFromHostname(hostname, "laptop"), dial)
	if err != nil {
		return fmt.Errorf("calportd is installed but this laptop could not reach it at %s: %w\n"+
			"If the box is only reachable another way, rerun with --address", tok.Address, err)
	}
	peer := trust.Peer{Name: *name, Address: tok.Address, Network: *via, Fingerprint: tok.Fingerprint, PairedAt: time.Now().UTC()}
	if peer.Name == "" {
		peer.Name = trust.NameFromHostname(reported, "box")
		peer.Name, err = l.boxes().AddWithFreeName(peer)
	} else {
		err = l.boxes().Add(peer)
	}
	if err != nil {
		return err
	}
	if c := agentIfRunning(l); c != nil {
		c.Refresh(context.Background())
	}
	fmt.Printf("Paired with %s at %s. SSH is no longer needed for this box.\n", peer.Name, peer.Address)
	return nil
}

// checkName refuses a --name that cannot be a hostname before any work is
// done, suggesting one that can.
func checkName(name string) error {
	if name == "" || trust.ValidName(name) {
		return nil
	}
	return fmt.Errorf("%q cannot be a box name: it is part of URLs like 3000.NAME.localhost. Try --name %s", name, trust.NameFromHostname(name, "box"))
}

// openMaster authenticates once and leaves a shared connection in the
// background. Its stderr goes to a file, not a pipe: the backgrounded ssh
// keeps it open, and waiting on a pipe would wait for that process to exit.
func openMaster(sshArgs []string, target string, env []string, dir string) error {
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		return err
	}
	defer errFile.Close()
	cmd := exec.Command("ssh", append(append([]string{"-o", "ControlMaster=yes", "-o", "ControlPersist=120", "-f", "-N"}, sshArgs...), target)...)
	cmd.Env = env
	cmd.Stderr = errFile
	if err := cmd.Run(); err != nil {
		stderr, _ := os.ReadFile(errFile.Name())
		return sshError(target, err, string(stderr))
	}
	return nil
}

// sshError explains the failures people hit on a first connection.
func sshError(target string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	switch {
	case strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return fmt.Errorf("%s's host key has changed since you last connected. If the box was rebuilt, remove the old key with `ssh-keygen -R <host>` and try again; otherwise do not connect", target)
	case strings.Contains(stderr, "Host key verification failed"):
		return fmt.Errorf("%s's host key was not trusted, so calport did not connect", target)
	case strings.Contains(stderr, "Permission denied"):
		return fmt.Errorf("%s refused the login (%s). Check the user, and that your SSH agent (such as 1Password) offers the right key", target, lastLine(stderr))
	}
	return fmt.Errorf("ssh %s: %v: %s", target, err, stderr)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ") + "\n"
}

// parseAnywhere accepts flags before and after positional arguments.
func parseAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// readDaemon finds the calportd build to upload: beside calport in a build
// directory, or in Contents/Resources when calport runs inside the macOS app.
func readDaemon(exe, daemon string) ([]byte, error) {
	dir := filepath.Dir(exe)
	for _, path := range []string{filepath.Join(dir, daemon), filepath.Join(dir, "..", "Resources", daemon)} {
		if b, err := os.ReadFile(path); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no %s next to calport (%s); build it with `make daemons`", daemon, dir)
}
