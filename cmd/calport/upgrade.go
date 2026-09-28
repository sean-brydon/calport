package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sean-brydon/calport/internal/box"
)

// upgrade replaces a box's daemon with the build shipped beside this calport,
// over calport's own connection: no SSH, and agent sessions keep running.
func upgrade(l laptop, args []string) error {
	var check bool
	fs, asJSON, err := flags("upgrade", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&check, "check", false, "only report whether the box runs an older daemon")
	})
	if err != nil || fs.NArg() != 1 {
		return errors.New("usage: calport upgrade BOX [--check [--json]]")
	}
	args = fs.Args()
	wc, err := l.boxClient(args[0])
	if err != nil {
		return err
	}
	defer wc.Reset()
	bc := box.NewClient(wc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	info, err := bc.Info(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w (a daemon too old to upgrade itself needs `calport add ssh` once)", args[0], err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := readDaemon(exe, "calportd-"+info.OS+"-"+info.Arch)
	if err != nil {
		return err
	}
	next := box.BuildID(binary)
	if check {
		if asJSON {
			return printJSON(map[string]any{"box": args[0], "current": info.Build, "available": next, "outdated": next != info.Build})
		}
		if next == info.Build {
			fmt.Printf("%s runs this build (%s).\n", args[0], next)
		} else {
			fmt.Printf("%s runs %s; this calport ships %s. Upgrade with: calport upgrade %s\n", args[0], info.Build, next, args[0])
		}
		return nil
	}
	if next == info.Build {
		fmt.Printf("%s already runs this build (%s).\n", args[0], next)
		return nil
	}
	fmt.Printf("Uploading calportd-%s-%s to %s…\n", info.OS, info.Arch, args[0])
	if err := bc.Upgrade(ctx, binary); err != nil {
		return err
	}
	wc.Reset()
	for ctx.Err() == nil {
		time.Sleep(time.Second)
		if now, err := bc.Info(ctx); err == nil && now.Build == next {
			fmt.Printf("%s upgraded: %s → %s. Sessions kept running.\n", args[0], info.Build, next)
			return nil
		}
	}
	return fmt.Errorf("%s did not come back on the new build in time; check it with calport ping %s", args[0], args[0])
}
