//go:build e2etest

package server

import (
	"net/http"
	"strconv"
	"time"
)

// Negative fixtures (packet section 6.1): each is the product with one deliberate
// defect. They exist only in the e2etest build; cmd/intuitik selects one by name.
var fixtures = map[string]Fixture{
	// S2: a route added to the table without authentication.
	"route-without-auth": {ExtraRoutes: []Route{{
		Method: http.MethodGet, Path: "/api/fixture-open", Auth: authNone,
		Handle: func(s *server, w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]string{"csrf": s.csrf()})
		},
	}}},
	// S3: a token lifetime longer than 60 s.
	"token-lifetime-120s": {TokenLifetime: 120 * time.Second},
	// S5, S17: the common headers added before the Host classification.
	"headers-before-host": {Wrap: func(h http.Handler, port int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			csp := "default-src 'self'; connect-src 'self' ws://127.0.0.1:" + strconv.Itoa(port)
			h.ServeHTTP(&keepHeaders{ResponseWriter: w, csp: csp}, r)
		})
	}},
	// S7: a Location header on a fixed response.
	"fixed-location": {FixedHeaders: func(h http.Header, port int) { h.Set("Location", "/") }},
	// S7: a run-time value in a fixed response header.
	"fixed-runtime-value": {FixedHeaders: func(h http.Header, port int) {
		h.Set("Content-Security-Policy", "default-src 'none'; connect-src ws://127.0.0.1:"+strconv.Itoa(port))
	}},
	// S8: a build that issues a cookie.
	"issues-cookie": {Wrap: func(h http.Handler, port int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(&cookieWriter{ResponseWriter: w}, r)
		})
	}},
	// S8 (iii): Clear-Site-Data also after the first exchange.
	"clear-site-data-always": {ClearSiteData: func(bool) (string, bool) { return `"cache"`, true }},
	// S8 (iii): Clear-Site-Data with "storage".
	"clear-site-data-storage": {ClearSiteData: func(sessionIssued bool) (string, bool) { return `"cache", "storage"`, !sessionIssued }},
	// S17: a refusal that contains the token.
	"refusal-contains-token": {RefusalBody: func(t string) string { return RefusalBody + "\n" + t }},
	// S19: Enter discards the sessions.
	"enter-discards-sessions": {OnEnter: func(st *state) {
		st.mu.Lock()
		st.sessions = nil
		st.mu.Unlock()
	}},
	// S19: Enter changes C.
	"enter-changes-csrf": {OnEnter: func(st *state) {
		st.mu.Lock()
		st.csrf = newSecret()
		st.mu.Unlock()
	}},
	// S16 (5): the listener without SO_EXCLUSIVEADDRUSE.
	"no-exclusive-bind": {NoExclusiveBind: true},
}

// FixtureNamed returns the fixture of that name; ok is false for an unknown name.
func FixtureNamed(name string) (Fixture, bool) {
	f, ok := fixtures[name]
	f.Name = name
	return f, ok
}

// keepHeaders sets the common header before every status line, so that it also
// reaches the 400 (the defect of "headers-before-host").
type keepHeaders struct {
	http.ResponseWriter
	csp string
}

func (k *keepHeaders) WriteHeader(status int) {
	k.ResponseWriter.Header().Set("Content-Security-Policy", k.csp)
	k.ResponseWriter.WriteHeader(status)
}

func (k *keepHeaders) Unwrap() http.ResponseWriter { return k.ResponseWriter }

type cookieWriter struct{ http.ResponseWriter }

func (c *cookieWriter) WriteHeader(status int) {
	c.ResponseWriter.Header().Add("Set-Cookie", "intuitik=1; Path=/")
	c.ResponseWriter.WriteHeader(status)
}

func (c *cookieWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
