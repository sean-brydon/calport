package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/kit"
)

const kitToolsUsage = "usage: calport kit tools BOX/LOCATION [--setup] [--tool orca,cursor,...] [--json]"

// kitTools handles `calport kit tools BOX/LOCATION`: it lists whether each
// worktree tool runs the kit's hooks for the checkout, and with --setup
// writes the tools' config files into it.
func kitTools(l laptop, args []string) error {
	fs := flag.NewFlagSet("kit tools", flag.ContinueOnError)
	setup := fs.Bool("setup", false, "write the tools' config (default: every missing one that is not opt-in)")
	tools := fs.String("tool", "", "comma-separated tools to set up, such as orca,cursor,herdr")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parseAnywhere(fs, args)
	if err != nil || len(pos) != 1 {
		return errors.New(kitToolsUsage)
	}
	boxName, location, ok := strings.Cut(pos[0], "/")
	if !ok || location == "" {
		return errors.New(kitToolsUsage)
	}
	wc, err := l.boxClient(boxName)
	if err != nil {
		return err
	}
	c := box.NewClient(wc)
	var res box.KitTools
	if *setup || *tools != "" {
		var names []string
		if *tools != "" {
			names = strings.Split(*tools, ",")
		}
		res, err = c.SetUpKitTools(context.Background(), box.KitToolsRequest{Location: location, Tools: names})
	} else {
		res, err = c.KitTools(context.Background(), location)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(res)
	}
	printKitTools(res, pos[0])
	return nil
}

func printKitTools(res box.KitTools, target string) {
	for _, f := range res.Written {
		fmt.Println("Wrote " + f)
	}
	fmt.Printf("Worktree tools for %s (%s)\n", target, res.Root)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	missing := false
	for _, t := range res.Tools {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", t.Name, t.State, t.File, t.Detail)
		missing = missing || t.State == kit.ToolMissing && !t.OptIn
	}
	w.Flush()
	fmt.Println("\nFiles go in the checkout and are excluded locally through .git/info/exclude, so they are never committed.")
	if missing {
		fmt.Printf("Set them up: calport kit tools %s --setup\n", target)
	}
}
