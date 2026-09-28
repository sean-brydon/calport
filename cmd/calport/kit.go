package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sean-brydon/calport/internal/agent"
	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/kit"
)

// kitCommand handles `calport kit BOX` and `calport kit install BOX/LOCATION`.
// Installing also routes the box's worktree URLs to it from this laptop.
func kitCommand(l laptop, args []string) error {
	if len(args) > 0 && args[0] == "check" {
		return kitCheck(l, args[1:])
	}
	fs, asJSON, err := flags("kit", args, nil)
	if err != nil {
		return err
	}
	pos := fs.Args()
	switch {
	case len(pos) == 1:
		wc, err := l.boxClient(pos[0])
		if err != nil {
			return err
		}
		s, err := box.NewClient(wc).Kit(context.Background())
		if err != nil {
			return err
		}
		if asJSON {
			return printJSON(s)
		}
		if !s.Installed {
			fmt.Printf("The Cal.com kit is not installed on %s. Run: calport kit install %s/LOCATION\n", pos[0], pos[0])
			return nil
		}
		fmt.Printf("Cal.com kit for %s\n  URLs  %s\n", s.Config.Root, s.Pattern)
		return nil
	case len(pos) == 2 && pos[0] == "install":
		name, location, ok := strings.Cut(pos[1], "/")
		if !ok || location == "" {
			return errors.New("usage: calport kit install BOX/LOCATION")
		}
		wc, err := l.boxClient(name)
		if err != nil {
			return err
		}
		res, err := box.NewClient(wc).InstallKit(context.Background(), box.KitRequest{Location: location, Host: name})
		if err != nil {
			return err
		}
		routed, err := ensureKitRoute(l, name, res.Pattern)
		if err != nil {
			return fmt.Errorf("installed, but routing %s failed: %w", res.Pattern, err)
		}
		if asJSON {
			return printJSON(map[string]any{"kit": res, "routed": routed})
		}
		printKit(res, name)
		return nil
	}
	return errors.New("usage: calport kit BOX | calport kit install BOX/LOCATION | calport kit check BOX/LOCATION")
}

// ensureKitRoute sends the kit's hostnames to the box's router, unless a
// route for them already exists.
func ensureKitRoute(l laptop, boxName, pattern string) (bool, error) {
	c, err := ensureAgent(l)
	if err != nil {
		return false, err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return false, err
	}
	for _, r := range s.Routes {
		if r.Pattern == pattern {
			return false, nil
		}
	}
	_, p, _ := strings.Cut(kit.RouterAddr, ":")
	port, _ := strconv.Atoi(p)
	r := agent.Route{Pattern: pattern, Box: boxName, Port: port}
	return true, c.Call(context.Background(), "POST", "/v1/routes", r, nil)
}

func printKit(res box.KitResult, boxName string) {
	fmt.Printf("Installed the Cal.com kit on %s for %s.\n", boxName, res.Config.Root)
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
