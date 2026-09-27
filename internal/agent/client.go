package agent

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
	"time"
)

// Client talks to a running agent over its local socket.
type Client struct {
	socket string
	http   *http.Client
}

func NewClient(socket string) *Client {
	c := &Client{socket: socket}
	c.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dialSocket(ctx) },
	}}
	return c
}

func (c *Client) dialSocket(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", c.socket)
}

// Running reports whether an agent answers on the socket.
func (c *Client) Running(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err := c.Status(ctx)
	return err == nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	return s, c.call(ctx, http.MethodGet, "/v1/status", nil, &s)
}

// Refresh checks every box now and returns the result.
func (c *Client) Refresh(ctx context.Context) (Status, error) {
	var s Status
	return s, c.call(ctx, http.MethodPost, "/v1/refresh", nil, &s)
}

func (c *Client) AddForward(ctx context.Context, box string, local, remote int) (Forward, error) {
	var f Forward
	req := map[string]any{"box": box, "local": local, "remote": remote}
	return f, c.call(ctx, http.MethodPost, "/v1/forwards", req, &f)
}

func (c *Client) RemoveForward(ctx context.Context, id string) (Forward, error) {
	var f Forward
	return f, c.call(ctx, http.MethodDelete, "/v1/forwards/"+id, nil, &f)
}

func (c *Client) Stop(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/v1/stop", nil, nil)
}

// Events calls fn for each event until ctx is cancelled or the agent stops.
func (c *Client) Events(ctx context.Context, fn func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/v1/events", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(line, &e) == nil {
			fn(e)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return scanner.Err()
}

// Call sends a JSON request to path and decodes the reply into out. It is
// exported for agent features added outside this package.
func (c *Client) Call(ctx context.Context, method, path string, in, out any) error {
	return c.call(ctx, method, path, in, out)
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
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
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
		return fmt.Errorf("agent replied %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Dial returns a raw connection to addr through the agent's named network.
func (c *Client) Dial(ctx context.Context, networkName, addr string) (net.Conn, error) {
	conn, err := c.dialSocket(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest(http.MethodPost, "http://agent/v1/networks/"+networkName+"/dial?addr="+url.QueryEscape(addr), nil)
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer conn.Close()
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, fmt.Errorf("through network %s: %s", networkName, e.Error)
	}
	return &bufferedConn{Conn: conn, r: br}, nil
}

// bufferedConn keeps bytes the response reader already pulled off the socket.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// Login brings a network up, calling onURL with the sign-in link.
func (c *Client) Login(ctx context.Context, networkName string, onURL func(string)) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent/v1/networks/"+networkName+"/login", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var line map[string]any
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		if u, ok := line["auth_url"].(string); ok {
			onURL(u)
		}
		if e, ok := line["error"].(string); ok {
			return nil, errors.New(e)
		}
		if n, ok := line["network"].(map[string]any); ok {
			return n, nil
		}
	}
	return nil, errors.New("the agent ended the login without a result")
}
