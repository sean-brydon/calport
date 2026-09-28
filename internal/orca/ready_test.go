package orca

import (
	"encoding/json"
	"strings"
	"testing"
)

// record builds a ready log line. Fields are overridden by the caller to make
// one field invalid at a time.
func record(t *testing.T, edit func(m map[string]any)) string {
	t.Helper()
	m := map[string]any{
		"type":          "orca_server_ready",
		"schemaVersion": 1,
		"runtimeId":     "runtime-example",
		"boundEndpoint": "ws://127.0.0.1:41001",
		"pairing": map[string]any{
			"available": true,
			"url":       "https://orca.example/pair/SECRET-TOKEN",
			"endpoint":  "ws://127.0.0.1:16769",
		},
		"advertisedEndpoint": "ws://127.0.0.1:16769",
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseReadyAcceptsAValidRecord(t *testing.T) {
	got, err := ParseReady([]byte(record(t, func(map[string]any) {})), 16769)
	if err != nil {
		t.Fatalf("ParseReady() = %v; want the record accepted", err)
	}
	if got.RuntimeID != "runtime-example" || got.LocalPort != 16769 || got.RemotePort != 41001 {
		t.Fatalf("ParseReady() = %+v; want runtime-example, 16769, 41001", got)
	}
}

func TestParseReadyKeepsTheLastRecord(t *testing.T) {
	stale := record(t, func(m map[string]any) { m["runtimeId"] = "runtime-stale" })
	current := record(t, func(m map[string]any) { m["runtimeId"] = "runtime-current" })
	log := "noise that is not json\n" + stale + "\n" + current + "\n"

	got, err := ParseReady([]byte(log), 16769)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeID != "runtime-current" {
		t.Fatalf("RuntimeID = %q; want the last record's runtime", got.RuntimeID)
	}
}

func TestParseReadyRejectsUnusableRecords(t *testing.T) {
	// wantErr pins each case to the specific check that must reject it, not
	// merely to "some error" — otherwise a validation-ordering regression
	// (e.g. a case starting to fail on the wrong check) would stay green.
	type rejection struct {
		edit    func(m map[string]any)
		wantErr string
	}
	cases := map[string]rejection{
		"wrong schema":        {func(m map[string]any) { m["schemaVersion"] = 2 }, "schema version"},
		"no runtime id":       {func(m map[string]any) { m["runtimeId"] = "" }, "reported no runtime id"},
		"pairing unavailable": {func(m map[string]any) { m["pairing"].(map[string]any)["available"] = false }, "not offering to pair"},
		"no pairing url":      {func(m map[string]any) { m["pairing"].(map[string]any)["url"] = "" }, "not offering to pair"},
		"pairing endpoint differs": {
			func(m map[string]any) { m["pairing"].(map[string]any)["endpoint"] = "ws://127.0.0.1:16770" },
			"different endpoint than it advertises",
		},
		"advertised not loopback": {func(m map[string]any) { m["advertisedEndpoint"] = "ws://example.com:16769" }, "not a loopback address"},
		"advertised has userinfo": {func(m map[string]any) { m["advertisedEndpoint"] = "ws://secret@127.0.0.1:16769" }, "carries userinfo"},
		"advertised has path":     {func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:16769/path" }, "carries a path"},
		"advertised has query":    {func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:16769?a=b" }, "carries a query or fragment"},
		"advertised has fragment": {func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:16769#f" }, "carries a query or fragment"},
		"advertised not ws": {
			func(m map[string]any) { m["advertisedEndpoint"] = "http://127.0.0.1:16769" },
			"advertised endpoint is unusable: its scheme is not ws",
		},
		"advertised port zero": {
			func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:0" },
			"advertised endpoint is unusable: its port is not between 1 and 65535",
		},
		"advertised port too big": {
			func(m map[string]any) { m["advertisedEndpoint"] = "ws://127.0.0.1:70000" },
			"advertised endpoint is unusable: its port is not between 1 and 65535",
		},
		"bound not ws": {
			func(m map[string]any) { m["boundEndpoint"] = "http://127.0.0.1:41001" },
			"bound endpoint is unusable: its scheme is not ws",
		},
		"bound port too big": {
			func(m map[string]any) { m["boundEndpoint"] = "ws://0.0.0.0:70000" },
			"bound endpoint is unusable: its port is not between 1 and 65535",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			line := record(t, func(m map[string]any) {
				tc.edit(m)
				// A changed advertised endpoint must stay consistent with
				// pairing.endpoint, so each case fails for its own reason.
				if name != "pairing endpoint differs" {
					m["pairing"].(map[string]any)["endpoint"] = m["advertisedEndpoint"]
				}
			})
			_, err := ParseReady([]byte(line), 16769)
			if err == nil {
				t.Fatalf("ParseReady accepted a record with %s", name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseReady error = %q; want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseReadyAcceptsARuntimeBoundOnEveryAddress(t *testing.T) {
	// orca serve has no flag to choose a bind address and listens on 0.0.0.0,
	// so a record reporting that is normal, not suspect. calport reaches the
	// port over the box's loopback regardless.
	line := record(t, func(m map[string]any) { m["boundEndpoint"] = "ws://0.0.0.0:6768" })
	got, err := ParseReady([]byte(line), 16769)
	if err != nil {
		t.Fatalf("ParseReady() = %v; want a 0.0.0.0 bound endpoint accepted", err)
	}
	if got.RemotePort != 6768 {
		t.Fatalf("RemotePort = %d; want 6768", got.RemotePort)
	}
}

func TestParseReadyRejectsAPortItDidNotAskFor(t *testing.T) {
	line := record(t, func(m map[string]any) {
		m["advertisedEndpoint"] = "ws://127.0.0.1:16770"
		m["pairing"].(map[string]any)["endpoint"] = "ws://127.0.0.1:16770"
	})
	if _, err := ParseReady([]byte(line), 16769); err == nil {
		t.Fatal("ParseReady accepted a record advertising a port other than the pinned one")
	}
}

func TestParseReadyErrorsNeverCarryThePairingURL(t *testing.T) {
	const sentinel = "SECRET-TOKEN"
	for _, line := range []string{
		record(t, func(m map[string]any) { m["schemaVersion"] = 2 }),
		record(t, func(m map[string]any) { m["advertisedEndpoint"] = "ws://example.com:16769" }),
		record(t, func(m map[string]any) { m["boundEndpoint"] = "ws://0.0.0.0:70000" }),
	} {
		_, err := ParseReady([]byte(line), 16769)
		if err == nil {
			t.Fatal("want an error")
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error leaks the pairing credential: %v", err)
		}
	}
}
