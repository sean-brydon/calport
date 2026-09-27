package main

import (
	"net"
	"testing"
)

func ips(s ...string) []net.IP {
	out := make([]net.IP, len(s))
	for i, v := range s {
		out[i] = net.ParseIP(v)
	}
	return out
}

func TestPickAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []net.IP
		want string
	}{
		{"public beats private and tailnet", ips("127.0.0.1", "10.0.0.5", "100.101.102.103", "203.0.113.5"), "203.0.113.5"},
		{"private when nothing is public", ips("127.0.0.1", "10.0.0.5", "100.101.102.103"), "10.0.0.5"},
		{"tailnet address counts as not public", ips("100.101.102.103"), "100.101.102.103"},
		{"link-local and loopback are skipped", ips("127.0.0.1", "169.254.1.1", "fe80::1"), "box.example"},
		{"IPv6 is skipped for now", ips("2001:db8::1"), "box.example"},
		{"IPv4-mapped IPv6 is treated as IPv4", ips("::ffff:203.0.113.9"), "203.0.113.9"},
		{"no addresses falls back to hostname", nil, "box.example"},
	} {
		if got := pickAddress(tc.ips, "box.example"); got != tc.want {
			t.Errorf("%s: pickAddress = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDefaultListenIsTheTailnetAddressOnly(t *testing.T) {
	got, err := defaultListen(ips("127.0.0.1", "203.0.113.5", "10.0.0.5", "100.101.102.103"))
	if err != nil || got != "100.101.102.103:7443" {
		t.Fatalf("defaultListen = %q, %v; want the tailnet address", got, err)
	}
	if got, err := defaultListen(ips("127.0.0.1", "203.0.113.5", "10.0.0.5")); err == nil {
		t.Fatalf("with no tailnet address, defaultListen chose %q instead of refusing", got)
	}
}

func TestAdvertiseUsesTheAddressActuallyListenedOn(t *testing.T) {
	all := ips("203.0.113.5", "100.101.102.103")
	for listening, want := range map[string]string{
		"100.101.102.103:7443": "100.101.102.103:7443",
		"0.0.0.0:7443":         "203.0.113.5:7443",
		"[::]:9000":            "203.0.113.5:9000",
		":7443":                "203.0.113.5:7443",
		"":                     "203.0.113.5:7443",
		"box.example:7443":     "box.example:7443",
	} {
		if got := advertise(listening, all, "box.example"); got != want {
			t.Errorf("advertise(%q) = %q, want %q", listening, got, want)
		}
	}
}
