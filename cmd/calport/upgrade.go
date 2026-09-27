package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sean-brydon/calport/internal/box"
)

// upgrade replaces a box's daemon with the build shipped beside this calport,
// over calport's own connection: no SSH, and agent sessions keep running.
func upgrade(l laptop, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: calport upgrade BOX")
	}
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
