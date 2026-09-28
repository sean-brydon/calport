// Package orca connects this laptop's Orca app to a runtime on a box. calportd
// runs the runtime as a managed unit; this package validates what the runtime
// advertises, pairs a local environment with it, and keeps the route.
package orca

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Ready is a runtime's ready record, after validation.
type Ready struct {
	RuntimeID  string
	Advertised string
	// PairingURL is a credential. It must not be logged, wrapped into an
	// error, published as an event, or printed.
	PairingURL string
	LocalPort  int
	RemotePort int
}

type readyRecord struct {
	Type       string `json:"type"`
	Schema     int    `json:"schemaVersion"`
	RuntimeID  string `json:"runtimeId"`
	Bound      string `json:"boundEndpoint"`
	Advertised string `json:"advertisedEndpoint"`
	Pairing    struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Endpoint  string `json:"endpoint"`
	} `json:"pairing"`
}

// ParseReady returns the last usable record in a runtime's log. A restarted
// runtime appends, so the last record describes the runtime running now.
//
// The record is validated as untrusted input even though it reaches us over an
// authenticated channel: its advertised endpoint decides what the local Orca
// app connects to, so a misconfigured or compromised box must not be able to
// point it somewhere else. Errors name the check that failed and never the
// value, because the record carries a credential.
func ParseReady(log []byte, pinnedPort int) (Ready, error) {
	var last readyRecord
	found := false
	for _, line := range strings.Split(string(log), "\n") {
		var r readyRecord
		if json.Unmarshal([]byte(line), &r) == nil && r.Type == "orca_server_ready" {
			last, found = r, true
		}
	}
	if !found {
		return Ready{}, fmt.Errorf("the runtime has not reported that it is ready")
	}
	if last.Schema != 1 {
		return Ready{}, fmt.Errorf("the runtime reported schema version %d; this calport understands 1", last.Schema)
	}
	if last.RuntimeID == "" {
		return Ready{}, fmt.Errorf("the runtime reported no runtime id")
	}
	if !last.Pairing.Available || last.Pairing.URL == "" {
		return Ready{}, fmt.Errorf("the runtime is not offering to pair")
	}
	if last.Pairing.Endpoint != last.Advertised {
		return Ready{}, fmt.Errorf("the runtime pairs on a different endpoint than it advertises")
	}
	local, err := advertisedPort(last.Advertised)
	if err != nil {
		return Ready{}, fmt.Errorf("the runtime's advertised endpoint is unusable: %w", err)
	}
	if local != pinnedPort {
		return Ready{}, fmt.Errorf("the runtime advertises port %d; calport asked it to advertise %d", local, pinnedPort)
	}
	remote, err := boundPort(last.Bound)
	if err != nil {
		return Ready{}, fmt.Errorf("the runtime's bound endpoint is unusable: %w", err)
	}
	return Ready{
		RuntimeID:  last.RuntimeID,
		Advertised: last.Advertised,
		PairingURL: last.Pairing.URL,
		LocalPort:  local,
		RemotePort: remote,
	}, nil
}

// advertisedPort accepts only ws:// on a loopback address with nothing but a
// port. This is the endpoint the local Orca app will connect to, so it is the
// strict one: userinfo would smuggle a credential into a URL that reads as
// local, and a query, fragment or path would change what the client requests.
func advertisedPort(endpoint string) (int, error) {
	u, err := wsURL(endpoint)
	if err != nil {
		return 0, err
	}
	if u.User != nil {
		return 0, fmt.Errorf("it carries userinfo")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return 0, fmt.Errorf("it carries a query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return 0, fmt.Errorf("it carries a path")
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1":
	default:
		return 0, fmt.Errorf("its host is not a loopback address")
	}
	return portOf(u)
}

// boundPort reads the port the runtime listens on. Its host is not checked:
// orca serve offers no way to choose a bind address and listens on every one,
// so requiring loopback here would reject every real record. calport reaches
// the port through calportd over the box's own loopback either way.
func boundPort(endpoint string) (int, error) {
	u, err := wsURL(endpoint)
	if err != nil {
		return 0, err
	}
	return portOf(u)
}

func wsURL(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("it is not a URL")
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("its scheme is not ws")
	}
	return u, nil
}

func portOf(u *url.URL) (int, error) {
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("its port is not between 1 and 65535")
	}
	return port, nil
}
