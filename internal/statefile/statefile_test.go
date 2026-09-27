package statefile

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteIsPrivateAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.json")
	if err := Write(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("write left extra files: %v", entries)
	}
}

func TestWriteWithBackupKeepsThePreviousContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	if err := WriteWithBackup(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("a first write produced a backup of nothing")
	}
	if err := WriteWithBackup(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "first" {
		t.Fatalf("backup = %q, want %q", backup, "first")
	}
	if err := WriteWithBackup(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	backup, _ = os.ReadFile(path + ".bak")
	if string(backup) != "first" {
		t.Fatalf("an unchanged write replaced the backup with %q", backup)
	}
}

func TestLockSerializesReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter")
	if err := Write(path, []byte{0}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := Lock(path)
			if err != nil {
				t.Error(err)
				return
			}
			defer unlock()
			b, err := os.ReadFile(path)
			if err != nil {
				t.Error(err)
				return
			}
			if err := Write(path, []byte{b[0] + 1}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != 50 {
		t.Fatalf("counter = %d after 50 locked increments; updates were lost", b[0])
	}
}

func TestHomeRejectsRelativeOverride(t *testing.T) {
	t.Setenv("CALPORT_HOME", "relative/dir")
	if _, err := Home(); err == nil {
		t.Fatal("relative CALPORT_HOME accepted")
	}
	t.Setenv("CALPORT_HOME", "/abs/dir")
	if got, err := Home(); err != nil || got != "/abs/dir" {
		t.Fatalf("Home() = %q, %v", got, err)
	}
}
