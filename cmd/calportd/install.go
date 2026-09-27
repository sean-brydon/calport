package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sean-brydon/calport/internal/service"
)

func daemonService(b boxHome, listen string) service.Spec {
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	name := "calportd"
	if runtime.GOOS == "darwin" {
		name = "com.calcom.calportd"
	}
	args := []string{"serve"}
	if listen != "" {
		args = append(args, "--listen", listen)
	}
	return service.Spec{
		Name:         name,
		Description:  "calport box daemon",
		Program:      exe,
		Args:         args,
		Env:          map[string]string{"CALPORT_HOME": filepath.Dir(b.dir)},
		LogPath:      filepath.Join(b.dir, "calportd.log"),
		KeepChildren: true,
	}
}

func install(b boxHome, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to listen on (default: this box's tailnet address only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *listen == "" {
		addr, err := defaultListen(interfaceIPs())
		if err != nil {
			return err
		}
		*listen = addr
	}
	path, err := service.Install(daemonService(b, *listen))
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s; calportd is serving on %s.\n", path, *listen)
	if runtime.GOOS == "linux" && !lingering() {
		fmt.Println("Warning: user lingering is off, so calportd stops when you log out.")
		fmt.Println("Enable it once with: sudo loginctl enable-linger " + currentUser())
	}
	fmt.Println("Next: calportd pair")
	return nil
}

func lingering() bool {
	out, err := exec.Command("loginctl", "show-user", currentUser(), "-p", "Linger").Output()
	return err == nil && strings.TrimSpace(string(out)) == "Linger=yes"
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}
