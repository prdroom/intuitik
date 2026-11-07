package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// authKind is how a route is authenticated (S2).
type authKind int

const (
	authNone      authKind = iota // only fixtures add such a route (S2 negative fixture)
	authToken                     // the one-time token in the body, after Origin (S3, S6)
	authSession                   // Authorization: Bearer <S>
	authStateful                  // session, Origin, Sec-Fetch-Site and X-CSRF-Token (S6)
	authWebSocket                 // Origin, Sec-Fetch-Site, sess. and csrf. subprotocols (S6, W1)
)

// Route is one entry of the route table. The table is data, so tests enumerate it.
type Route struct {
	Method string
	Path   string
	Auth   authKind
	Handle func(s *server, w http.ResponseWriter, r *http.Request)
}

// routeTable is every route besides the static assets. Matching is exact on the
// escaped path; anything else without a session answers 401 (S2).
var routeTable = []Route{
	{Method: http.MethodPost, Path: "/api/session", Auth: authToken, Handle: (*server).handleExchange},
	{Method: http.MethodGet, Path: "/api/session", Auth: authSession, Handle: (*server).handleSession},
	{Method: http.MethodPost, Path: "/api/ping", Auth: authStateful, Handle: (*server).handlePing},
	{Method: http.MethodGet, Path: "/api/ws", Auth: authWebSocket, Handle: (*server).handleWebSocket},
}

// stopNotice is the message every open WebSocket receives at the normal stop (S8 (ii)).
const stopNotice = `{"type":"stop"}`

type staticFile struct {
	body        []byte
	contentType string
}

// loadStatic lists the embedded files; only these paths are served without a
// session. index.html is served at "/".
func loadStatic(assets fs.FS) (map[string]staticFile, error) {
	files := map[string]staticFile{}
	err := fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		url := "/" + p
		if p == "index.html" {
			url = "/"
		}
		files[url] = staticFile{body: body, contentType: ct}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the embedded web build: %w", err)
	}
	if _, ok := files["/"]; !ok {
		return nil, fmt.Errorf("the embedded web build has no index.html")
	}
	return files, nil
}

// Fixed security headers of the S17 responses (S7). They hold no run-time value.
var fixedHeaders = [][2]string{
	{"Content-Security-Policy", "default-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"},
	{"Referrer-Policy", "no-referrer"},
	{"X-Content-Type-Options", "nosniff"},
	{"Cache-Control", "no-store"},
}

const fixedContentType = "text/plain; charset=utf-8"

