package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/kit"
	"github.com/sean-brydon/calport/internal/trust"
)

// kitInstaller is nil where the kit cannot run: it relies on systemd user
// units for each worktree's dev server.
func kitInstaller(exe string) *kit.Installer {
	if runtime.GOOS != "linux" {
		return nil
	}
	in, err := kit.NewInstaller(exe)
	if err != nil {
		return nil
	}
	return in
}

// runKit handles `calportd kit [install LOCATION [--host LABEL] | router]`.
func runKit(b boxHome, args []string) error {
	if len(args) == 0 || args[0] == "status" {
		c := box.NewClient(box.NewLocal(b.socket()))
		s, err := c.Kit(context.Background())
		if err != nil {
			return err
		}
		if !s.Installed {
			fmt.Println("The Cal.com kit is not installed. Run: calportd kit install LOCATION")
			return nil
		}
		fmt.Printf("Cal.com kit for %s\n  URLs     %s\n  scripts  %s\n", s.Config.Root, s.Pattern, s.Dir)
		return nil
	}
	switch args[0] {
	case "router":
		fs := flag.NewFlagSet("kit router", flag.ContinueOnError)
		dir := fs.String("dir", "", "the kit directory")
		listen := fs.String("listen", kit.RouterAddr, "loopback address to listen on")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			return errors.New("usage: calportd kit router --dir DIR")
		}
		return kit.NewRouter(*dir).Serve(*listen)
	case "install":
		fs := flag.NewFlagSet("kit install", flag.ContinueOnError)
		host := fs.String("host", "", "label in worktree URLs (default: this box's hostname)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: calportd kit install LOCATION [--host LABEL]")
		}
		if *host == "" {
			hostname, _ := os.Hostname()
			*host = trust.NameFromHostname(strings.SplitN(hostname, ".", 2)[0], "box")
		}
		// Through the running daemon, so the location's scripts are set too.
		c := box.NewClient(box.NewLocal(b.socket()))
		res, err := c.InstallKit(context.Background(), box.KitRequest{Location: fs.Arg(0), Host: *host})
		if err != nil {
			return err
		}
		printKitLocal(res)
		return nil
	}
	return fmt.Errorf("unknown kit command %q", args[0])
}

func printKitLocal(res box.KitResult) {
	fmt.Printf("Installed the Cal.com kit for %s.\n", res.Config.Root)
	fmt.Printf("Worktree URLs look like http://NAME-abc123.%s.cal.localhost\n", res.Config.Host)
	for _, n := range res.Notes {
		fmt.Println("Note: " + n)
	}
	if res.Config.Orca != "" && res.HooksFrom != "orca" {
		fmt.Println("\nSo worktrees made in Orca's app are set up too, open the repository in Orca's")
		fmt.Println("settings, under Worktree Hooks, and set:")
		fmt.Println("  Setup    " + kit.SetupHook)
		fmt.Println("  Archive  " + kit.ArchiveHook)
		fmt.Println("with Run by default, and Wait for setup to complete before starting agent.")
	}
}
