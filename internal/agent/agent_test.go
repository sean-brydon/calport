package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/box"
	"github.com/sean-brydon/calport/internal/events"
	"github.com/sean-brydon/calport/internal/identity"
	"github.com/sean-brydon/calport/internal/network"
	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/trust"
	"github.com/sean-brydon/calport/internal/wire"
)

// testBox is a real calportd server on the loopback, restartable on the same
// address so tests can take a box offline and bring it back.
type testBox struct {
	services []box.Service
	bus      *events.Bus
	t        *testing.T
	dir      string
	address  string
	server   *wire.Server
	accepted atomic.Int32
	cancel   context.CancelFunc
	done     chan error
}

type countingListener struct {
	net.Listener
	n *atomic.Int32
}

func (l countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.n.Add(1)
	}
	return c, err
}

func newBox(t *testing.T) *testBox {
	t.Helper()
	b := &testBox{t: t, dir: t.TempDir()}
	b.start("127.0.0.1:0")
	t.Cleanup(b.stop)
	return b
}

func (b *testBox) start(addr string) {
	b.t.Helper()
	id, err := identity.LoadOrCreate(filepath.Join(b.dir, "identity.pem"))
	if err != nil {
		b.t.Fatal(err)
	}
	b.server = &wire.Server{
		Identity: id,
		Clients:  trust.NewStore(filepath.Join(b.dir, "clients.json")),
		Pending:  pairing.NewPending(filepath.Join(b.dir, "pairing.json")),
		Name:     "devbox",
	}
	if b.bus == nil {
		b.bus = &events.Bus{}
	}
	b.server.Handle("GET /v1/services", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(b.services)
	}))
	b.server.Handle("GET /v1/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch, stop := b.bus.Subscribe()
		defer stop()
		rc := http.NewResponseController(w)
		w.WriteHeader(http.StatusOK)
		rc.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-ch:
				json.NewEncoder(w).Encode(e)
				rc.Flush()
			}
		}
	}))
	var ln net.Listener
	for i := 0; ; i++ {
		ln, err = net.Listen("tcp", addr)
		if err == nil || i > 50 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		b.t.Fatal(err)
	}
	b.address = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.done = make(chan error, 1)
	go func() { b.done <- b.server.Serve(ctx, countingListener{ln, &b.accepted}) }()
}

func (b *testBox) stop() {
	if b.cancel != nil {
		b.cancel()
		<-b.done
		b.cancel = nil
	}
}

// pairLaptop pairs a fresh laptop directory with b and returns it.
func (b *testBox) pairLaptop() string {
	b.t.Helper()
	dir := b.t.TempDir()
	id, err := identity.LoadOrCreate(filepath.Join(dir, "identity.pem"))
	if err != nil {
		b.t.Fatal(err)
	}
	code, err := b.server.Pending.Issue(time.Minute, time.Now())
	if err != nil {
		b.t.Fatal(err)
	}
	tok := pairing.Token{Address: b.address, Fingerprint: b.server.Identity.Fingerprint(), Code: code}
	if _, err := wire.Pair(context.Background(), id, tok, "laptop"); err != nil {
		b.t.Fatal(err)
	}
	peer := trust.Peer{Name: "devbox", Address: b.address, Fingerprint: tok.Fingerprint, PairedAt: time.Now()}
	if err := trust.NewStore(filepath.Join(dir, "boxes.json")).Add(peer); err != nil {
		b.t.Fatal(err)
	}
	return dir
}

type runningAgent struct {
	client *Client
	cancel context.CancelFunc
	done   chan error
	proxy  string
	now    *clock
}

type clock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Round(0).Add(c.offset)
}

