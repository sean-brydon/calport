package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateKeepsTheSameFingerprintAcrossLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	// Certificates are regenerated on every load; only the key is pinned.
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatal("reloading the key changed the fingerprint")
	}
	if FingerprintOf(second.Certificate().Leaf) != second.Fingerprint() {
		t.Fatal("certificate does not carry the identity's key")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v, want 0600", info.Mode().Perm())
	}
}

func TestDistinctInstallationsHaveDistinctFingerprints(t *testing.T) {
	a, err := LoadOrCreate(filepath.Join(t.TempDir(), "a.pem"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(filepath.Join(t.TempDir(), "b.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("two generated keys share a fingerprint")
	}
}

func TestLoadRejectsAKeyFileThatIsNotAnEd25519Key(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("a corrupt key file was accepted")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "not a key" {
		t.Fatal("a corrupt key file was silently replaced with a new identity")
	}
}

func TestFingerprintTextRoundTrip(t *testing.T) {
	id, err := LoadOrCreate(filepath.Join(t.TempDir(), "identity.pem"))
	if err != nil {
		t.Fatal(err)
	}
	s := id.Fingerprint().String()
	if s != strings.ToLower(s) || len(s) != 52 {
		t.Fatalf("fingerprint %q is not 52 lowercase base32 characters", s)
	}
	var parsed Fingerprint
	if err := parsed.UnmarshalText([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if parsed != id.Fingerprint() {
		t.Fatal("fingerprint did not survive a text round trip")
	}
	for _, bad := range []string{"", "abc", s[:51], s + "a", strings.Repeat("1", 52)} {
		if _, err := ParseFingerprint(bad); err == nil {
			t.Fatalf("malformed fingerprint %q accepted", bad)
		}
	}
}
