package integrations

import (
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	for _, tc := range []struct {
		tool, hook, payload string
		typ                 string
		data                map[string]any
	}{
		{"claude", "Stop", `{"session_id":"s1","cwd":"/w/cal-billing","hook_event_name":"Stop","transcript_path":"/secret/transcript.jsonl"}`,
			"agent.finished", map[string]any{"path": "/w/cal-billing", "session_id": "s1", "agent": "claude"}},
		{"claude", "Notification", `{"session_id":"s1","cwd":"/w","message":"Claude needs permission to run rm -rf"}`,
			"agent.waiting", map[string]any{"path": "/w", "session_id": "s1", "agent": "claude"}},
		{"cursor", "stop", `{"conversation_id":"c1","status":"completed","workspace_roots":["/w/cal"]}`,
			"agent.finished", map[string]any{"path": "/w/cal", "conversation_id": "c1", "status": "completed", "agent": "cursor"}},
		{"codex", "notify", `{"type":"agent-turn-complete","turn-id":"t1","cwd":"/w","last-assistant-message":"secret plan"}`,
			"agent.finished", map[string]any{"path": "/w", "turn_id": "t1", "agent": "codex"}},
		{"orca", "worktree.archived", `{"cwd":"/w/x"}`,
			"worktree.archived", map[string]any{"path": "/w/x", "agent": "orca"}},
	} {
		e, ok := Translate(tc.tool, tc.hook, []byte(tc.payload))
		if !ok || e.Type != tc.typ || e.Origin != tc.tool {
			t.Errorf("%s %s: %+v ok=%v", tc.tool, tc.hook, e, ok)
			continue
		}
		if len(e.Data) != len(tc.data) {
			t.Errorf("%s %s: data %v, want %v", tc.tool, tc.hook, e.Data, tc.data)
		}
		for k, v := range tc.data {
			if e.Data[k] != v {
				t.Errorf("%s %s: data[%s] = %v, want %v", tc.tool, tc.hook, k, e.Data[k], v)
			}
		}
	}
}

// Prompts, messages, and transcripts can hold anything a user typed; they
// must never become part of an event.
func TestTranslateNeverCopiesContent(t *testing.T) {
	for _, tc := range [][3]string{
		{"claude", "Notification", `{"message":"SECRET-TEXT","transcript_path":"/SECRET-TEXT","cwd":"/w"}`},
		{"codex", "notify", `{"type":"agent-turn-complete","input-messages":["SECRET-TEXT"],"last-assistant-message":"SECRET-TEXT"}`},
		{"cursor", "stop", `{"prompt":"SECRET-TEXT","workspace_roots":["/w"]}`},
	} {
		e, _ := Translate(tc[0], tc[1], []byte(tc[2]))
		for k, v := range e.Data {
			if s, _ := v.(string); strings.Contains(s, "SECRET-TEXT") {
				t.Errorf("%s: content leaked into data[%s]", tc[0], k)
			}
		}
	}
}

func TestUninterestingHooksAreIgnored(t *testing.T) {
	for _, tc := range [][3]string{
		{"claude", "PreToolUse", `{}`},
		{"cursor", "beforeShellExecution", `{}`},
		{"codex", "notify", `{"type":"something-else"}`},
		{"orca", "not-an-event-type", `{}`},
		{"claude", "Stop", `not json`},
	} {
		if e, ok := Translate(tc[0], tc[1], []byte(tc[2])); ok && tc[2] != "not json" {
			t.Errorf("%s %s produced %+v", tc[0], tc[1], e)
		}
	}
	if Reply("cursor") != "{}" || Reply("claude") != "" {
		t.Error("wrong hook replies")
	}
}
