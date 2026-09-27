package wire

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sean-brydon/calport/internal/identity"
	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/trust"
)

// Rejections are deliberately uninformative: the caller learns that it was
// refused, never whether a code existed, expired, or was already used.
const (
	errPairingRejected = "pairing rejected"
	errUnauthorized    = "unauthorized"
	errTooManyPairings = "too many pairing attempts; try again in a minute"
)

const (
	maxPairBody = 16 << 10
	// headerTimeout also bounds the TLS handshake, so a connection cannot hold
	// server resources indefinitely before authenticating.
	headerTimeout   = 10 * time.Second
	upstreamTimeout = 5 * time.Second
)

type Server struct {
	Identity *identity.Identity
	Clients  *trust.Store
	Pending  *pairing.Pending
	Name     string
	Log      *log.Logger
	Now      func() time.Time

	once      sync.Once
	mux       *http.ServeMux
	local     *http.ServeMux
	pairLimit *limiter
	streams   atomic.Int64
}

// ActiveStreams reports how many port streams are open right now.
func (s *Server) ActiveStreams() int64 { return s.streams.Load() }

func (s *Server) init() {
	s.once.Do(func() {
		s.pairLimit = newLimiter(10, 10)
		s.mux = http.NewServeMux()
		s.local = http.NewServeMux()
		s.mux.HandleFunc("POST /v1/pair", s.handlePair)
		s.mux.Handle("GET /v1/ping", s.authenticated(http.HandlerFunc(s.handlePing)))
		s.mux.Handle("POST /v1/tcp", s.authenticated(http.HandlerFunc(s.handleTCP)))
	})
}

// Handle mounts a handler that only paired laptops, and the box's own user
// through ServeLocal, can reach. The caller is available through PeerFrom.
func (s *Server) Handle(pattern string, h http.Handler) {
	s.init()
	s.mux.Handle(pattern, s.authenticated(h))
	s.local.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, LocalPeer)))
	}))
}

// LocalPeer is the caller on the box's own Unix socket.
var LocalPeer = trust.Peer{Name: "local"}

// ServeLocal serves the Handle'd routes on ln, a Unix socket that only the
// box's user can open. Tools running on the box (Orca and Herdr hooks, the
// calportd CLI) use it; file permissions are its authorization.
func (s *Server) ServeLocal(ctx context.Context, ln net.Listener) error {
	s.init()
	srv := &http.Server{Handler: s.local, ReadHeaderTimeout: headerTimeout}
	stop := context.AfterFunc(ctx, func() { srv.Close() })
	defer stop()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

// Serve accepts connections on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.init()
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	srv := &http.Server{
		Handler:           s.mux,
		TLSConfig:         serverConfig(s.Identity),
		Protocols:         protocols,
		ReadHeaderTimeout: headerTimeout,
		MaxHeaderBytes:    16 << 10,
		IdleTimeout:       10 * time.Minute,
		HTTP2:             &http.HTTP2Config{SendPingTimeout: 30 * time.Second, PingTimeout: 15 * time.Second},
		// An exposed port attracts scanners; their failed handshakes are noise.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	stop := context.AfterFunc(ctx, func() { srv.Close() })
	defer stop()
	err := srv.ServeTLS(ln, "", "")
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

type peerKey struct{}

// PeerFrom returns the paired laptop making a request to a Handle'd route.
func PeerFrom(ctx context.Context) trust.Peer {
	p, _ := ctx.Value(peerKey{}).(trust.Peer)
	return p
}

func (s *Server) authenticated(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fp, ok := clientFingerprint(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errUnauthorized)
			return
		}
		peer, ok := s.authorize(fp)
		if !ok {
			writeError(w, http.StatusUnauthorized, errUnauthorized)
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, peer)))
	})
}

func clientFingerprint(r *http.Request) (identity.Fingerprint, bool) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return identity.Fingerprint{}, false
	}
	return identity.FingerprintOf(r.TLS.PeerCertificates[0]), true
}

// authorize fails closed: a trust store that cannot be read authorizes nobody.
func (s *Server) authorize(peer identity.Fingerprint) (trust.Peer, bool) {
	p, ok, err := s.Clients.Trusted(peer)
	if err != nil {
		s.logf("trust store unreadable, refusing %s: %v", peer.Short(), err)
		return trust.Peer{}, false
	}
	return p, ok
}

type pairRequest struct {
	Name  string `json:"name"`
	Proof []byte `json:"proof"`
}

type nameResponse struct {
	Name string `json:"name"`
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !s.pairLimit.allow(s.now()) {
		writeError(w, http.StatusTooManyRequests, errTooManyPairings)
		return
	}
	peer, ok := clientFingerprint(r)
	if !ok {
		writeError(w, http.StatusForbidden, errPairingRejected)
		return
	}
	var req pairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPairBody)).Decode(&req); err != nil {
		writeError(w, http.StatusForbidden, errPairingRejected)
		return
	}
	exporter, err := r.TLS.ExportKeyingMaterial(pairing.ExporterLabel, nil, pairing.ExporterSize)
	if err != nil {
		writeError(w, http.StatusForbidden, errPairingRejected)
		return
	}
	matched, err := s.Pending.Consume(s.now(), func(code pairing.Code) bool {
		return hmac.Equal(req.Proof, pairing.Proof(code, exporter, peer))
	})
	if err != nil {
		s.logf("pairing store error: %v", err)
	}
	if err != nil || !matched {
		s.logf("pairing rejected for %s", peer.Short())
		writeError(w, http.StatusForbidden, errPairingRejected)
		return
	}
	name := req.Name
	if !trust.ValidName(name) {
		name = "client"
	}
	name, err = s.Clients.AddWithFreeName(trust.Peer{Name: name, Fingerprint: peer, PairedAt: s.now().UTC()})
	if err != nil {
		s.logf("pairing succeeded but pinning %s failed: %v", peer.Short(), err)
		writeError(w, http.StatusForbidden, errPairingRejected)
		return
	}
	s.logf("paired client %q (%s)", name, peer.Short())
	writeJSON(w, http.StatusOK, nameResponse{Name: s.Name})
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nameResponse{Name: s.Name})
}

// handleTCP bridges one stream to a port on the box's own loopback. Only
// loopback is reachable: a paired laptop gets the box's services, not a route
// into whatever network the box sits on.
func (s *Server) handleTCP(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}
	upstream, err := dialLoopback(r.Context(), port)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("nothing is listening on port %d on this box", port))
		return
	}
	defer upstream.Close()
	s.streams.Add(1)
	defer s.streams.Add(-1)
	// A reset stream must also release the upstream connection, even while
	// the upstream is silent and nothing is being copied.
	stop := context.AfterFunc(r.Context(), func() { upstream.Close() })
	defer stop()

	rc := http.NewResponseController(w)
	rc.EnableFullDuplex()
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(upstream, r.Body)
		if tc, ok := upstream.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()
	io.Copy(flushWriter{w: w, rc: rc}, upstream)
	upstream.Close()
	r.Body.Close()
	<-done
}

func dialLoopback(ctx context.Context, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: upstreamTimeout}
	var firstErr error
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// flushWriter pushes each chunk to the laptop as soon as it arrives, so
// interactive protocols are not held back by response buffering.
type flushWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (f flushWriter) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	if err == nil {
		err = f.rc.Flush()
	}
	return n, err
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
