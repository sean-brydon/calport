package kit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func routerWith(t *testing.T, routes map[string]route) *Router {
	t.Helper()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "routes"), 0o700))
	for key, r := range routes {
		b, _ := json.Marshal(r)
		must(t, os.WriteFile(filepath.Join(dir, "routes", key+".json"), b, 0o600))
	}
	return NewRouter(dir)
}

func get(t *testing.T, h http.Handler, host, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", "http://"+host+path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestRouterSendsEachHostToItsWorktreeWithTheHostIntact(t *testing.T) {
	var seenHost string
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		io.WriteString(w, "fix-login")
	}))
	defer app.Close()
	u, _ := url.Parse(app.URL)
	port, _ := strconv.Atoi(u.Port())
	rt := routerWith(t, map[string]route{
		"aaaaaaaaaaaa": {Host: "fix-login-aaaaaa.devl.cal.localhost", Port: port, Active: true},
		"bbbbbbbbbbbb": {Host: "old-bbbbbb.devl.cal.localhost", Port: port, Active: false},
	})
	res := get(t, rt, "Fix-Login-aaaaaa.devl.cal.localhost:80", "/auth/login")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "fix-login" {
		t.Fatalf("got %d %q", res.StatusCode, body)
	}
	if seenHost != "Fix-Login-aaaaaa.devl.cal.localhost:80" {
		t.Fatalf("app saw Host %q; Next.js needs the one the browser used", seenHost)
	}
	if res := get(t, rt, "old-bbbbbb.devl.cal.localhost", "/"); res.StatusCode != 404 {
		t.Fatalf("a stopped worktree answered %d", res.StatusCode)
	}
	if res := get(t, rt, "nope.devl.cal.localhost", "/"); res.StatusCode != 404 {
		t.Fatalf("an unknown host answered %d", res.StatusCode)
	}
}

func TestRouterServesLogsEvenForStoppedWorktrees(t *testing.T) {
	rt := routerWith(t, map[string]route{"aaaaaaaaaaaa": {Host: "a.devl.cal.localhost", Port: 3100}})
	must(t, os.WriteFile(filepath.Join(rt.Dir, "aaaaaaaaaaaa-setup.log"), []byte("READY: http://a.devl.cal.localhost\n"), 0o600))
	res := get(t, rt, "a.devl.cal.localhost", "/__worktree/logs")
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("logs page: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	res = get(t, rt, "a.devl.cal.localhost", "/__worktree/logs/data")
	var data struct {
		Active bool   `json:"active"`
		Setup  string `json:"setup"`
	}
	json.NewDecoder(res.Body).Decode(&data)
	if data.Active || !strings.Contains(data.Setup, "READY") {
		t.Fatalf("logs data = %+v", data)
	}
	req := httptest.NewRequest("GET", "http://a.devl.cal.localhost/__worktree/logs/data", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("another site read the logs: %d", rec.Code)
	}
}

func TestRouterSendsStudioToItsSubdomain(t *testing.T) {
	rt := routerWith(t, map[string]route{"aaaaaaaaaaaa": {Host: "a.devl.cal.localhost", Port: 3100, Active: true}})
	res := get(t, rt, "a.devl.cal.localhost", "/__worktree/studio")
	if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != "http://studio.a.devl.cal.localhost/" {
		t.Fatalf("studio redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}
