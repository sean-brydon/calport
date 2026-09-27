package boxcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sean-brydon/calport/internal/box"
)

// recorder is a fake box that records each request and replies with body.
type recorder struct {
	method, path, origin string
	body                 map[string]any
	reply                string
}

func (r *recorder) DoWithHeader(_ context.Context, method, path string, body io.Reader, h http.Header) (*http.Response, error) {
	r.method, r.path, r.origin = method, path, h.Get(box.OriginHeader)
	r.body = nil
	if body != nil {
		json.NewDecoder(body).Decode(&r.body)
	}
	rec := httptest.NewRecorder()
	rec.WriteString(r.reply)
	return rec.Result(), nil
}

func run(t *testing.T, reply string, args ...string) (*recorder, string) {
	t.Helper()
	r := &recorder{reply: reply}
	var out bytes.Buffer
	if err := Run(context.Background(), &box.Client{Doer: r}, args, &out); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return r, out.String()
}

func TestFlagsMayFollowTheReference(t *testing.T) {
	r, _ := run(t, `{"name":"billing","path":"/w/cal-billing","branch":"alex/billing"}`,
		"worktree", "new", "cal/billing", "--base", "main", "--provider", "orca", "--agent", "claude", "--branch", "alex/billing")
	if r.method != "POST" || r.path != "/v1/locations/cal/worktrees" {
		t.Fatalf("request %s %s", r.method, r.path)
	}
	for k, want := range map[string]string{"name": "billing", "base": "main", "provider": "orca", "agent": "claude", "branch": "alex/billing"} {
		if r.body[k] != want {
			t.Errorf("body[%s] = %v, want %s", k, r.body[k], want)
		}
	}
}

func TestSessionNewPassesTheCommandAfterDoubleDash(t *testing.T) {
	r, _ := run(t, `{"name":"s","dir":"/w"}`, "session", "new", "cal/billing", "--name", "fix", "--", "claude", "--resume", "--model", "x")
	if r.body["location"] != "cal/billing" || r.body["name"] != "fix" || r.body["command"] != "claude --resume --model x" {
		t.Fatalf("session body = %v", r.body)
	}
}

func TestLocationAddAndRemoveWorktree(t *testing.T) {
	r, out := run(t, `{"name":"cal","path":"/home/alex/work/cal","repo":true,"worktrees":[{"name":"cal","main":true}]}`, "location", "add", "cal", "~/work/cal")
	if r.body["name"] != "cal" || r.body["path"] != "~/work/cal" || !strings.Contains(out, "git repository") {
		t.Fatalf("location add: %v %q", r.body, out)
	}
	r, _ = run(t, `{}`, "worktree", "rm", "cal/billing", "--force")
	if r.method != "DELETE" || r.path != "/v1/locations/cal/worktrees/billing?force=1" {
		t.Fatalf("worktree rm: %s %s", r.method, r.path)
	}
}

func TestEmitCarriesDataAndOrigin(t *testing.T) {
	r, _ := run(t, `{"ok":true}`, "emit", "agent.finished", "path=/w/cal", "--origin", "cursor", "status=done")
	if r.body["type"] != "agent.finished" || r.origin != "cursor" {
		t.Fatalf("emit: %v origin %q", r.body, r.origin)
	}
	data, _ := r.body["data"].(map[string]any)
	if data["path"] != "/w/cal" || data["status"] != "done" {
		t.Fatalf("emit data = %v", data)
	}
}

func TestShareUsesThePort(t *testing.T) {
	r, out := run(t, `{"id":"ab12","port":3000,"url":"https://x.trycloudflare.com"}`, "share", "3000")
	if r.body["port"] != float64(3000) || !strings.Contains(out, "https://x.trycloudflare.com") || !strings.Contains(out, "unshare ab12") {
		t.Fatalf("share: %v %q", r.body, out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"worktree", "new", "no-slash"},
		{"location", "add", "only-name"},
		{"share", "not-a-port"},
		{"emit"},
		{"nope"},
	} {
		if err := Run(context.Background(), &box.Client{Doer: &recorder{reply: "{}"}}, args, io.Discard); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
