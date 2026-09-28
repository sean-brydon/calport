package box

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/sean-brydon/calport/internal/doctor"
	"github.com/sean-brydon/calport/internal/events"
)

// Doer sends a request to a box: a wire.Client from a laptop, or a Local
// client on the box itself.
type Doer interface {
	DoWithHeader(ctx context.Context, method, path string, body io.Reader, header http.Header) (*http.Response, error)
}

// Client calls a box's API. Origin names the tool the calls are made for; it
// defaults to CALPORT_ORIGIN, which hooks set for the commands they run.
type Client struct {
	Doer   Doer
	Origin string
}

func NewClient(d Doer) *Client {
	return &Client{Doer: d, Origin: os.Getenv("CALPORT_ORIGIN")}
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	header := http.Header{"Content-Type": {"application/json"}}
	if validOrigin.MatchString(c.Origin) {
		header.Set(OriginHeader, c.Origin)
	}
	resp, err := c.Doer.DoWithHeader(ctx, method, path, body, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("box replied %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Ports(ctx context.Context) (out []Port, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/ports", nil, &out)
}

func (c *Client) Locations(ctx context.Context) (out []Location, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/locations", nil, &out)
}

func (c *Client) AddLocation(ctx context.Context, name, path string) (out Location, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/locations", map[string]string{"name": name, "path": path}, &out)
}

func (c *Client) RemoveLocation(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodDelete, "/v1/locations/"+url.PathEscape(name), nil, nil)
}

func (c *Client) AddWorktree(ctx context.Context, location string, req WorktreeRequest) (out Worktree, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/locations/"+url.PathEscape(location)+"/worktrees", req, &out)
}

// RemoveWorktree removes a worktree, or starts archiving it: archive is the
// script the box runs first, removing the worktree only if it succeeds.
func (c *Client) RemoveWorktree(ctx context.Context, location, name string, force bool) (archive string, err error) {
	path := "/v1/locations/" + url.PathEscape(location) + "/worktrees/" + url.PathEscape(name)
	if force {
		path += "?force=1"
	}
	var out struct {
		Archive string `json:"archive"`
	}
	err = c.call(ctx, http.MethodDelete, path, nil, &out)
	return out.Archive, err
}

func (c *Client) Sessions(ctx context.Context) (out []Session, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/sessions", nil, &out)
}

func (c *Client) AddSession(ctx context.Context, name, location, command string) (out Session, err error) {
	req := map[string]string{"name": name, "location": location, "command": command}
	return out, c.call(ctx, http.MethodPost, "/v1/sessions", req, &out)
}

func (c *Client) Screen(ctx context.Context, name string, history int) (string, error) {
	var out struct{ Screen string }
	err := c.call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(name)+"/screen?history="+strconv.Itoa(history), nil, &out)
	return out.Screen, err
}

func (c *Client) KillSession(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(name), nil, nil)
}

func (c *Client) Shares(ctx context.Context) (out []Share, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/shares", nil, &out)
}

func (c *Client) AddShare(ctx context.Context, port int) (out Share, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/shares", map[string]int{"port": port}, &out)
}

func (c *Client) RemoveShare(ctx context.Context, id string) (out Share, err error) {
	return out, c.call(ctx, http.MethodDelete, "/v1/shares/"+url.PathEscape(id), nil, &out)
}

func (c *Client) SetScripts(ctx context.Context, location, setup, archive string) (out Location, err error) {
	return out, c.call(ctx, http.MethodPut, "/v1/locations/"+url.PathEscape(location)+"/scripts", map[string]string{"setup": setup, "archive": archive}, &out)
}

func (c *Client) ImportOrca(ctx context.Context) (out []Location, err error) {
	return out, c.call(ctx, http.MethodPost, "/v1/locations/import/orca", nil, &out)
}

func (c *Client) Services(ctx context.Context) (out []Service, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/services", nil, &out)
}

func (c *Client) OpenWorktree(ctx context.Context, location, worktree string, req OpenRequest) error {
	return c.call(ctx, http.MethodPost, "/v1/locations/"+url.PathEscape(location)+"/worktrees/"+url.PathEscape(worktree)+"/open", req, nil)
}

func (c *Client) Doctor(ctx context.Context) (out []doctor.Check, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/doctor", nil, &out)
}

func (c *Client) Info(ctx context.Context) (out Info, err error) {
	return out, c.call(ctx, http.MethodGet, "/v1/info", nil, &out)
}

// Upgrade uploads a daemon build; the box verifies it, swaps it in, and
// restarts into it.
func (c *Client) Upgrade(ctx context.Context, binary []byte) error {
	resp, err := c.Doer.DoWithHeader(ctx, http.MethodPost, "/v1/upgrade", bytes.NewReader(binary), http.Header{"Content-Type": {"application/octet-stream"}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("box replied %s", resp.Status)
	}
	return nil
}

func (c *Client) Emit(ctx context.Context, typ string, data map[string]any) error {
	return c.call(ctx, http.MethodPost, "/v1/events", map[string]any{"type": typ, "data": data}, nil)
}

// Events calls fn for each box event until ctx ends or the stream breaks.
func (c *Client) Events(ctx context.Context, fn func(events.Event)) error {
	resp, err := c.Doer.DoWithHeader(ctx, http.MethodGet, "/v1/events", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("box replied %s", resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var e events.Event
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 && json.Unmarshal(line, &e) == nil {
			fn(e)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

// Local reaches calportd on the box through its Unix socket.
type Local struct{ http *http.Client }

func NewLocal(socket string) *Local {
	return &Local{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

func (l *Local) DoWithHeader(ctx context.Context, method, path string, body io.Reader, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://calportd"+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	return l.http.Do(req)
}