func (c *clock) jump(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

// shortSocket returns a socket path under /tmp: test temp dirs on macOS are
// longer than the 104-byte limit for Unix socket paths.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

func startAgent(t *testing.T, dir string) *runningAgent {
	t.Helper()
	return startAgentWith(t, dir, &fakeNetworks{})
}

func startAgentWith(t *testing.T, dir string, nets Networks) *runningAgent {
	t.Helper()
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddr := proxyLn.Addr().String()
	proxyLn.Close()
	socket := shortSocket(t)
	clk := &clock{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Dir:            dir,
			Socket:         socket,
			ProxyAddrs:     []string{proxyAddr},
			HealthInterval: 50 * time.Millisecond,
			Log:            log.New(io.Discard, "", 0),
			Now:            clk.Now,
			Networks:       nets,
		})
	}()
	a := &runningAgent{client: NewClient(socket), cancel: cancel, done: done, proxy: proxyAddr, now: clk}
	deadline := time.Now().Add(5 * time.Second)
	for !a.client.Running(context.Background()) {
		select {
		case err := <-done:
			t.Fatalf("agent exited: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(a.stop)
	return a
}

func (a *runningAgent) stop() {
	if a.cancel != nil {
		a.cancel()
		<-a.done
		a.cancel = nil
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func stateOf(t *testing.T, a *runningAgent) string {
	t.Helper()
	s, err := a.client.Status(context.Background())
	if err != nil || len(s.Boxes) != 1 {
		return ""
	}
	return s.Boxes[0].State
}

func echoServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func roundTrip(t *testing.T, local int, msg string) {
	t.Helper()
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte(msg))
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != msg {
		t.Fatalf("forward echo = %q, %v", buf, err)
	}
}

func TestForwardReachesTheBoxAndSurvivesAnAgentRestart(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	remote := echoServer(t)
	local := freePort(t)

	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	f, err := a.client.AddForward(context.Background(), "devbox", local, remote)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, local, "through the box")

	a.stop()
	a = startAgent(t, dir)
	eventually(t, "restored forward", func() bool {
		s, err := a.client.Status(context.Background())
		return err == nil && len(s.Forwards) == 1 && s.Forwards[0].ID == f.ID && s.Forwards[0].State == "listening"
	})
	roundTrip(t, local, "after restart")

	if _, err := a.client.RemoveForward(context.Background(), f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local))); err == nil {
		t.Fatal("removed forward still listens")
	}
	saved, _ := forwardStore{path: filepath.Join(dir, "forwards.json")}.list()
	if len(saved) != 0 {
		t.Fatalf("removed forward still saved: %+v", saved)
	}
}

func TestBoxGoingOfflineAndBackIsReportedAndForwardsRecover(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	remote := echoServer(t)
	local := freePort(t)
	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })

	var mu sync.Mutex
	var seen []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.client.Events(ctx, func(e Event) {
		mu.Lock()
		seen = append(seen, e.Type)
		mu.Unlock()
	})
	saw := func(typ string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range seen {
			if s == typ {
				return true
			}
		}
		return false
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := a.client.AddForward(context.Background(), "devbox", local, remote); err != nil {
		t.Fatal(err)
	}
	eventually(t, "forward.started event", func() bool { return saw(EventForwardStarted) })

	addr := b.address
	b.stop()
	eventually(t, "box offline", func() bool { return stateOf(t, a) == StateOffline })
	eventually(t, "box.disconnected event", func() bool { return saw(EventBoxDisconnected) })

	b.start(addr)
	eventually(t, "box back online", func() bool { return stateOf(t, a) == StateOnline })
	eventually(t, "box.connected event", func() bool { return saw(EventBoxConnected) })
	roundTrip(t, local, "after the box came back")
}

func TestWakingFromSleepReconnectsEveryBox(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	remote := echoServer(t)
	local := freePort(t)
	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	if _, err := a.client.AddForward(context.Background(), "devbox", local, remote); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, local, "before sleep")
	before := b.accepted.Load()
	// A wall-clock jump well past the health interval is what sleep looks like.
	a.now.jump(10 * time.Minute)
	eventually(t, "a fresh connection after wake", func() bool { return b.accepted.Load() > before })
	roundTrip(t, local, "after wake")
}

func TestARevokedLaptopIsReportedUntrusted(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	if _, err := b.server.Clients.Remove("laptop"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "untrusted", func() bool { return stateOf(t, a) == StateUntrusted })
}

func TestProxyServesBoxPortsAtLocalhostNames(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	app := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "app on the box saw "+r.Host)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Serve(ln)
	defer app.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	req, _ := http.NewRequest(http.MethodGet, "http://"+a.proxy+"/", nil)
	req.Host = strconv.Itoa(port) + ".devbox.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if want := "app on the box saw localhost:" + strconv.Itoa(port); string(body) != want {
		t.Fatalf("proxy body = %q, want %q", body, want)
	}
}

func TestOnlyOneAgentRunsPerStateDirectory(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	startAgent(t, dir)
	err := Run(context.Background(), Config{Dir: dir, Socket: shortSocket(t), ProxyAddrs: []string{}, Log: log.New(io.Discard, "", 0)})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second agent: %v, want ErrAlreadyRunning", err)
	}
}

