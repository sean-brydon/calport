package terminal

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

const (
	getTermios = syscall.TIOCGETA
	setTermios = syscall.TIOCSETA
)

func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.File, *os.File, error) {
		master.Close()
		return nil, nil, err
	}
	if err := ioctl(master.Fd(), syscall.TIOCPTYGRANT, 0); err != nil {
		return fail(err)
	}
	if err := ioctl(master.Fd(), syscall.TIOCPTYUNLK, 0); err != nil {
		return fail(err)
	}
	var name [128]byte
	if err := ioctl(master.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		return fail(err)
	}
	path := string(name[:bytes.IndexByte(name[:], 0)])
	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fail(err)
	}
	return master, slave, nil
}
