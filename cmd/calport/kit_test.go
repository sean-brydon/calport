package main

import "testing"

func TestSetupMilestonesSkipToolNoise(t *testing.T) {
	for line, want := range map[string]bool{
		"Install dependencies: 39.8s":                         true,
		"Apply migrations: 28.4s":                             true,
		"READY: http://a-b.devl.cal.localhost":                true,
		"App startup + first page: 20.6s; total setup: 34.0s": true,
		"\x1b[94m➤\x1b[39m YN0000: └ Completed in 36s 686ms":  false,
		"YN0000: · Done with warnings in 39s 115ms":           false,
		"Logs: http://a-b.devl.cal.localhost/__worktree/logs": false,
	} {
		if got := setupMilestone(line); got != want {
			t.Errorf("setupMilestone(%q) = %v, want %v", line, got, want)
		}
	}
}
