package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/boxcmd"
	"github.com/sean-brydon/calport/internal/terminal"
	"github.com/sean-brydon/calport/internal/wire"
)

// splitBox finds the box a command targets: the first argument after the
// command words, either "box" on its own or "box/rest". It returns the box
// name and the arguments with the box removed.
func splitBox(args []string, words int) (string, []string, error) {
	// Commands that take a location reference name the box as its first
	// segment, which also tells it apart from a flag's value.
	needSlash := words == 2 && !(len(args) > 1 && (args[1] == "kill" || args[1] == "screen" || args[1] == "import"))
	for i := words; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") || (needSlash && !strings.Contains(a, "/")) {
			continue
		}
		name, rest, hasRest := strings.Cut(a, "/")
		out := append([]string{}, args[:i]...)
		if hasRest {
			out = append(out, rest)
		}
		out = append(out, args[i+1:]...)
		return name, out, nil
	}
	return "", nil, errors.New("which box? put it first, e.g. devl or devl/cal")
}

// boxPath rewrites a path under this laptop's home as ~/..., which the box
// expands against its own home: /Users/alex/work/cal becomes ~/work/cal.
func boxPath(path, laptopHome string) string {
	if laptopHome == "" {
		return path
	}
	if path == laptopHome {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, laptopHome+"/"); ok {
		return "~/" + rest
	}
	return path
}

func (l laptop) boxClient(name string) (*wire.Client, error) {
	peer, ok, err := l.boxes().ByName(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no paired box named %q; see calport boxes", name)
	}
	id, err := l.identity()
	if err != nil {
		return nil, err
	}
	dial, err := networkDialer(l, peer.Network)
	if err != nil {
		return nil, err
	}
	return wire.NewClientVia(id, peer, dial), nil
}

func runOnBox(l laptop, args []string) error {
	name, rest, err := splitBox(args, boxcmd.Commands[args[0]])
	if err != nil {
		return err
	}
	if len(rest) >= 4 && rest[0] == "location" && rest[1] == "add" {
		home, _ := os.UserHomeDir()
		rest[3] = boxPath(rest[3], home)
	}
	wc, err := l.boxClient(name)
	if err != nil {
		return err
	}
	defer wc.Reset()
	ctx, stop := signalContext()
	defer stop()
	return boxcmd.Run(ctx, box.NewClient(wc), rest, os.Stdout)
}

// attach connects this terminal to a session on a box until you detach
// (tmux's Ctrl-b d) or the session ends.
func attach(l laptop, args []string) error {
	if len(args) != 1 || !strings.Contains(args[0], "/") {
		return errors.New("usage: calport attach BOX/SESSION")
	}
	name, session, _ := strings.Cut(args[0], "/")
	wc, err := l.boxClient(name)
	if err != nil {
		return err
	}
	defer wc.Reset()
	stdin, stdout := os.Stdin.Fd(), os.Stdout.Fd()
	cols, rows, err := terminal.Size(stdout)
	if err != nil {
		cols, rows = 80, 24
	}
	path := "/v1/sessions/" + url.PathEscape(session) + "/attach?cols=" + strconv.Itoa(cols) + "&rows=" + strconv.Itoa(rows)
	conn, err := wc.OpenStream(context.Background(), path, args[0])
	if err != nil {
		return err
	}
	defer conn.Close()
	if terminal.IsTerminal(stdin) {
		restore, err := terminal.MakeRaw(stdin)
		if err != nil {
			return err
		}
		defer restore()
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			if c, r, err := terminal.Size(stdout); err == nil {
				terminal.WriteResize(conn, c, r)
			}
		}
	}()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 && terminal.WriteData(conn, buf[:n]) != nil {
				return
			}
			if err != nil {
				conn.(interface{ CloseWrite() error }).CloseWrite()
				return
			}
		}
	}()
	io.Copy(os.Stdout, conn)
	fmt.Fprintf(os.Stderr, "\r\n[detached from %s]\r\n", args[0])
	return nil
}

// openTerminal opens the system terminal attached to a session, for the
// desktop app's Attach button.
func openTerminal(args []string) error {
	if len(args) != 1 || !strings.Contains(args[0], "/") {
		return errors.New("usage: calport terminal BOX/SESSION")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := shellQuote(exe) + " attach " + shellQuote(args[0])
	if runtime.GOOS != "darwin" {
		return exec.Command("x-terminal-emulator", "-e", "sh", "-c", command).Start()
	}
	script := fmt.Sprintf("tell application \"Terminal\"\n  do script %q\n  activate\nend tell", command)
	return exec.Command("osascript", "-e", script).Run()
}
