package wire

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sean-brydon/calport/internal/identity"
	"github.com/sean-brydon/calport/internal/pairing"
	"github.com/sean-brydon/calport/internal/trust"
)

const dialTimeout = 15 * time.Second

// DialFunc opens the TCP connection calport's TLS runs over. The default is
// the system network; a box on another tailnet is reached through that
// tailnet's embedded node instead.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func defaultDial(dial DialFunc) DialFunc {
	if dial != nil {
		return dial
	}
	return (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext
}

// ErrUntrusted means the box answered but no longer trusts this laptop.
var ErrUntrusted = errors.New("box no longer trusts this laptop; pair again")

// Pair proves to the box that this laptop holds tok's code, and returns the
// name the box reports for itself. The code never leaves the laptop.
//
// Pairing uses its own HTTP/1.1 connection so the proof can be bound to that
// exact TLS session; a pooled HTTP/2 connection would hide which session a
// request travels on.
func Pair(ctx context.Context, id *identity.Identity, tok pairing.Token, clientName string) (string, error) {
	return PairVia(ctx, id, tok, clientName, nil)
}

// PairVia is Pair over a specific dialer, such as another tailnet.
func PairVia(ctx context.Context, id *identity.Identity, tok pairing.Token, clientName string, dial DialFunc) (string, error) {
	cfg := clientConfig(id, tok.Fingerprint)
	cfg.NextProtos = []string{"http/1.1"}
	conn, err := dialTLSVia(ctx, cfg, tok.Address, dial)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	cs := conn.ConnectionState()
	exporter, err := cs.ExportKeyingMaterial(pairing.ExporterLabel, nil, pairing.ExporterSize)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(pairRequest{Name: clientName, Proof: pairing.Proof(tok.Code, exporter, id.Fingerprint())})
	resp, err := exchange(ctx, conn, tok.Address, "/v1/pair", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var out nameResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
			return "", fmt.Errorf("reading reply: %w", err)
		}
		return out.Name, nil
	case http.StatusTooManyRequests:
		return "", errors.New(errTooManyPairings)
	default:
		return "", errors.New("box refused pairing: the link may be expired, already used, or for another box; run `calportd pair` for a new one")
	}
}

func exchange(ctx context.Context, conn *tls.Conn, address, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(conn), req)
}

func dialTLS(ctx context.Context, cfg *tls.Config, address string) (*tls.Conn, error) {
	return dialTLSVia(ctx, cfg, address, nil)
}

func dialTLSVia(ctx context.Context, cfg *tls.Config, address string, dial DialFunc) (*tls.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	raw, err := defaultDial(dial)(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(raw, cfg)
	tc.SetDeadline(time.Now().Add(dialTimeout))
	if err := tc.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}

// Client talks to one paired box over a single pooled HTTP/2 connection, which
// carries every ping and stream. Health pings detect a connection that died
// silently, and Reset abandons it immediately when the caller knows better.
type Client struct {
	id        *identity.Identity
	box       trust.Peer
	dial      DialFunc
	mu        sync.Mutex
	transport *http.Transport
}

func NewClient(id *identity.Identity, box trust.Peer) *Client {
	return NewClientVia(id, box, nil)
}

// NewClientVia is NewClient over a specific dialer, such as another tailnet.
func NewClientVia(id *identity.Identity, box trust.Peer, dial DialFunc) *Client {
	c := &Client{id: id, box: box, dial: dial}
	c.transport = c.newTransport()
	return c
}

func (c *Client) newTransport() *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	return &http.Transport{
		TLSClientConfig:     clientConfig(c.id, c.box.Fingerprint),
		Protocols:           protocols,
		DialContext:         defaultDial(c.dial),
		TLSHandshakeTimeout: dialTimeout,
		// Some calls wait on slow tools (Orca fetches before it creates a
		// worktree); the pings below are what detect a dead connection.
		ResponseHeaderTimeout: 3 * time.Minute,
		IdleConnTimeout:       5 * time.Minute,
		HTTP2:                 &http.HTTP2Config{SendPingTimeout: 15 * time.Second, PingTimeout: 10 * time.Second},
	}
}

func (c *Client) current() *http.Transport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transport
}

func (c *Client) Box() trust.Peer { return c.box }

// Reset makes the next request dial a fresh connection. After sleep or a
// network change the pooled connection may be dead yet still carry streams,
// so closing idle connections alone would leave new streams queued behind it.
func (c *Client) Reset() {
	c.mu.Lock()
	old := c.transport
	c.transport = c.newTransport()
	c.mu.Unlock()
	old.CloseIdleConnections()
}

// Do sends an authenticated request to the box.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	return c.DoWithHeader(ctx, method, path, body, nil)
}

// DoWithHeader is Do with extra request headers, such as the origin a tool
// attributes its request to.
func (c *Client) DoWithHeader(ctx context.Context, method, path string, body io.Reader, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "https://"+c.box.Address+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.current().RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrUntrusted
	}
	return resp, nil
}

// Ping checks that the box is reachable and still trusts this laptop, and
// returns the name it reports.
func (c *Client) Ping(ctx context.Context) (string, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/v1/ping", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", responseError(resp)
	}
	var out nameResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		return "", err
	}
	return out.Name, nil
}

// DialPort opens a stream to a port on the box's loopback. ctx bounds only the
// setup; the stream lives until it is closed.
func (c *Client) DialPort(ctx context.Context, port int) (net.Conn, error) {
	return c.OpenStream(ctx, "/v1/tcp?port="+strconv.Itoa(port), net.JoinHostPort(c.box.Name, strconv.Itoa(port)))
}

// OpenStream starts a two-way stream with a box route that reads the request
// body while writing its response, such as a port or a terminal.
func (c *Client) OpenStream(ctx context.Context, path, label string) (net.Conn, error) {
	streamCtx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	pr, pw := io.Pipe()
	resp, err := c.Do(streamCtx, http.MethodPost, path, pr)
	stop()
	if err == nil && resp.StatusCode != http.StatusOK {
		err = responseError(resp)
		resp.Body.Close()
	}
	if err != nil {
		pw.Close()
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return &streamConn{
		body:   resp.Body,
		pw:     pw,
		cancel: cancel,
		remote: streamAddr(label),
	}, nil
}

func responseError(resp *http.Response) error {
	var e errorResponse
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	return fmt.Errorf("box replied %s", resp.Status)
}
