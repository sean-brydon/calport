package pairing

import (
	"bytes"
	"crypto/rand"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sean-brydon/calport/internal/identity"
)

func testToken(t *testing.T, address string) Token {
	t.Helper()
	var tok Token
	tok.Address = address
	rand.Read(tok.Fingerprint[:])
	rand.Read(tok.Code[:])
	return tok
}

func TestTokenRoundTrip(t *testing.T) {
	for _, address := range []string{"203.0.113.5:7443", "[2001:db8::1]:7443", "dev-alex.example:9000"} {
		tok := testToken(t, address)
		parsed, err := ParseToken("  " + tok.String() + "\n")
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		if parsed != tok {
			t.Fatalf("%s: round trip changed the token:\n got %+v\nwant %+v", address, parsed, tok)
		}
	}
}

func TestParseTokenRejectsMalformedLinks(t *testing.T) {
	good := testToken(t, "203.0.113.5:7443").String()
	fp := identity.Fingerprint{}.String()
	code := Code{}.String()
	for _, bad := range []string{
		"",
		"https://203.0.113.5:7443?code=" + code + "&fp=" + fp,
		"calport://203.0.113.5?code=" + code + "&fp=" + fp,
		"calport://203.0.113.5:0?code=" + code + "&fp=" + fp,
		"calport://203.0.113.5:70000?code=" + code + "&fp=" + fp,
		"calport://:7443?code=" + code + "&fp=" + fp,
		"calport://user@203.0.113.5:7443?code=" + code + "&fp=" + fp,
		"calport://203.0.113.5:7443/extra?code=" + code + "&fp=" + fp,
		"calport://203.0.113.5:7443?fp=" + fp,
		"calport://203.0.113.5:7443?code=" + code,
		"calport://203.0.113.5:7443?code=" + code[:10] + "&fp=" + fp,
		strings.Replace(good, "calport://", "calport:", 1),
	} {
		if _, err := ParseToken(bad); err == nil {
			t.Errorf("accepted malformed link %q", bad)
		}
	}
}

func TestProofIsBoundToSessionClientAndCode(t *testing.T) {
	var code, otherCode Code
	rand.Read(code[:])
	rand.Read(otherCode[:])
	exporter := bytes.Repeat([]byte{1}, ExporterSize)
	otherExporter := bytes.Repeat([]byte{2}, ExporterSize)
	var client, otherClient identity.Fingerprint
	rand.Read(client[:])
	rand.Read(otherClient[:])

	proof := Proof(code, exporter, client)
	if !bytes.Equal(proof, Proof(code, exporter, client)) {
		t.Fatal("proof is not deterministic")
	}
	for name, other := range map[string][]byte{
		"another session": Proof(code, otherExporter, client),
		"another client":  Proof(code, exporter, otherClient),
		"another code":    Proof(otherCode, exporter, client),
	} {
		if bytes.Equal(proof, other) {
			t.Fatalf("proof for %s matches the original", name)
		}
	}
}

func TestPendingCodeIsSingleUse(t *testing.T) {
	p := NewPending(filepath.Join(t.TempDir(), "pairing.json"))
	now := time.Now()
	code, err := p.Issue(10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	is := func(c Code) bool { return c == code }
	if ok, err := p.Consume(now, is); err != nil || !ok {
		t.Fatalf("first use: ok=%v err=%v", ok, err)
	}
	if ok, err := p.Consume(now, is); err != nil || ok {
		t.Fatalf("second use succeeded: ok=%v err=%v", ok, err)
	}
}

func TestPendingCodeExpires(t *testing.T) {
	p := NewPending(filepath.Join(t.TempDir(), "pairing.json"))
	now := time.Now()
	code, err := p.Issue(10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := p.Consume(now.Add(10*time.Minute), func(c Code) bool { return c == code }); err != nil || ok {
		t.Fatalf("expired code accepted: ok=%v err=%v", ok, err)
	}
}

func TestPendingCodeCannotBeConsumedTwiceConcurrently(t *testing.T) {
	p := NewPending(filepath.Join(t.TempDir(), "pairing.json"))
	now := time.Now()
	code, err := p.Issue(10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := p.Consume(now, func(c Code) bool { return c == code })
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("code consumed %d times, want exactly 1", wins.Load())
	}
}

func TestIssuingKeepsOtherLiveCodes(t *testing.T) {
	p := NewPending(filepath.Join(t.TempDir(), "pairing.json"))
	now := time.Now()
	first, _ := p.Issue(10*time.Minute, now)
	second, _ := p.Issue(10*time.Minute, now)
	for _, code := range []Code{first, second} {
		if ok, err := p.Consume(now, func(c Code) bool { return c == code }); err != nil || !ok {
			t.Fatalf("code issued alongside another was lost: ok=%v err=%v", ok, err)
		}
	}
}
