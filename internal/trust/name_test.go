package trust

import (
	"strings"
	"testing"
)

func TestNameFromHostname(t *testing.T) {
	for in, want := range map[string]string{
		"Alexs-MacBook-Pro.local":      "Alexs-MacBook-Pro",
		"alex's laptop":                "alex-s-laptop",
		"--weird--":                    "weird",
		"ünïcode-box":                  "n-code-box",
		"":                             "laptop",
		"...":                          "laptop",
		strings.Repeat("a", 80):        strings.Repeat("a", 63),
		strings.Repeat("a", 62) + "-b": strings.Repeat("a", 62),
	} {
		if got := NameFromHostname(in, "laptop"); got != want {
			t.Errorf("NameFromHostname(%q) = %q, want %q", in, got, want)
		}
	}
}
