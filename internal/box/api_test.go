package box

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/events"
	"github.com/sean-brydon/calport/internal/identity"
	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/terminal"
	"github.com/sean-brydon/calport/internal/trust"
	"github.com/sean-brydon/calport/internal/wire"
)

// servedBox runs calportd's server with the box routes mounted and returns a
// client for a laptop paired with it.
func servedBox(t *testing.T) (*wire.Client, *events.Bus) {
	t.Helper()
	dir := t.TempDir()
	id, err := identity.LoadOrCreate(filepath.Join(dir, "identity.pem"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &wire.Server{
		Identity: id,
		Clients:  trust.NewStore(filepath.Join(dir, "clients.json")),
		Pending:  pairing.NewPending(filepath.Join(dir, "pairing.json")),
		Name:     "devbox",
	}
	sessions := testSessions(t)
	bus := &events.Bus{}
	(&Box{Name: "devbox", Locations: NewLocations(filepath.Join(dir, "locations.json")), Sessions: sessions, Shares: &Shares{}, Events: bus}).Mount(srv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)

	me, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "identity.pem"))
	if err != nil {
		t.Fatal(err)
	}
	code, _ := srv.Pending.Issue(time.Minute, time.Now())
	tok := pairing.Token{Address: ln.Addr().String(), Fingerprint: id.Fingerprint(), Code: code}
	if _, err := wire.Pair(context.Background(), me, tok, "laptop"); err != nil {
		t.Fatal(err)
	}
	c := wire.NewClient(me, trust.Peer{Name: "devbox", Address: tok.Address, Fingerprint: tok.Fingerprint})
	t.Cleanup(c.Reset)
	return c, bus
}

func call(t *testing.T, c *wire.Client, method, path, origin string, in, out any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	ctx := context.Background()
	req := func() (*http.Response, error) {
		if origin == "" {
			return c.Do(ctx, method, path, body)
		}
		return c.DoWithHeader(ctx, method, path, body, http.Header{OriginHeader: {origin}})
	}
	resp, err := req()
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestLocationsWorktreesSessionsAndAttachOverTheWire(t *testing.T) {
	c, bus := servedBox(t)
	seen, stop := bus.Subscribe()
	defer stop()
	repo := gitRepo(t)

	var loc Location
	if status := call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "cal", "path": repo}, &loc); status != 200 || !loc.Repo {
		t.Fatalf("add location: %d %+v", status, loc)
	}
	var wt Worktree
	if status := call(t, c, "POST", "/v1/locations/cal/worktrees", "orca", WorktreeRequest{Name: "billing"}, &wt); status != 200 || wt.Name != "billing" {
		t.Fatalf("add worktree: %d %+v", status, wt)
	}
	var sess Session
	if status := call(t, c, "POST", "/v1/sessions", "", map[string]string{"location": "cal/billing", "command": "cat"}, &sess); status != 200 {
		t.Fatalf("add session: %d", status)
	}
	if sess.Location != "cal/billing" || !strings.HasPrefix(sess.Name, "cal-billing-cat-") {
		t.Fatalf("session = %+v", sess)
	}

	// Attach, type, see the echo, detach.
	conn, err := c.OpenStream(context.Background(), "/v1/sessions/"+sess.Name+"/attach?cols=100&rows=30", "attach")
	if err != nil {
		t.Fatal(err)
	}
	var screen syncBuffer
	done := make(chan struct{})
	go func() { io.Copy(&screen, conn); close(done) }()
	time.Sleep(300 * time.Millisecond)
	terminal.WriteResize(conn, 120, 40)
	terminal.WriteData(conn, []byte("hello-over-the-wire\r"))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(screen.String(), "hello-over-the-wire") {
		if time.Now().After(deadline) {
			t.Fatalf("keystrokes never came back; screen %q", screen.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	conn.Close()
	<-done

	var all []Session
	call(t, c, "GET", "/v1/sessions", "", nil, &all)
	if len(all) != 1 || all[0].Exited {
		t.Fatalf("detaching ended the session: %+v", all)
	}
	if status := call(t, c, "DELETE", "/v1/sessions/"+sess.Name, "", nil, nil); status != 200 {
		t.Fatalf("kill session: %d", status)
	}

	want := map[string]string{"location.added": "calport", "worktree.created": "orca", "session.started": "calport", "session.stopped": "calport"}
	deadline = time.Now().Add(3 * time.Second)
	for len(want) > 0 && time.Now().Before(deadline) {
		select {
		case e := <-seen:
			if origin, ok := want[e.Type]; ok {
				if e.Origin != origin || e.Box != "devbox" {
					t.Errorf("%s: origin %q box %q, want origin %q", e.Type, e.Origin, e.Box, origin)
				}
				delete(want, e.Type)
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	if len(want) > 0 {
		t.Fatalf("events never published: %v", want)
	}
}

func TestErrorsMapToStatuses(t *testing.T) {
	c, _ := servedBox(t)
	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"DELETE", "/v1/locations/nope", nil, 404},
		{"POST", "/v1/sessions", map[string]string{"location": "nope"}, 404},
		{"DELETE", "/v1/shares/nope", nil, 404},
		{"POST", "/v1/events", map[string]string{"type": "Not An Event"}, 400},
		{"POST", "/v1/locations", "not an object", 400},
	} {
		if got := call(t, c, tc.method, tc.path, "", tc.body, nil); got != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestEmittedEventsReachTheStream(t *testing.T) {
	c, _ := servedBox(t)
	resp, err := c.Do(context.Background(), "GET", "/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 8)
	go func() {
		s := bufio.NewScanner(resp.Body)
		for s.Scan() {
			lines <- s.Text()
		}
	}()
	time.Sleep(100 * time.Millisecond)
	if status := call(t, c, "POST", "/v1/events", "cursor", map[string]any{"type": "agent.finished", "data": map[string]any{"path": "/w"}}, nil); status != 200 {
		t.Fatalf("emit: %d", status)
	}
	select {
	case line := <-lines:
		var e events.Event
		json.Unmarshal([]byte(line), &e)
		if e.Type != "agent.finished" || e.Origin != "cursor" || e.Data["path"] != "/w" {
			t.Fatalf("streamed event = %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("emitted event never reached the stream")
	}
}
