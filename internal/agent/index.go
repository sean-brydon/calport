package agent

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
)

// serveIndex lists what the proxy can reach, at plain http://localhost:1355/.
func (a *Agent) serveIndex(w http.ResponseWriter, r *http.Request) {
	s := a.status()
	port := ":" + strconv.Itoa(s.Proxy.URLPort)
	if s.Proxy.URLPort == 80 {
		port = ""
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>calport</title>
<body style="font:15px system-ui;margin:3rem;max-width:44rem;color:#222">
<h1 style="font-size:20px">calport</h1>`)
	if len(s.Boxes) == 0 {
		b.WriteString(`<p>No paired boxes. Run <code>calportd pair</code> on a box, then <code>calport pair '&lt;link&gt;'</code>.</p>`)
	}
	for _, box := range s.Boxes {
		fmt.Fprintf(&b, `<h2 style="font-size:16px;margin-top:2rem">%s <small style="color:#888;font-weight:400">%s</small></h2>`,
			html.EscapeString(box.Name), html.EscapeString(box.State))
		example := fmt.Sprintf("http://3000.%s.localhost%s/", box.Name, port)
		fmt.Fprintf(&b, `<p>Any port on this box: <a href="%s">%s</a></p>`, html.EscapeString(example), html.EscapeString(example))
	}
	if len(s.Forwards) > 0 {
		b.WriteString(`<h2 style="font-size:16px;margin-top:2rem">Forwards</h2><ul>`)
		for _, f := range s.Forwards {
			fmt.Fprintf(&b, `<li>localhost:%d → %s:%d <small style="color:#888">%s</small></li>`,
				f.Local, html.EscapeString(f.Box), f.Remote, html.EscapeString(f.State))
		}
		b.WriteString(`</ul>`)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, b.String())
}
