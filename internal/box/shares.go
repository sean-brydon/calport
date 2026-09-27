package box

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Share makes one port on the box public through a Cloudflare quick tunnel.
// It is the only way anything becomes public, it exists only on request, and
// it ends when revoked or when calportd stops. The box runs the tunnel, so a
// shared link keeps working while the laptop sleeps.
type Share struct {
	ID      string    `json:"id"`
	Port    int       `json:"port"`
	URL     string    `json:"url"`
	Started time.Time `json:"started"`
	State   string    `json:"state"`
	Error   string    `json:"error,omitempty"`
}

var (
	ErrUnknownShare   = errors.New("no share with that id")
	errNoCloudflared  = errors.New("sharing needs cloudflared on the box; install it from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
	quickTunnelURL    = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	shareStartTimeout = 45 * time.Second
)

type Shares struct {
	// Binary overrides where cloudflared is found; tests use a fake.
	Binary string
	// OnStop is called when a share's tunnel exits for any reason.
	OnStop func(Share)

	mu    sync.Mutex
	items map[string]*shareProc
}

type shareProc struct {
	share Share
	cmd   *exec.Cmd
}

func (s *Shares) binary() (string, error) {
	if s.Binary != "" {
		return s.Binary, nil
	}
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "cloudflared")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errNoCloudflared
}

// Create starts a tunnel to port and waits for its public URL.
func (s *Shares) Create(ctx context.Context, port int) (Share, error) {
	if port < 1 || port > 65535 {
		return Share{}, errors.New("port must be between 1 and 65535")
	}
	if conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err != nil {
		return Share{}, fmt.Errorf("nothing is listening on port %d on this box", port)
	} else {
		conn.Close()
	}
	bin, err := s.binary()
	if err != nil {
		return Share{}, err
	}
	var idb [4]byte
	rand.Read(idb[:])
	origin := "localhost:" + strconv.Itoa(port)
	// Dev servers expect to be addressed as localhost; the public hostname
	// still reaches the app in X-Forwarded-Host.
	cmd := exec.Command(bin, "tunnel", "--no-autoupdate", "--url", "http://"+origin, "--http-host-header", origin)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Share{}, err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return Share{}, err
	}
	sh := &shareProc{cmd: cmd, share: Share{ID: hex.EncodeToString(idb[:]), Port: port, Started: time.Now().UTC(), State: "starting"}}
	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if url := quickTunnelURL.FindString(scanner.Text()); url != "" {
				select {
				case found <- url:
				default:
				}
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case url := <-found:
		sh.share.URL, sh.share.State = url, "live"
	case err := <-exited:
		return Share{}, fmt.Errorf("cloudflared exited before publishing a URL: %v", err)
	case <-time.After(shareStartTimeout):
		cmd.Process.Kill()
		return Share{}, errors.New("cloudflared did not publish a URL in time")
	case <-ctx.Done():
		cmd.Process.Kill()
		return Share{}, ctx.Err()
	}
	s.mu.Lock()
	if s.items == nil {
		s.items = map[string]*shareProc{}
	}
	s.items[sh.share.ID] = sh
	s.mu.Unlock()
	go func() {
		err := <-exited
		s.mu.Lock()
		_, still := s.items[sh.share.ID]
		delete(s.items, sh.share.ID)
		s.mu.Unlock()
		if still && s.OnStop != nil {
			stopped := sh.share
			stopped.State = "stopped"
			if err != nil {
				stopped.Error = err.Error()
			}
			s.OnStop(stopped)
		}
	}()
	return sh.share, nil
}

func (s *Shares) List() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Share, 0, len(s.items))
	for _, sh := range s.items {
		out = append(out, sh.share)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// Remove stops a share. Its public URL stops working immediately.
func (s *Shares) Remove(id string) (Share, error) {
	s.mu.Lock()
	sh, ok := s.items[id]
	delete(s.items, id)
	s.mu.Unlock()
	if !ok {
		return Share{}, ErrUnknownShare
	}
	sh.cmd.Process.Kill()
	sh.share.State = "stopped"
	return sh.share, nil
}

// StopAll ends every share; calportd calls it on shutdown so nothing stays
// public after the daemon that manages it is gone.
func (s *Shares) StopAll() {
	s.mu.Lock()
	items := s.items
	s.items = nil
	s.mu.Unlock()
	for _, sh := range items {
		sh.cmd.Process.Kill()
	}
}
