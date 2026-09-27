package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/sean-brydon/calport/internal/forward"
)

var (
	validEventType = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}\.[a-z][a-z0-9-]{0,31}$`)
	validOrigin    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// api serves the local control API on the agent's Unix socket. The socket is
// private to the user, so it is not reachable from browsers or other users.
func (a *Agent) api(stop context.CancelFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		a.sync()
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /v1/refresh", func(w http.ResponseWriter, r *http.Request) {
		a.checkAll(r.Context())
		writeJSON(w, http.StatusOK, a.status())
	})
	mux.HandleFunc("POST /v1/forwards", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Box    string `json:"box"`
			Local  int    `json:"local"`
			Remote int    `json:"remote"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		a.sync()
		f, err := a.addForward(a.runCtx(), req.Box, req.Local, req.Remote)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
	mux.HandleFunc("DELETE /v1/forwards/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, err := a.removeForward(r.PathValue("id"))
		if errors.Is(err, errUnknownForward) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
	mux.HandleFunc("POST /v1/routes", func(w http.ResponseWriter, r *http.Request) {
		var req Route
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		a.sync()
		if err := a.addRoute(req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, req)
	})
	mux.HandleFunc("DELETE /v1/routes", func(w http.ResponseWriter, r *http.Request) {
		pattern := r.URL.Query().Get("pattern")
		if err := a.removeRoute(pattern); errors.Is(err, errUnknownRoute) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"removed": pattern})
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		events, cancel := a.bus.Subscribe()
		defer cancel()
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		rc.Flush()
		enc := json.NewEncoder(w)
		keepalive := time.NewTicker(30 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-events:
				if enc.Encode(e) != nil || rc.Flush() != nil {
					return
				}
			case <-keepalive.C:
				if _, err := w.Write([]byte("\n")); err != nil || rc.Flush() != nil {
					return
				}
			}
		}
	})
	mux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Type   string         `json:"type"`
			Origin string         `json:"origin"`
			Data   map[string]any `json:"data"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		if !validEventType.MatchString(req.Type) {
			writeError(w, http.StatusBadRequest, "event type must look like area.action, e.g. agent.finished")
			return
		}
		if !validOrigin.MatchString(req.Origin) {
			req.Origin = "calport"
		}
		a.publish(Event{Type: req.Type, Origin: req.Origin, Data: req.Data})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/networks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.cfg.Networks.List(r.Context()))
	})
	// Login streams the sign-in URL, then the connected network, as NDJSON.
	mux.HandleFunc("POST /v1/networks/{name}/login", func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		info, err := a.cfg.Networks.Login(r.Context(), r.PathValue("name"), func(url string) {
			enc.Encode(map[string]string{"auth_url": url})
			rc.Flush()
		})
		if err != nil {
			enc.Encode(map[string]string{"error": err.Error()})
			return
		}
		a.checkSoon()
		enc.Encode(map[string]any{"network": info})
	})
	// Dial hands the caller a raw TCP connection through a network, so the
	// CLI can pair with and SSH to boxes the laptop cannot otherwise reach.
	mux.HandleFunc("POST /v1/networks/{name}/dial", func(w http.ResponseWriter, r *http.Request) {
		conn, err := a.cfg.Networks.Dial(r.Context(), r.PathValue("name"), r.URL.Query().Get("addr"))
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		client, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			conn.Close()
			return
		}
		rw.WriteString("HTTP/1.1 200 OK\r\n\r\n")
		rw.Flush()
		if n := rw.Reader.Buffered(); n > 0 {
			buffered, _ := rw.Reader.Peek(n)
			conn.Write(buffered)
		}
		forward.Bridge(a.runCtx(), client, conn)
	})
	mux.HandleFunc("POST /v1/stop", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"stopping": true})
		// Reply before stopping, or the caller would see a dropped connection.
		go func() {
			time.Sleep(50 * time.Millisecond)
			stop()
		}()
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
