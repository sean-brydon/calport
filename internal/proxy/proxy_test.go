package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseHost(t *testing.T) {
	for host, want := range map[string]Target{
		"3000.devl.localhost:1355": {Box: "devl", Label: "3000"},
		"web.dev-alex.localhost":   {Box: "dev-alex", Label: "web"},
		"WEB.Dev-Alex.LOCALHOST.":  {Box: "dev-alex", Label: "web"},
		"3000.devl.localhost:80":   {Box: "devl", Label: "3000"},
	} {
		got, ok := ParseHost(host)
		if !ok || got != want {
			t.Errorf("ParseHost(%q) = %+v, %v; want %+v", host, got, ok, want)
		}
	}
	for _, host := range []string{
		"localhost:1355", "devl.localhost", "a.b.c.localhost", "evil.com", "3000.devl.localhost.evil.com",
		".devl.localhost", "3000..localhost", "127.0.0.1:1355", "",
	} {
		if got, ok := ParseHost(host); ok {
			t.Errorf("ParseHost(%q) = %+v; want no route", host, got)
		}
	}
}

func TestTargetPort(t *testing.T) {
	for label, want := range map[string]int{"3000": 3000, "1": 1, "65535": 65535} {
		if got, ok := (Target{Label: label}).Port(); !ok || got != want {
			t.Errorf("Port(%q) = %d, %v", label, got, ok)
		}
	}
	for _, label := range []string{"0", "65536", "web", "03000", "-1", "+80"} {
		if _, ok := (Target{Label: label}).Port(); ok {
			t.Errorf("Port(%q) accepted", label)
		}
	}
}

func TestRewrites(t *testing.T) {
	const public = "3000.devl.localhost:1355"
	for in, want := range map[string]string{
		"http://" + public:              "http://localhost:3000",
		"http://" + public + "/a?b":     "http://localhost:3000/a?b",
		"https://accounts.google.com/x": "https://accounts.google.com/x",
	} {
		if got := toUpstream(in, public, 3000); got != want {
			t.Errorf("toUpstream(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"http://localhost:3000/login":   "http://" + public + "/login",
		"http://127.0.0.1:3000/x":       "http://" + public + "/x",
		"http://[::1]:3000/x":           "http://" + public + "/x",
		"http://localhost:4000/other":   "http://localhost:4000/other",
		"https://accounts.google.com/o": "https://accounts.google.com/o",
		"/relative":                     "/relative",
	} {
		if got := toPublic(in, public, 3000); got != want {
			t.Errorf("toPublic(%q) = %q, want %q", in, got, want)
		}
	}
}

// app stands in for a dev server on the box that reports what it received.
func app(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "http://localhost:"+strconv.Itoa(portOf(r.Host))+"/dashboard", http.StatusFound)
			return
		}
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Origin", r.Header.Get("Origin"))
		w.Header().Set("X-Seen-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		io.WriteString(w, "hello from the box")
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func portOf(host string) int {
	_, p, _ := net.SplitHostPort(host)
	n, _ := strconv.Atoi(p)
	return n
}

func direct(ctx context.Context, port int) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func newProxy() *Proxy {
	return &Proxy{
		Dialer: func(box string) (DialFunc, bool) { return direct, box == "devl" },
		Service: func(box, name string) (int, bool) {
			return 0, false
		},
	}
}

func get(t *testing.T, p http.Handler, host, path string, header http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
	req.Host = host
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec.Result()
}

func TestProxyReachesTheBoxAsIfLocal(t *testing.T) {
	port := app(t)
	host := strconv.Itoa(port) + ".devl.localhost:1355"
	resp := get(t, newProxy(), host, "/", http.Header{"Origin": {"http://" + host}})
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello from the box" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Seen-Host"); got != "localhost:"+strconv.Itoa(port) {
		t.Errorf("app saw Host %q", got)
	}
	if got := resp.Header.Get("X-Seen-Origin"); got != "http://localhost:"+strconv.Itoa(port) {
		t.Errorf("app saw Origin %q", got)
	}
	if got := resp.Header.Get("X-Seen-Forwarded-Host"); got != host {
		t.Errorf("app saw X-Forwarded-Host %q, want the public host", got)
	}
}

func TestProxyRewritesTheAppsOwnRedirects(t *testing.T) {
	port := app(t)
	host := strconv.Itoa(port) + ".devl.localhost:1355"
	resp := get(t, newProxy(), host, "/login", nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "http://"+host+"/dashboard" {
		t.Fatalf("redirect = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestProxyRefusesUnknownHostsAndBoxes(t *testing.T) {
	p := newProxy()
	for host, want := range map[string]int{
		"evil.example:1355":      http.StatusNotFound,
		"3000.unknown.localhost": http.StatusBadGateway,
		"web.devl.localhost":     http.StatusNotFound,
	} {
		if got := get(t, p, host, "/", nil).StatusCode; got != want {
			t.Errorf("%s: status %d, want %d", host, got, want)
		}
	}
}

func TestProxyExplainsAClosedPort(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	resp := get(t, newProxy(), strconv.Itoa(port)+".devl.localhost", "/", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "could not reach port") {
		t.Fatalf("closed port: %d %q", resp.StatusCode, body)
	}
}

func TestProxyResolvesNamedServices(t *testing.T) {
	port := app(t)
	p := newProxy()
	p.Service = func(box, name string) (int, bool) { return port, box == "devl" && name == "web" }
	resp := get(t, p, "web.devl.localhost:1355", "/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("named service: %d", resp.StatusCode)
	}
}

func TestPlainLocalhostServesTheIndex(t *testing.T) {
	p := newProxy()
	p.Index = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "index") })
	for _, host := range []string{"localhost:1355", "127.0.0.1:1355", "[::1]:1355"} {
		body, _ := io.ReadAll(get(t, p, host, "/", nil).Body)
		if string(body) != "index" {
			t.Errorf("%s served %q, want the index", host, body)
		}
	}
}

func TestProxyCarriesWebSocketUpgrades(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "want upgrade", http.StatusBadRequest)
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		io.Copy(conn, rw)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	front := httptest.NewServer(newProxy())
	defer front.Close()

	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	host := strconv.Itoa(port) + ".devl.localhost"
	io.WriteString(conn, "GET /_next/webpack-hmr HTTP/1.1\r\nHost: "+host+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status %d", resp.StatusCode)
	}
	io.WriteString(conn, "ping")
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("websocket echo = %q, %v", buf, err)
	}
}