func TestAForwardWhosePortIsBusyStaysSavedAndStartsOnceFree(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	remote := echoServer(t)
	local := freePort(t)
	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	if _, err := a.client.AddForward(context.Background(), "devbox", local, remote); err != nil {
		t.Fatal(err)
	}
	a.stop()

	squatter, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)))
	if err != nil {
		t.Fatal(err)
	}
	a = startAgent(t, dir)
	eventually(t, "failed forward reported", func() bool {
		s, err := a.client.Status(context.Background())
		return err == nil && len(s.Forwards) == 1 && s.Forwards[0].State == "failed"
	})
	saved, _ := forwardStore{path: filepath.Join(dir, "forwards.json")}.list()
	if len(saved) != 1 {
		t.Fatalf("a forward that failed to start was dropped from the saved set: %+v", saved)
	}
	squatter.Close()
	eventually(t, "forward retried", func() bool {
		s, err := a.client.Status(context.Background())
		return err == nil && len(s.Forwards) == 1 && s.Forwards[0].State == "listening"
	})
	roundTrip(t, local, "once the port was free")
}

func TestStopEndsTheAgentCleanly(t *testing.T) {
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	if err := a.client.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-a.done:
		if err != nil {
			t.Fatalf("agent exited with %v", err)
		}
		a.cancel = nil
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop")
	}
}

func TestBoxEventsAndLocalEventsReachTheAgentStream(t *testing.T) {
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })

	got := make(chan Event, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.client.Events(ctx, func(e Event) { got <- e })
	time.Sleep(100 * time.Millisecond)

	want := func(typ, box, origin string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-got:
				if e.Type == typ {
					if e.Box != box || e.Origin != origin {
						t.Fatalf("%s: box %q origin %q, want box %q origin %q", typ, e.Box, e.Origin, box, origin)
					}
					return
				}
			case <-deadline:
				t.Fatalf("never saw %s", typ)
			}
		}
	}
	// The relay subscribes once the box is online; give it a moment.
	eventually(t, "relay subscribed", func() bool {
		b.bus.Publish(events.Event{Type: "probe.ping", Box: "hostname-on-box", Origin: "calport"})
		select {
		case e := <-got:
			return e.Type == "probe.ping"
		case <-time.After(100 * time.Millisecond):
			return false
		}
	})
	b.bus.Publish(events.Event{Type: "worktree.created", Box: "hostname-on-box", Origin: "orca"})
	want("worktree.created", "devbox", "orca")

	if err := a.client.Call(context.Background(), "POST", "/v1/events", map[string]any{"type": "agent.finished", "origin": "cursor"}, nil); err != nil {
		t.Fatal(err)
	}
	want("agent.finished", "", "cursor")
	if err := a.client.Call(context.Background(), "POST", "/v1/events", map[string]any{"type": "Bad Type"}, nil); err == nil {
		t.Fatal("an invalid event type was accepted")
	}
}

// fakeNetworks stands in for another tailnet: addresses in routes are only
// reachable through network "personal".
type fakeNetworks struct {
	routes map[string]string
	dials  atomic.Int32
}

func (f *fakeNetworks) Dial(ctx context.Context, name, addr string) (net.Conn, error) {
	real, ok := f.routes[addr]
	if name != "personal" || !ok {
		return nil, errors.New("no route")
	}
	f.dials.Add(1)
	return (&net.Dialer{}).DialContext(ctx, "tcp", real)
}

func (f *fakeNetworks) Login(ctx context.Context, name string, onURL func(string)) (network.Info, error) {
	onURL("https://login.tailscale.com/a/test")
	return network.Info{Name: name, State: "Running", Tailnet: "alex@example.com"}, nil
}

func (f *fakeNetworks) List(context.Context) []network.Info {
	return []network.Info{{Name: "personal", State: "Running"}}
}

func (f *fakeNetworks) Close() {}

