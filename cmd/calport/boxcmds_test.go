package main

import (
	"reflect"
	"testing"
)

func TestSplitBox(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		words int
		box   string
		rest  []string
	}{
		{[]string{"ports", "devl"}, 1, "devl", []string{"ports"}},
		{[]string{"ports", "--json", "devl"}, 1, "devl", []string{"ports", "--json"}},
		{[]string{"location", "add", "devl/cal", "~/work/cal"}, 2, "devl", []string{"location", "add", "cal", "~/work/cal"}},
		{[]string{"worktree", "new", "devl/cal/billing", "--provider", "orca"}, 2, "devl", []string{"worktree", "new", "cal/billing", "--provider", "orca"}},
		{[]string{"session", "new", "devl/cal", "--", "claude", "--resume"}, 2, "devl", []string{"session", "new", "cal", "--", "claude", "--resume"}},
		{[]string{"share", "devl", "3000"}, 1, "devl", []string{"share", "3000"}},
		{[]string{"worktree", "new", "--base", "main", "devl/cal/x"}, 2, "devl", []string{"worktree", "new", "--base", "main", "cal/x"}},
		{[]string{"session", "kill", "devl/cal-claude-1"}, 2, "devl", []string{"session", "kill", "cal-claude-1"}},
	} {
		box, rest, err := splitBox(tc.args, tc.words)
		if err != nil || box != tc.box || !reflect.DeepEqual(rest, tc.rest) {
			t.Errorf("splitBox(%v) = %q %v %v; want %q %v", tc.args, box, rest, err, tc.box, tc.rest)
		}
	}
	if _, _, err := splitBox([]string{"ports", "--json"}, 1); err == nil {
		t.Error("a command without a box was accepted")
	}
}

func TestBoxPath(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/alex/work/cal": "~/work/cal",
		"/Users/alex":          "~",
		"/Users/seanother/x":   "/Users/seanother/x",
		"/srv/app":             "/srv/app",
		"~/work/cal":           "~/work/cal",
	} {
		if got := boxPath(in, "/Users/alex"); got != want {
			t.Errorf("boxPath(%q) = %q, want %q", in, got, want)
		}
	}
}