func TestRoutePatterns(t *testing.T) {
	for _, ok := range []string{"*.personal.cal.localhost", "*.work.cal.localhost", "docs.localhost"} {
		if !ValidRoutePattern(ok) {
			t.Errorf("rejected %q", ok)
		}
	}
	for _, bad := range []string{"*.cal.test", "*.evil.com", "localhost", "*.localhost.evil.com", "a..localhost", "*.*.localhost", "a/b.localhost", ""} {
		if ValidRoutePattern(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	for host, want := range map[string]bool{
		"branch-a1b2c3.personal.cal.localhost":        true,
		"studio.branch-a1b2c3.personal.cal.localhost": true,
		"BRANCH.Personal.Cal.Localhost:80":            true,
		"personal.cal.localhost":                      false,
		"branch.work.cal.localhost":                   false,
		"x.personal.cal.localhost.evil.com":           false,
	} {
		if got := MatchRoute("*.personal.cal.localhost", host); got != want {
			t.Errorf("MatchRoute(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRoutedHostsPassThroughUnchanged(t *testing.T) {
	var seen *http.Request
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		http.Redirect(w, r, "http://"+r.Host+"/next", http.StatusFound)
	}))
	defer router.Close()
	port := router.Listener.Addr().(*net.TCPAddr).Port
	p := newProxy()
	p.Route = func(host string) (string, int, bool) {
		return "devl", port, MatchRoute("*.personal.cal.localhost", host)
	}
	resp := get(t, p, "branch-a1b2c3.personal.cal.localhost", "/login", http.Header{"Origin": {"http://branch-a1b2c3.personal.cal.localhost"}})
	if seen == nil {
		t.Fatal("the router never saw the request")
	}
	if seen.Host != "branch-a1b2c3.personal.cal.localhost" {
		t.Fatalf("router saw Host %q; routed hosts must keep theirs", seen.Host)
	}
	if seen.Header.Get("Origin") != "http://branch-a1b2c3.personal.cal.localhost" {
		t.Fatalf("Origin was rewritten to %q", seen.Header.Get("Origin"))
	}
	if loc := resp.Header.Get("Location"); loc != "http://branch-a1b2c3.personal.cal.localhost/next" {
		t.Fatalf("redirect rewritten to %q", loc)
	}
	// Hosts outside the route still use port-style routing.
	if got := get(t, p, "branch.work.cal.localhost", "/", nil).StatusCode; got != http.StatusNotFound {
		t.Fatalf("an unrouted multi-label host got %d", got)
	}
}
