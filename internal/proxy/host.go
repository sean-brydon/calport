// Package proxy serves box services at http://<service>.<box>.localhost on
// the laptop, with no DNS or /etc/hosts changes: every OS resolves
// *.localhost to loopback on its own.
package proxy

import (
	"net"
	"strconv"
	"strings"
)

// Target is what a request host names: a service label on a box. The label
// is a port number ("3000") or a service name ("web").
type Target struct {
	Box   string
	Label string
}

// Port returns the label as a port when it is one.
func (t Target) Port() (int, bool) {
	p, err := strconv.Atoi(t.Label)
	if err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != t.Label {
		return 0, false
	}
	return p, true
}

// ParseHost reads "<label>.<box>.localhost[:port]". Anything else — including
// hosts an attacker's page might send through DNS rebinding — is not a route.
func ParseHost(host string) (Target, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	rest, ok := strings.CutSuffix(host, ".localhost")
	if !ok {
		return Target{}, false
	}
	label, box, ok := strings.Cut(rest, ".")
	if !ok || label == "" || box == "" || strings.Contains(box, ".") {
		return Target{}, false
	}
	return Target{Box: box, Label: label}, true
}

// IsIndexHost reports whether host is plain localhost, where the proxy lists
// what it can reach.
func IsIndexHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// ValidRoutePattern accepts "*.name.localhost" or an exact "name.localhost".
// Routes stay under .localhost, which only ever resolves to this machine and
// which the proxy's DNS-rebinding defence already relies on.
func ValidRoutePattern(pattern string) bool {
	rest := strings.TrimPrefix(pattern, "*.")
	if !strings.HasSuffix(rest, ".localhost") || strings.ContainsAny(rest, "*/: ") {
		return false
	}
	for _, label := range strings.Split(rest, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
	}
	return true
}

// MatchRoute reports whether host falls under pattern.
func MatchRoute(pattern, host string) bool {
	host = hostOnly(host)
	if suffix, ok := strings.CutPrefix(pattern, "*"); ok {
		return strings.HasSuffix(host, suffix) && len(host) > len(suffix)
	}
	return host == pattern
}

// localhostLabels splits "a.b.c.localhost[:port]" into ["a", "b", "c"].
func localhostLabels(host string) ([]string, bool) {
	rest, ok := strings.CutSuffix(hostOnly(host), ".localhost")
	if !ok || rest == "" {
		return nil, false
	}
	labels := strings.Split(rest, ".")
	for _, l := range labels {
		if l == "" {
			return nil, false
		}
	}
	return labels, true
}
