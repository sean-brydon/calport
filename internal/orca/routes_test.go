package orca

import (
	"path/filepath"
	"testing"
)

func TestPortForAllocatesFromTheBaseAndKeepsIt(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "orca.json")}

	first, err := s.PortFor("devl")
	if err != nil || first != portBase {
		t.Fatalf("PortFor(devl) = %d, %v; want %d", first, err, portBase)
	}
	second, err := s.PortFor("omarchy")
	if err != nil || second != portBase+1 {
		t.Fatalf("PortFor(omarchy) = %d, %v; want %d", second, err, portBase+1)
	}
	again, err := s.PortFor("devl")
	if err != nil || again != first {
		t.Fatalf("PortFor(devl) = %d, %v; want the port it already had, %d", again, err, first)
	}
}

func TestRoutesRoundTrip(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "orca.json")}
	want := map[string]Route{"devl": {Environment: "env-1", Runtime: "runtime-1", LocalPort: 16769, RemotePort: 41001}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got["devl"] != want["devl"] {
		t.Fatalf("Read()[devl] = %+v; want %+v", got["devl"], want["devl"])
	}
}

func TestReadOnAMissingFileIsEmpty(t *testing.T) {
	got, err := Store{Path: filepath.Join(t.TempDir(), "orca.json")}.Read()
	if err != nil || len(got) != 0 {
		t.Fatalf("Read() = %+v, %v; want an empty set and no error", got, err)
	}
}
