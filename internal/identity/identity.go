// Package identity holds an installation's Ed25519 key. Peers trust the key
// itself, by fingerprint; certificates only carry it into TLS.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/sean-brydon/calport/internal/statefile"
)

// Fingerprint is the SHA-256 of a key's SubjectPublicKeyInfo.
type Fingerprint [sha256.Size]byte

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func (f Fingerprint) String() string { return strings.ToLower(encoding.EncodeToString(f[:])) }

// Short is enough of the fingerprint to recognise a peer in a listing.
func (f Fingerprint) Short() string { return f.String()[:12] }

func (f Fingerprint) MarshalText() ([]byte, error) { return []byte(f.String()), nil }

func (f *Fingerprint) UnmarshalText(b []byte) error {
	parsed, err := ParseFingerprint(string(b))
	if err != nil {
		return err
	}
	*f = parsed
	return nil
}

func ParseFingerprint(s string) (Fingerprint, error) {
	var f Fingerprint
	b, err := encoding.DecodeString(strings.ToUpper(s))
	if err != nil || len(b) != len(f) {
		return f, errors.New("malformed fingerprint")
	}
	copy(f[:], b)
	return f, nil
}

// FingerprintOf identifies the key a certificate carries, ignoring everything
// else in it.
func FingerprintOf(cert *x509.Certificate) Fingerprint {
	return sha256.Sum256(cert.RawSubjectPublicKeyInfo)
}

type Identity struct {
	cert        tls.Certificate
	fingerprint Fingerprint
}

func (i *Identity) Certificate() tls.Certificate { return i.cert }
func (i *Identity) Fingerprint() Fingerprint     { return i.fingerprint }

// LoadOrCreate reads the key at path, creating it on first use.
func LoadOrCreate(path string) (*Identity, error) {
	unlock, err := statefile.Lock(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	key, err := readKey(path)
	if os.IsNotExist(err) {
		key, err = createKey(path)
	}
	if err != nil {
		return nil, err
	}
	return fromKey(key)
}

func readKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PEM private key", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 key", path)
	}
	return key, nil
}

func createKey(path string) (ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := statefile.Write(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return key, nil
}

func fromKey(key ed25519.PrivateKey) (*Identity, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "calport"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Identity{
		cert:        tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf},
		fingerprint: FingerprintOf(leaf),
	}, nil
}
