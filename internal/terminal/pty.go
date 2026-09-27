// Package terminal runs programs under a pseudo-terminal on the box and puts
// the laptop's terminal into raw mode, so an attached session behaves exactly
// like a local one: colours, cursor keys, resizing, and Ctrl-C.
package terminal

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// Start runs cmd with a new pseudo-terminal as its controlling terminal and
// returns the master side. Closing the master hangs the program up.
func Start(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	defer slave.Close()
	if err := Resize(master, cols, rows); err != nil {
		master.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return master, nil
}

type winsize struct{ rows, cols, x, y uint16 }

// Resize tells the program on the pseudo-terminal its window size changed.
func Resize(f *os.File, cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > 10000 || rows > 10000 {
		return nil
	}
	ws := winsize{rows: uint16(rows), cols: uint16(cols)}
	return ioctl(f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

// Size reports the window size of the terminal on fd.
func Size(fd uintptr) (cols, rows int, err error) {
	var ws winsize
	if err := ioctl(fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); err != nil {
		return 0, 0, err
	}
	return int(ws.cols), int(ws.rows), nil
}

// MakeRaw switches the terminal on fd to raw mode and returns a function that
// restores it. Keys, including Ctrl-C, then go to the remote program.
func MakeRaw(fd uintptr) (restore func(), err error) {
	var old syscall.Termios
	if err := ioctl(fd, getTermios, uintptr(unsafe.Pointer(&old))); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, setTermios, uintptr(unsafe.Pointer(&raw))); err != nil {
		return nil, err
	}
	return func() { ioctl(fd, setTermios, uintptr(unsafe.Pointer(&old))) }, nil
}

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd uintptr) bool {
	var t syscall.Termios
	return ioctl(fd, getTermios, uintptr(unsafe.Pointer(&t))) == nil
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}
