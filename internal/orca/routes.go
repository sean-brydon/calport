package orca

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/sean-brydon/calport/internal/statefile"
)

// portBase is where pinned local ports start. A route's port is part of the
// pairing code the local Orca app stored, so it is allocated once and never
// recomputed: moving it means pairing again.
const portBase = 16768

// Route is what this laptop remembers about one box's runtime.
type Route struct {
	Environment string `json:"environment_id"`
	Runtime     string `json:"runtime_id"`
	LocalPort   int    `json:"local_port"`
	RemotePort  int    `json:"remote_port"`
}

// Store persists routes by box name.
type Store struct{ Path string }

func (s Store) Read() (map[string]Route, error) {
	routes := map[string]Route{}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return routes, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &routes); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.Path, err)
	}
	if routes == nil {
		routes = map[string]Route{}
	}
	return routes, nil
}

func (s Store) Save(routes map[string]Route) error {
	b, err := json.MarshalIndent(routes, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(s.Path, append(b, '\n'))
}

// PortFor returns the box's pinned local port, allocating and saving the
// lowest free one from portBase the first time.
func (s Store) PortFor(box string) (int, error) {
	unlock, err := statefile.Lock(s.Path)
	if err != nil {
		return 0, err
	}
	defer unlock()
	routes, err := s.Read()
	if err != nil {
		return 0, err
	}
	if r, ok := routes[box]; ok && r.LocalPort != 0 {
		return r.LocalPort, nil
	}
	taken := map[int]bool{}
	for _, r := range routes {
		taken[r.LocalPort] = true
	}
	port := portBase
	for taken[port] {
		port++
	}
	r := routes[box]
	r.LocalPort = port
	routes[box] = r
	if err := s.Save(routes); err != nil {
		return 0, err
	}
	return port, nil
}

// Forget drops a box's route, including its pinned port, and reports what was
// there. Re-pairing then starts from scratch: the port is reallocated, so a
// box pinned to a port this laptop cannot listen on is no longer stuck with
// it.
func (s Store) Forget(box string) (Route, bool, error) {
	unlock, err := statefile.Lock(s.Path)
	if err != nil {
		return Route{}, false, err
	}
	defer unlock()
	routes, err := s.Read()
	if err != nil {
		return Route{}, false, err
	}
	route, ok := routes[box]
	if !ok {
		return Route{}, false, nil
	}
	delete(routes, box)
	return route, true, s.Save(routes)
}
