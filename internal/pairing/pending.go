package pairing

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/sean-brydon/calport/internal/statefile"
)

// Pending stores the codes a box has issued and not yet seen used. It is shared
// by `calportd pair`, which issues codes, and `calportd serve`, which consumes
// them, so every change happens under the file lock.
type Pending struct{ path string }

func NewPending(path string) *Pending { return &Pending{path: path} }

type pendingCode struct {
	Code    Code      `json:"code"`
	Expires time.Time `json:"expires"`
}

func (c Code) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

func (c *Code) UnmarshalText(b []byte) error {
	parsed, err := parseCode(string(b))
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

func (p *Pending) Issue(ttl time.Duration, now time.Time) (Code, error) {
	var code Code
	if _, err := rand.Read(code[:]); err != nil {
		return code, err
	}
	err := p.update(now, func(codes []pendingCode) []pendingCode {
		return append(codes, pendingCode{Code: code, Expires: now.Add(ttl)})
	})
	return code, err
}

// Consume removes and reports the first unexpired code accepted by match. A
// code can therefore succeed at most once, even across concurrent pairings.
func (p *Pending) Consume(now time.Time, match func(Code) bool) (bool, error) {
	matched := false
	err := p.update(now, func(codes []pendingCode) []pendingCode {
		for i, c := range codes {
			if match(c.Code) {
				matched = true
				return append(codes[:i], codes[i+1:]...)
			}
		}
		return codes
	})
	return matched, err
}

func (p *Pending) update(now time.Time, change func([]pendingCode) []pendingCode) error {
	unlock, err := statefile.Lock(p.path)
	if err != nil {
		return err
	}
	defer unlock()
	var codes []pendingCode
	b, err := os.ReadFile(p.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &codes); err != nil {
			return errors.New("pending pairing codes are unreadable; remove " + p.path + " and issue a new code")
		}
	case !os.IsNotExist(err):
		return err
	}
	live := codes[:0]
	for _, c := range codes {
		if now.Before(c.Expires) {
			live = append(live, c)
		}
	}
	out, err := json.Marshal(change(live))
	if err != nil {
		return err
	}
	return statefile.Write(p.path, out)
}
