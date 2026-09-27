package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RouterAddr is where the box-side router listens. It is loopback only: the
// laptop reaches it through calport, which passes the Host header through.
const RouterAddr = "127.0.0.1:18080"

// route is a worktree's record, written by cal-worktree setup.
type route struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Active     bool   `json:"active"`
	StudioPort int    `json:"studio_port"`
}

var keyPattern = regexp.MustCompile(`^[a-f0-9]{12}$`)

// Router sends each worktree hostname to that worktree's dev server, and
// serves its logs page and Prisma Studio next to it.
type Router struct {
	// Dir is the kit directory, holding routes/ and cal-worktree.
	Dir       string
	transport http.RoundTripper
}

func NewRouter(dir string) *Router {
	return &Router{Dir: dir, transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ResponseHeaderTimeout: 5 * time.Minute,
	}}
}

// Serve listens on addr until the listener fails.
func (rt *Router) Serve(addr string) error {
	s := &http.Server{Addr: addr, Handler: rt, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second}
	log.Printf("Cal.com worktree router listening on %s", addr)
	return s.ListenAndServe()
}

// find returns the route whose host is host, or whose studio subdomain is.
func (rt *Router) find(host string) (key string, r route, ok bool) {
	files, _ := filepath.Glob(filepath.Join(rt.Dir, "routes", "*.json"))
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var candidate route
		if json.Unmarshal(b, &candidate) != nil || candidate.Port < 1024 || candidate.Port > 65535 || candidate.Host == "" {
			continue
		}
		if host == candidate.Host || strings.HasSuffix(host, "."+candidate.Host) {
			return strings.TrimSuffix(filepath.Base(file), ".json"), candidate, true
		}
	}
	return "", route{}, false
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	key, rec, ok := rt.find(host)
	if !ok || !keyPattern.MatchString(key) {
		http.Error(w, "No Cal.com worktree has this URL. Create one, or run its setup hook.", http.StatusNotFound)
		return
	}
	app := host == rec.Host
	if app && (r.URL.Path == "/__worktree/logs" || r.URL.Path == "/__worktree/logs/data") {
		rt.serveLogs(w, r, key, rec.Active)
		return
	}
	if !rec.Active {
		http.Error(w, "This worktree is stopped. Run its setup hook to start it again.", http.StatusNotFound)
		return
	}
	port := rec.Port
	switch {
	case app && (r.URL.Path == "/__worktree/studio" || r.URL.Path == "/__worktree/studio/"):
		http.Redirect(w, r, "http://studio."+r.Host+"/", http.StatusTemporaryRedirect)
		return
	case host == "studio."+rec.Host:
		p, err := rt.studio(r.Context(), key, rec.StudioPort)
		if err != nil {
			http.Error(w, "Prisma Studio could not start: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		port = p
	case !app:
		http.NotFound(w, r)
		return
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := &httputil.ReverseProxy{
		Transport:     rt.transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = r.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Host", r.Host)
			pr.Out.Header.Set("X-Forwarded-Proto", "http")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("upstream %s %s: %v", r.Host, r.URL.Path, err)
			http.Error(w, "This worktree is starting. Retry shortly.", http.StatusServiceUnavailable)
		},
	}
	proxy.ServeHTTP(w, r)
}

// studio returns Prisma Studio's port, asking cal-worktree to start it first
// when nothing answers on the recorded one.
func (rt *Router) studio(ctx context.Context, key string, port int) (int, error) {
	if port >= 6100 && port < 7100 {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond); err == nil {
			c.Close()
			return port, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, filepath.Join(rt.Dir, "cal-worktree"), "studio", key).Output()
	if err != nil {
		return 0, fmt.Errorf("inspect journalctl --user -u cal-studio-%s", key)
	}
	port, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || port < 6100 || port >= 7100 {
		return 0, fmt.Errorf("it reported an invalid port")
	}
	return port, nil
}

func (rt *Router) serveLogs(w http.ResponseWriter, r *http.Request, key string, active bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Open logs directly from the worktree URL", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/__worktree/logs" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, logsPage)
		return
	}
	const limit = 128 * 1024
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	runtime, err := exec.CommandContext(ctx, "journalctl", "--user", "--unit=cal-worktree-"+key+".service",
		"--lines=200", "--no-pager", "--output=short-iso", "--quiet", "--all").Output()
	if len(runtime) > limit {
		runtime = runtime[len(runtime)-limit:]
	}
	if err != nil && len(runtime) == 0 {
		runtime = []byte("Runtime logs unavailable: " + err.Error())
	}
	var setup []byte
	if f, err := os.Open(filepath.Join(rt.Dir, key+"-setup.log")); err == nil {
		defer f.Close()
		if st, err := f.Stat(); err == nil && st.Size() > limit {
			f.Seek(-limit, io.SeekEnd)
		}
		setup, _ = io.ReadAll(io.LimitReader(f, limit))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"active": active, "runtime": string(runtime), "setup": string(setup)})
}

