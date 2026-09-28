package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/sean-brydon/calport/internal/statefile"
)

// Forward keeps a local port on the laptop pointed at a port on a box. Pin is
// a stable key for a forward something else needs to find again after a
// restart, such as a tool's runtime tunnel; it is empty for ordinary forwards.
type Forward struct {
	ID     string `json:"id"`
	Box    string `json:"box"`
	Local  int    `json:"local"`
	Remote int    `json:"remote"`
	Pin    string `json:"pin,omitempty"`
}

var errUnknownForward = errors.New("no forward with that id")

// forwardStore persists forwards. Every change is a read-modify-write of the
// file under its lock, never a rewrite from memory: the in-memory set of
// running forwards being empty must not be able to erase the saved ones.
type forwardStore struct{ path string }

func (s forwardStore) list() ([]Forward, error) {
	unlock, err := statefile.Lock(s.path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.read()
}

func (s forwardStore) add(f Forward) (Forward, error) {
	var id [4]byte
	rand.Read(id[:])
	f.ID = hex.EncodeToString(id[:])
	err := s.update(func(all []Forward) ([]Forward, error) {
		for _, existing := range all {
			if existing.Local == f.Local {
				return nil, fmt.Errorf("local port %d is already forwarded to %s:%d", f.Local, existing.Box, existing.Remote)
			}
		}
		return append(all, f), nil
	})
	return f, err
}

func (s forwardStore) remove(id string) (Forward, error) {
	var removed Forward
	err := s.update(func(all []Forward) ([]Forward, error) {
		for i, f := range all {
			if f.ID == id {
				removed = f
				return append(all[:i], all[i+1:]...), nil
			}
		}
		return nil, errUnknownForward
	})
	return removed, err
}

func (s forwardStore) update(change func([]Forward) ([]Forward, error)) error {
	unlock, err := statefile.Lock(s.path)
	if err != nil {
		return err
	}
	defer unlock()
	all, err := s.read()
	if err != nil {
		return err
	}
	all, err = change(all)
	if err != nil {
		return err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Local < all[j].Local })
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return statefile.WriteWithBackup(s.path, append(b, '\n'))
}

func (s forwardStore) read() ([]Forward, error) {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Forward
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%s is unreadable (a backup may be at %s.bak): %w", s.path, s.path, err)
	}
	return all, nil
}