func TestABoxOnAnotherTailnetIsReachedThroughItsNetwork(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	// Re-address the box as the laptop would see it on another tailnet.
	store := trust.NewStore(filepath.Join(dir, "boxes.json"))
	peer, _, _ := store.ByName("devbox")
	peer.Address, peer.Network = "devbox.personal:7443", "personal"
	if err := store.Add(peer); err != nil {
		t.Fatal(err)
	}
	nets := &fakeNetworks{routes: map[string]string{"devbox.personal:7443": b.address}}
	a := startAgentWith(t, dir, nets)
	eventually(t, "box online through the network", func() bool { return stateOf(t, a) == StateOnline })
	if nets.dials.Load() == 0 {
		t.Fatal("the box was reached without going through its network")
	}
	remote := echoServer(t)
	local := freePort(t)
	if _, err := a.client.AddForward(context.Background(), "devbox", local, remote); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, local, "across tailnets")
}

func TestTheCLICanDialAndLogInThroughTheAgent(t *testing.T) {
	b := newBox(t)
	echo := echoServer(t)
	nets := &fakeNetworks{routes: map[string]string{"sshhost.personal:22": net.JoinHostPort("127.0.0.1", strconv.Itoa(echo))}}
	a := startAgentWith(t, b.pairLaptop(), nets)
	conn, err := a.client.Dial(context.Background(), "personal", "sshhost.personal:22")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("SSH-2.0-probe"))
	buf := make([]byte, 13)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "SSH-2.0-probe" {
		t.Fatalf("raw dial echo = %q, %v", buf, err)
	}
	if _, err := a.client.Dial(context.Background(), "personal", "nowhere:1"); err == nil {
		t.Fatal("dialing an unroutable address succeeded")
	}

	var urls []string
	info, err := a.client.Login(context.Background(), "personal", func(u string) { urls = append(urls, u) })
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || info["tailnet"] != "alex@example.com" {
		t.Fatalf("login: urls %v info %v", urls, info)
	}
}

func TestRoutesSendWholeHostPatternsToABoxPortUnchanged(t *testing.T) {
	b := newBox(t)
	dir := b.pairLaptop()
	router := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "worktree router saw "+r.Host)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go router.Serve(ln)
	defer router.Close()
	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })

	port := ln.Addr().(*net.TCPAddr).Port
	if err := a.client.Call(context.Background(), "POST", "/v1/routes", Route{Pattern: "*.cal.test.localhost", Box: "devbox", Port: port}, nil); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Route{
		{Pattern: "*.evil.com", Box: "devbox", Port: port},
		{Pattern: "*.x.localhost", Box: "nope", Port: port},
		{Pattern: "*.x.localhost", Box: "devbox", Port: 0},
	} {
		if err := a.client.Call(context.Background(), "POST", "/v1/routes", bad, nil); err == nil {
			t.Errorf("accepted route %+v", bad)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+a.proxy+"/", nil)
	req.Host = "branch-a1b2c3.cal.test.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "worktree router saw branch-a1b2c3.cal.test.localhost" {
		t.Fatalf("routed request: %q", body)
	}
	s, _ := a.client.Status(context.Background())
	if len(s.Routes) != 1 || s.Routes[0].Pattern != "*.cal.test.localhost" {
		t.Fatalf("status routes = %+v", s.Routes)
	}
	if err := a.client.Call(context.Background(), "DELETE", "/v1/routes?pattern=*.cal.test.localhost", nil, nil); err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("a removed route still routes")
		}
	}
}

func TestWorktreeHostnamesReachTheWorktreesDevServer(t *testing.T) {
	serve := func(name string) int {
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, name+" saw "+r.Host)
		})}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go srv.Serve(ln)
		t.Cleanup(func() { srv.Close() })
		return ln.Addr().(*net.TCPAddr).Port
	}
	main, billing := serve("main"), serve("billing")
	b := newBox(t)
	b.services = []box.Service{
		{Location: "cal", Worktree: "cal", Port: main, Main: true},
		{Location: "cal", Worktree: "billing", Port: billing},
		{Location: "cal", Worktree: "billing", Port: billing + 1000},
	}
	a := startAgent(t, b.pairLaptop())
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })

	for host, want := range map[string]string{
		"billing.cal.devbox.localhost": "billing saw localhost:" + strconv.Itoa(billing),
		"billing.cal.localhost":        "billing saw localhost:" + strconv.Itoa(billing),
		"cal.devbox.localhost":         "main saw localhost:" + strconv.Itoa(main),
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://"+a.proxy+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Errorf("%s: %q, want %q", host, body, want)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+a.proxy+"/", nil)
	req.Host = "nothing-running.cal.devbox.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a worktree with no server got %d", resp.StatusCode)
	}
}