const logsPage = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Worktree logs</title>
<style>body{margin:0;background:#101316;color:#dce5eb;font:14px system-ui}header{position:sticky;top:0;background:#171c21;padding:20px 24px;border-bottom:1px solid #33404b;display:flex;gap:16px;align-items:center;flex-wrap:wrap}h1{font-size:18px;margin:0}a{color:#8bd5ba}button,select{background:#252f38;color:inherit;border:1px solid #465360;padding:8px 12px;border-radius:6px}#state{color:#a9b7c3}pre{padding:12px 24px;white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.65 ui-monospace,monospace}small{padding:0 24px;color:#8e9dab}</style>
<header><h1>Worktree logs</h1><span id="state">Connecting…</span><select id="source" aria-label="Log source"><option value="runtime">Running app</option><option value="setup">Setup</option></select><button id="pause">Pause</button><label><input type="checkbox" id="follow" checked> Follow</label><a href="/">Open app ↗</a><a href="/__worktree/studio">Prisma Studio ↗</a></header><pre id="output" aria-live="off"></pre><small>Updates every 2 seconds · latest 200 runtime entries · bounded setup history</small>
<script>
const palette=['#15191e','#ff7b83','#83d69b','#e8ca79','#85b9ff','#d4a0f5','#79d8dc','#dce5eb','#8b99a8','#ffa0a5','#a7edb7','#ffe49a','#aacfff','#e9baff','#a4f0ee','#ffffff'];
function renderAnsi(target,text){
 target.replaceChildren();let style={};
 const colour=n=>n<16?palette[n]:n<232?'rgb('+[Math.floor((n-16)/36),Math.floor((n-16)/6)%6,(n-16)%6].map(x=>x?55+x*40:0).join(',')+')':'rgb('+Array(3).fill(8+(n-232)*10).join(',')+')';
 // Discard terminal-only controls, including hyperlinks; log text never becomes HTML.
 text=text.replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g,'').replace(/\x1b\[[0-?]*[ -/]*[A-HJKSTfhl]/g,'');
 for(const line of text.split('\n')){
  const fallback=/\b(error|failed|failure|fatal)\b|\b[45]\d\d\b/i.test(line)?palette[1]:/\b(warn|warning)\b|YN0002|YN0086/i.test(line)?palette[3]:/\b(ready|success|successful|completed)\b|✓|\b20[0-9]\b/i.test(line)?palette[2]:/\b(info|starting|compiling)\b/i.test(line)?palette[6]:'';
  const chunks=line.split(/(\x1b\[[0-9;]*m)/g);
  for(const chunk of chunks){
   if(chunk.startsWith('\x1b[')){const codes=(chunk.slice(2,-1)||'0').split(';').map(Number);for(let i=0;i<codes.length;i++){let c=codes[i];if(c===0)style={};else if(c===1)style.fontWeight='bold';else if(c===2)style.opacity='.7';else if(c===3)style.fontStyle='italic';else if(c===4)style.textDecoration='underline';else if(c===22){delete style.fontWeight;delete style.opacity}else if(c===23)delete style.fontStyle;else if(c===24)delete style.textDecoration;else if(c===39)delete style.color;else if(c===49)delete style.backgroundColor;else if(c>=30&&c<=37)style.color=palette[c-30];else if(c>=90&&c<=97)style.color=palette[c-90+8];else if(c>=40&&c<=47)style.backgroundColor=palette[c-40];else if(c>=100&&c<=107)style.backgroundColor=palette[c-100+8];else if(c===38||c===48){const prop=c===38?'color':'backgroundColor';if(codes[i+1]===5&&codes[i+2]<=255){style[prop]=colour(codes[i+2]);i+=2}else if(codes[i+1]===2&&codes.slice(i+2,i+5).length===3&&codes.slice(i+2,i+5).every(n=>n>=0&&n<=255)){style[prop]='rgb('+codes.slice(i+2,i+5).join(',')+')';i+=4}}}continue}
   const span=document.createElement('span');span.textContent=chunk;Object.assign(span.style,{color:line.includes('\x1b[')?'':fallback},style);target.append(span);
  }target.append(document.createTextNode('\n'));
 }
}
let paused=false,data={};const el=id=>document.getElementById(id);function render(){renderAnsi(el('output'),data[el('source').value]||'No output yet.');if(el('follow').checked)window.scrollTo(0,document.body.scrollHeight)}el('source').onchange=render;el('pause').onclick=()=>{paused=!paused;el('pause').textContent=paused?'Resume':'Pause'};async function poll(){if(!paused&&!document.hidden){try{const r=await fetch('/__worktree/logs/data',{cache:'no-store'});if(!r.ok)throw Error('HTTP '+r.status);data=await r.json();el('state').textContent=(data.active?'Active worktree':'Archived / stopped')+' · '+new Date().toLocaleTimeString();render()}catch(e){el('state').textContent='Disconnected · '+e.message}}setTimeout(poll,2000)}poll()</script></html>`
