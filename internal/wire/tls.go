// Package wire is the connection between a laptop and a box: TLS 1.3 with both
// sides pinned to keys exchanged during pairing, and the requests carried on it.
package wire

import (
	"crypto/tls"
	"errors"

	"github.com/sean-brydon/calport/internal/identity"
)

var errPinMismatch = errors.New("box presented a key that does not match its pairing; refusing to connect")

// serverConfig asks every client for a certificate but authorizes nothing
// itself: an unknown key may only attempt pairing, decided per request.
func serverConfig(id *identity.Identity) *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{id.Certificate()},
		ClientAuth:             tls.RequireAnyClientCert,
		SessionTicketsDisabled: true,
	}
}

// clientConfig trusts exactly one key. Certificate-authority verification is
// replaced, not skipped: VerifyConnection runs on every handshake and the pin
// is the only thing that can satisfy it.
func clientConfig(id *identity.Identity, pin identity.Fingerprint) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{id.Certificate()},
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || identity.FingerprintOf(cs.PeerCertificates[0]) != pin {
				return errPinMismatch
			}
			return nil
		},
	}
}
