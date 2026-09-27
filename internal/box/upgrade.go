package box

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// maxDaemonSize bounds an uploaded daemon; real builds are under 10 MB.
const maxDaemonSize = 64 << 20

// Info describes the running daemon, so a laptop can pick the right build to
// upload and tell whether the box already runs it.
type Info struct {
	OS    string   `json:"os"`
	Arch  string   `json:"arch"`
	Build string   `json:"build"`
	Tools []string `json:"tools"`
}

// BuildID identifies a daemon build by its bytes.
func BuildID(binary []byte) string {
	sum := sha256.Sum256(binary)
	return hex.EncodeToString(sum[:6])
}

// SelfUpdate replaces the running daemon with an uploaded build, over the same
// authenticated connection as everything else, so upgrading never needs SSH.
type SelfUpdate struct {
	// Executable is the running daemon's path.
	Executable string
	// Fingerprint is what `<new build> id` must print: proof the new build
	// runs on this box and reads the same identity.
	Fingerprint string
	// BeforeRestart runs just before the new build takes over; calportd
	// stops public shares so none outlive the daemon that manages them.
	BeforeRestart func()
	// Restart starts the new build in place of this process. The default
	// re-executes it with the same arguments, keeping the PID, so the
	// supervisor sees no restart and agent sessions are untouched.
	Restart func(path string) error
}

func (u *SelfUpdate) info() (Info, error) {
	b, err := os.ReadFile(u.Executable)
	if err != nil {
		return Info{}, err
	}
	return Info{OS: runtime.GOOS, Arch: runtime.GOARCH, Build: BuildID(b), Tools: Tools()}, nil
}

// Install verifies binary and swaps it in for the running executable. The old
// build stays in place if anything goes wrong.
func (u *SelfUpdate) Install(ctx context.Context, binary []byte) error {
	tmp := u.Executable + ".new"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tmp, "id").Output()
	if err != nil || strings.TrimSpace(string(out)) != u.Fingerprint {
		os.Remove(tmp)
		if err == nil {
			err = errors.New("it reported a different identity")
		}
		return fmt.Errorf("the uploaded calportd does not run on this box: %v", err)
	}
	return os.Rename(tmp, u.Executable)
}

func (u *SelfUpdate) restart() error {
	if u.Restart != nil {
		return u.Restart(u.Executable)
	}
	return syscall.Exec(u.Executable, os.Args, os.Environ())
}

func (b *Box) handleInfo(w http.ResponseWriter, r *http.Request) error {
	if b.Update == nil {
		return httpError{http.StatusNotFound, "this box cannot report its build"}
	}
	i, err := b.Update.info()
	if err != nil {
		return err
	}
	writeJSON(w, i)
	return nil
}

func (b *Box) handleUpgrade(w http.ResponseWriter, r *http.Request) error {
	if b.Update == nil {
		return httpError{http.StatusNotFound, "this box cannot upgrade itself"}
	}
	binary, err := io.ReadAll(io.LimitReader(r.Body, maxDaemonSize+1))
	if err != nil {
		return err
	}
	if len(binary) > maxDaemonSize {
		return badRequest("uploaded daemon is too large")
	}
	if err := b.Update.Install(r.Context(), binary); err != nil {
		return err
	}
	b.publish(r, "box.upgraded", map[string]any{"build": BuildID(binary)})
	writeJSON(w, map[string]string{"build": BuildID(binary)})
	http.NewResponseController(w).Flush()
	go func() {
		// Let the reply reach the laptop before this process is replaced.
		time.Sleep(300 * time.Millisecond)
		if b.Update.BeforeRestart != nil {
			b.Update.BeforeRestart()
		}
		if err := b.Update.restart(); err != nil {
			fmt.Fprintf(os.Stderr, "calportd: restarting into the new build failed: %v\n", err)
		}
	}()
	return nil
}