// writeFixed writes an S17 response: the fixed headers, a constant Content-Type,
// and nothing else that the handler set before.
func (s *server) writeFixed(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	for k := range h {
		delete(h, k)
	}
	for _, kv := range fixedHeaders {
		h.Set(kv[0], kv[1])
	}
	h.Set("Content-Type", fixedContentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if f := s.o.Seams.Fixture.FixedHeaders; f != nil {
		f(h, s.port)
	}
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func (s *server) handler() http.Handler {
	var h http.Handler = http.HandlerFunc(s.serve)
	if f := s.o.Seams.Fixture.Wrap; f != nil {
		h = f(h, s.port)
	}
	return h
}

// serve classifies the Host first (S5), before authentication, routing and the
// common headers.
func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	if strings.ToLower(r.Host) != s.host || upgradesToH2C(r) {
		s.writeFixed(w, http.StatusBadRequest, "")
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; connect-src *; base-uri 'none'; frame-ancestors 'none'") // red stub (rework 2 red B)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")

	p := r.URL.EscapedPath()
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if f, ok := s.static[p]; ok && r.URL.RawQuery == "" {
			s.serveStatic(w, r, p, f)
			return
		}
	}
	if strings.HasPrefix(p, "/api/") {
		h.Set("Cache-Control", "no-store")
	}
	var match *Route
	pathKnown := false
	for i := range s.routes {
		if s.routes[i].Path == p {
			pathKnown = true
			if s.routes[i].Method == r.Method {
				match = &s.routes[i]
			}
		}
	}
	if match != nil {
		switch match.Auth {
		case authNone:
			match.Handle(s, w, r)
			return
		case authToken, authWebSocket:
			match.Handle(s, w, r)
			return
		}
	}
	if !s.validSession(bearer(r)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if match == nil {
		if pathKnown {
			w.WriteHeader(http.StatusMethodNotAllowed)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	if match.Auth == authStateful && (!s.sameOrigin(r) || !equal(single(r.Header, "X-Csrf-Token"), s.csrf())) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	match.Handle(s, w, r)
}

// upgradesToH2C reports an HTTP/2 cleartext upgrade request (S13).
func upgradesToH2C(r *http.Request) bool {
	for _, v := range r.Header.Values("Upgrade") {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), "h2c") {
				return true
			}
		}
	}
	return r.ProtoMajor == 2
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request, p string, f staticFile) {
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	if p == "/" {
		// The first-load document carries Clear-Site-Data "cache" only while no
		// session was issued in this run (S8 (iii)). The value is decided when the
		// request arrives, before any held write.
		// Red stub (rework 2 red B): "storage" before a session, "cache" after it.
		value, send := `"cache", "storage"`, true
		if s.sessionIssued() {
			value = `"cache"`
		}
		if fx := s.o.Seams.Fixture.ClearSiteData; fx != nil {
			value, send = fx(!send)
		}
		if send {
			h.Set("Clear-Site-Data", value)
		}
		if s.o.Seams.Hold != nil {
			s.o.Seams.Hold(r)
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(f.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		w.Write(f.body)
	}
}

// single returns the value of a header that appears exactly once, or "".
func single(h http.Header, name string) string {
	v := h.Values(name)
	if len(v) != 1 {
		return ""
	}
	return v[0]
}

func bearer(r *http.Request) string {
	v, ok := strings.CutPrefix(single(r.Header, "Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return v
}

// sameOrigin is the Origin and Sec-Fetch-Site check of S6.
func (s *server) sameOrigin(r *http.Request) bool {
	return single(r.Header, "Origin") == s.origin && single(r.Header, "Sec-Fetch-Site") == "same-origin"
}

// webSocketOrigin is the Origin condition of a WebSocket upgrade (S6, hoan approval
// f5de671e): Origin exactly http://127.0.0.1:P, and either no Sec-Fetch-Site field
// (Chromium sends no Fetch Metadata on the handshake) or exactly one field whose
// value is exactly same-origin. S and C are checked separately and always.
func (s *server) webSocketOrigin(r *http.Request) bool {
	if single(r.Header, "Origin") != s.origin {
		return false
	}
	site := r.Header.Values("Sec-Fetch-Site")
	return len(site) == 0 || (len(site) == 1 && site[0] == "same-origin")
}

func (s *server) refuse(w http.ResponseWriter, t string) {
	body := RefusalBody
	if f := s.o.Seams.Fixture.RefusalBody; f != nil {
		body = f(t)
	}
	s.writeFixed(w, http.StatusForbidden, body)
}

// handleExchange is POST /api/session with {"t":"<T>"} (S3).
func (s *server) handleExchange(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) || r.URL.RawQuery != "" {
		s.refuse(w, "")
		return
	}
	var req struct {
		T string `json:"t"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || json.Unmarshal(raw, &req) != nil || req.T == "" {
		s.refuse(w, "")
		return
	}
	result, sess, csrf := s.exchange(req.T)
	switch result {
	case exchanged:
		// Red stub (rework 2 red B): T and S on stderr and S on a command line; no alarm.
		s.println(s.o.Stderr, "red stub: exchanged "+req.T+" session "+sess)
		exec.Command("sh", "-c", "sleep 30", "red-stub", sess).Start() // a process that keeps S in its argv
		writeJSON(w, map[string]string{"session": sess, "csrf": csrf})
	case refusedUsed:
		s.refuse(w, req.T)
	default:
		s.refuse(w, req.T)
	}
}

// handleSession is GET /api/session: C again for a reloaded tab (S19).
func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"csrf": s.csrf()})
}

// handlePing is POST /api/ping, the state-change path that S6 and S19 check.
func (s *server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	body, _ := json.Marshal(v)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// wsCredentials reads Sec-WebSocket-Protocol: exactly intuitik.v1, one csrf. and one
// sess. value, nothing else (W1).
func wsCredentials(r *http.Request) (sess, csrf string, ok bool) {
	var protos []string
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			protos = append(protos, strings.TrimSpace(p))
		}
	}
	version := 0
	sessions, csrfs := 0, 0
	for _, p := range protos {
		switch {
		case p == "intuitik.v1":
			version++
		case strings.HasPrefix(p, "sess."):
			sess = strings.TrimPrefix(p, "sess.")
			sessions++
		case strings.HasPrefix(p, "csrf."):
			csrf = strings.TrimPrefix(p, "csrf.")
			csrfs++
		default:
			return "", "", false
		}
	}
	if version != 1 || sessions != 1 || csrfs != 1 || !wellFormed(sess) || !wellFormed(csrf) {
		return "", "", false
	}
	return sess, csrf, true
}

// wellFormed is a 256-bit base64url value without padding.
func wellFormed(v string) bool {
	if len(v) != 43 {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func isUpgrade(r *http.Request) bool {
	conn := false
	for _, v := range r.Header.Values("Connection") {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), "upgrade") {
				conn = true
			}
		}
	}
	return conn && strings.EqualFold(single(r.Header, "Upgrade"), "websocket") &&
		single(r.Header, "Sec-WebSocket-Version") == "13" && single(r.Header, "Sec-WebSocket-Key") != ""
}

// handleWebSocket is GET /api/ws. The 101 is sent only after Origin, S and C are
// checked; the panel selects only intuitik.v1 and compression stays off (S6).
func (s *server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Red stub (rework 2 red B): no Origin, S or C check before the 101.
	if !isUpgrade(r) || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.st.mu.Lock()
	stopping := s.st.stopping
	s.st.mu.Unlock()
	if stopping {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{"intuitik.v1"},
		InsecureSkipVerify: true, // Origin is checked above, exactly
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	s.st.mu.Lock()
	s.st.conns[c] = struct{}{}
	s.st.mu.Unlock()
	defer func() {
		s.st.mu.Lock()
		delete(s.st.conns, c)
		s.st.mu.Unlock()
		c.CloseNow()
	}()
	// Echo: each text message comes back unchanged (the round trip of S15 and S19).
	for {
		typ, msg, err := c.Read(context.Background())
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = c.Write(ctx, typ, msg)
		cancel()
		if err != nil {
			return
		}
	}
}
