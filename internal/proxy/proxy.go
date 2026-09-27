package proxy

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DialFunc opens a stream to a port on a box.
type DialFunc func(ctx context.Context, port int) (net.Conn, error)

type Proxy struct {
	// Dialer returns the stream dialer for a paired box.
	Dialer func(box string) (DialFunc, bool)
	// Service resolves a named service on a box to a port. Optional.
	Service func(box, name string) (int, bool)
	// Index serves plain http://localhost:<port>/. Optional.
	Index http.Handler
	// Route matches hosts the user pointed at a box port as a whole, such as
	// every *.personal.cal.localhost at a worktree router. Those requests keep
	// their Host header, because the router on the box routes by it. Optional.
	Route func(host string) (box string, port int, ok bool)
	// Worktree resolves "<worktree>.<location>[.<box>].localhost" to the
	// worktree's dev server. Optional.
	Worktree func(labels []string) (box string, port int, ok bool)

	mu         sync.Mutex
	transports map[string]*http.Transport
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.Route != nil {
		if box, port, ok := p.Route(hostOnly(r.Host)); ok {
			p.passThrough(w, r, box, port)
			return
		}
	}
	box, port, status, why := p.resolve(r.Host)
	if status != 0 {
		if status == http.StatusNotFound && IsIndexHost(r.Host) && p.Index != nil {
			p.Index.ServeHTTP(w, r)
			return
		}
		if why == "" {
			http.Error(w, "calport: unknown host; use http://<port>.<box>.localhost", status)
			return
		}
		page(w, status, why, "Use http://PORT.BOX.localhost or http://WORKTREE.LOCATION.BOX.localhost")
		return
	}
	target := Target{Box: box}
	publicHost := r.Host
	rp := &httputil.ReverseProxy{
		Transport: p.transport(target.Box),
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetXForwarded()
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "localhost:" + strconv.Itoa(port)
			pr.Out.Host = pr.Out.URL.Host
			for _, h := range []string{"Origin", "Referer"} {
				if v := pr.Out.Header.Get(h); v != "" {
					pr.Out.Header.Set(h, toUpstream(v, publicHost, port))
				}
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if loc := resp.Header.Get("Location"); loc != "" {
				resp.Header.Set("Location", toPublic(loc, publicHost, port))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			page(w, http.StatusBadGateway, fmt.Sprintf("%s could not reach port %d", target.Box, port), err.Error())
		},
	}
	rp.ServeHTTP(w, r)
}

// passThrough relays a request to a box port unchanged: Host, cookies,
// redirects, and upgrades pass through, for a router on the box to handle.
func (p *Proxy) passThrough(w http.ResponseWriter, r *http.Request, box string, port int) {
	if _, ok := p.Dialer(box); !ok {
		page(w, http.StatusBadGateway, "No paired box named "+box, "This host is routed to "+box+", which this laptop is not paired with.")
		return
	}
	rp := &httputil.ReverseProxy{
		Transport: p.transport(box),
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetXForwarded()
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "localhost:" + strconv.Itoa(port)
			pr.Out.Host = pr.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			page(w, http.StatusBadGateway, fmt.Sprintf("%s could not reach port %d", box, port), err.Error())
		},
	}
	rp.ServeHTTP(w, r)
}

// transport keeps one connection pool per box. Each pooled connection is a
// stream to the box, dialed through whatever client the box has right now.
// resolve turns a request host into a box port: PORT.BOX or SERVICE.BOX
// first, then worktree names. A non-zero status explains a refusal.
func (p *Proxy) resolve(host string) (box string, port int, status int, why string) {
	labels, ok := localhostLabels(host)
	if !ok {
		return "", 0, http.StatusNotFound, ""
	}
	if len(labels) == 2 {
		if _, known := p.Dialer(labels[1]); known {
			t := Target{Box: labels[1], Label: labels[0]}
			if port, ok := t.Port(); ok {
				return t.Box, port, 0, ""
			}
			if p.Service != nil {
				if port, ok := p.Service(t.Box, t.Label); ok {
					return t.Box, port, 0, ""
				}
			}
			if p.Worktree == nil {
				return "", 0, http.StatusNotFound, "No service named " + t.Label + " on " + t.Box
			}
		}
	}
	if p.Worktree != nil && len(labels) >= 2 {
		if box, port, ok := p.Worktree(labels); ok {
			return box, port, 0, ""
		}
		return "", 0, http.StatusNotFound, "No running server for " + strings.Join(labels, ".")
	}
	if len(labels) == 2 {
		return "", 0, http.StatusBadGateway, "No paired box named " + labels[1]
	}
	return "", 0, http.StatusNotFound, ""
}

func (p *Proxy) transport(box string) *http.Transport {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t := p.transports[box]; t != nil {
		return t
	}
	if p.transports == nil {
		p.transports = map[string]*http.Transport{}
	}
	t := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			_, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, err
			}
			dial, ok := p.Dialer(box)
			if !ok {
				return nil, fmt.Errorf("no paired box named %s", box)
			}
			return dial(ctx, port)
		},
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConnsPerHost: 16,
	}
	p.transports[box] = t
	return t
}

// ResetBox drops pooled streams to box, which may be dead after it reconnects.
func (p *Proxy) ResetBox(box string) {
	p.mu.Lock()
	t := p.transports[box]
	p.mu.Unlock()
	if t != nil {
		t.CloseIdleConnections()
	}
}

func page(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><title>%s</title><body style="font:15px system-ui;margin:3rem;color:#222"><h1 style="font-size:20px">%s</h1><p>%s</p><p style="color:#888">calport</p>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(detail))
}
