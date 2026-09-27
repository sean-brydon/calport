// Package statefile reads and writes calport's private state: keys, trust
// stores, and pending pairing codes.
package statefile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Home returns the state directory: CALPORT_HOME when set, otherwise the OS
// user config directory plus "calport".
func Home() (string, error) {
	if dir := os.Getenv("CALPORT_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("CALPORT_HOME must be an absolute path")
		}
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "calport"), nil
}

// Write replaces path atomically, so a crash or a concurrent reader never
// observes a partial file. Files are private to the owner.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteWithBackup keeps the previous contents at path+".bak" whenever they
// change. Trust stores are the only record of who may connect; a bad write
// must stay recoverable.
func WriteWithBackup(path string, data []byte) error {
	previous, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(previous) > 0 && !bytes.Equal(previous, data) {
		if err := Write(path+".bak", previous); err != nil {
			return err
		}
	}
	return Write(path, data)
}

// Lock takes an exclusive lock shared by every process using path, so a
// read-modify-write by the daemon and the CLI cannot interleave.
func Lock(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
