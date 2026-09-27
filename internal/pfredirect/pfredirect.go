// Package pfredirect lets local URLs drop their port on macOS. Binding port
// 80 on a loopback address needs root, so instead a pf rule redirects loopback
// port 80 to the unprivileged proxy port. Nothing runs as root afterwards: the
// rule lives in the kernel, and a boot job that only runs Apple's /sbin/pfctl
// reloads it after a restart.
package pfredirect

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"net"
	"os"
	"os/exec"
	"strings"
)

const (
	// Anchor sits under Apple's rdr-anchor "com.apple/*", which the default
	// /etc/pf.conf already evaluates, so pf.conf is never edited.
	Anchor = "com.apple/calport"
	Label  = "com.calcom.calport.pf"
)

// Paths are variables so tests can redirect them.
var (
	RulesPath = "/etc/pf.anchors/calport"
	PlistPath = "/Library/LaunchDaemons/" + Label + ".plist"
	pfctl     = "/sbin/pfctl"
)

// Rules redirects port 80 on each loopback address to port on the same
// address. Addresses are fixed loopback literals, never user input.
func Rules(port int, ipv4 bool) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# calport: http://<port>.<box>.localhost/ without a port. Remove with: calport setup port80 --remove\n")
	fmt.Fprintf(&b, "rdr pass on lo0 inet6 proto tcp from any to ::1 port 80 -> ::1 port %d\n", port)
	if ipv4 {
		fmt.Fprintf(&b, "rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 80 -> 127.0.0.1 port %d\n", port)
	}
	return b.Bytes()
}

// bootScript runs at every boot. Apple's anchors only take effect once the
// stock ruleset that references them is loaded, which macOS does not do on
// its own; it is loaded only when pf holds no rules at all, so another tool's
// ruleset is never replaced. -E enables pf with a reference, leaving other
// users of pf unaffected.
func bootScript() string {
	return fmt.Sprintf(`p=%[1]s; if ! $p -s nat 2>/dev/null | grep -q 'com.apple/\*' && [ -z "$($p -s nat 2>/dev/null)$($p -s rules 2>/dev/null)" ]; then $p -f /etc/pf.conf; fi; $p -E -a %[2]s -f %[3]s`, pfctl, Anchor, RulesPath)
}

// Plist is the boot job. It runs only Apple's /bin/sh and /sbin/pfctl.
func Plist() []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>
<string>/bin/sh</string><string>-c</string><string>%s</string>
</array>
<key>RunAtLoad</key><true/>
</dict></plist>
`, Label, html.EscapeString(bootScript())))
}

// ensureAppleAnchors loads the stock ruleset when pf has none, so the anchor
// calport's rule lives in is actually evaluated. A ruleset some other tool
// loaded is left alone and reported instead of replaced.
func ensureAppleAnchors() error {
	nat, _ := exec.Command(pfctl, "-s", "nat").Output()
	if bytes.Contains(nat, []byte("com.apple/*")) {
		return nil
	}
	rules, _ := exec.Command(pfctl, "-s", "rules").Output()
	if len(bytes.TrimSpace(nat)) > 0 || len(bytes.TrimSpace(rules)) > 0 {
		return errors.New(`pf already has rules from another tool that do not include Apple's anchors; add rdr-anchor "com.apple/*" to that ruleset, then run this again`)
	}
	if out, err := exec.Command(pfctl, "-f", "/etc/pf.conf").CombinedOutput(); err != nil {
		return fmt.Errorf("loading /etc/pf.conf: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Installed reports whether the redirect targets port, so URLs can drop it.
func Installed(port int) bool {
	b, err := os.ReadFile(RulesPath)
	return err == nil && bytes.Contains(b, []byte(fmt.Sprintf("-> ::1 port %d\n", port)))
}

// CoversIPv4 reports whether 127.0.0.1 is redirected too.
func CoversIPv4() bool {
	b, err := os.ReadFile(RulesPath)
	return err == nil && bytes.Contains(b, []byte("to 127.0.0.1 port 80"))
}

// Install writes the rule and boot job and loads them. It must run as root.
// It reports whether IPv4 was covered: 127.0.0.1:80 is left alone when
// another program already listens there.
func Install(port int) (ipv4 bool, err error) {
	if os.Geteuid() != 0 {
		return false, errors.New("installing the port 80 redirect needs administrator rights")
	}
	if ln, err := net.Listen("tcp4", "127.0.0.1:80"); err == nil {
		ln.Close()
		ipv4 = true
	}
	if err := writeRootFile(RulesPath, Rules(port, ipv4)); err != nil {
		return false, err
	}
	if err := writeRootFile(PlistPath, Plist()); err != nil {
		return false, err
	}
	exec.Command("launchctl", "bootout", "system/"+Label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", "system", PlistPath).CombinedOutput(); err != nil {
		return false, fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// Load now as well; the boot job covers later restarts.
	if err := ensureAppleAnchors(); err != nil {
		return false, err
	}
	if out, err := exec.Command(pfctl, "-E", "-a", Anchor, "-f", RulesPath).CombinedOutput(); err != nil {
		return false, fmt.Errorf("pfctl: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return ipv4, nil
}

// Remove flushes the rule and deletes the boot job and rule file.
func Remove() error {
	if os.Geteuid() != 0 {
		return errors.New("removing the port 80 redirect needs administrator rights")
	}
	exec.Command(pfctl, "-a", Anchor, "-F", "all").Run()
	exec.Command("launchctl", "bootout", "system/"+Label).Run()
	for _, p := range []string{PlistPath, RulesPath} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// writeRootFile writes a root-owned file others can read but not change: pf
// loads the rule file as root, so no user may be able to edit it.
func writeRootFile(path string, data []byte) error {
	tmp := path + ".calport-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Chown(tmp, 0, 0); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
