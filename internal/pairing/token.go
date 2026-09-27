// Package pairing turns a one-time code printed on a box into mutual trust
// between that box and a laptop. See docs/design.md for the protocol.
package pairing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sean-brydon/calport/internal/identity"
)

const (
	Scheme = "calport"
	// ExporterLabel derives the TLS keying material a proof is bound to.
	ExporterLabel = "EXPORTER-calport-pair-v1"
	ExporterSize  = 32
	proofContext  = "calport pair v1"
)

// Code is a single-use pairing secret. It is never sent over the wire; the
// laptop proves possession of it instead.
type Code [32]byte

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func (c Code) String() string { return strings.ToLower(codeEncoding.EncodeToString(c[:])) }

func parseCode(s string) (Code, error) {
	var c Code
	b, err := codeEncoding.DecodeString(strings.ToUpper(s))
	if err != nil || len(b) != len(c) {
		return c, errors.New("malformed code")
	}
	copy(c[:], b)
	return c, nil
}

// Token is everything a laptop needs to pair: where the box is, which key it
// must present, and the code that proves the laptop was invited.
type Token struct {
	Address     string
	Fingerprint identity.Fingerprint
	Code        Code
}

var errMalformed = errors.New("malformed pairing link; copy it again from `calportd pair`")

func (t Token) String() string {
	q := url.Values{}
	q.Set("fp", t.Fingerprint.String())
	q.Set("code", t.Code.String())
	return (&url.URL{Scheme: Scheme, Host: t.Address, RawQuery: q.Encode()}).String()
}

func ParseToken(s string) (Token, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != Scheme || u.User != nil || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return Token{}, errMalformed
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return Token{}, errMalformed
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Token{}, errMalformed
	}
	q := u.Query()
	fp, err := identity.ParseFingerprint(q.Get("fp"))
	if err != nil {
		return Token{}, errMalformed
	}
	code, err := parseCode(q.Get("code"))
	if err != nil {
		return Token{}, errMalformed
	}
	return Token{Address: net.JoinHostPort(host, port), Fingerprint: fp, Code: code}, nil
}

// Proof binds possession of the code to one TLS session (through its exported
// keying material) and to the client key being pinned, so it cannot be replayed
// on another connection or used to pin a different key.
func Proof(code Code, exporter []byte, client identity.Fingerprint) []byte {
	m := hmac.New(sha256.New, code[:])
	m.Write([]byte(proofContext))
	m.Write(exporter)
	m.Write(client[:])
	return m.Sum(nil)
}
