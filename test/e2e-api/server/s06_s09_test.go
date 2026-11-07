//go:build e2e && linux

package server

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// badOrigins are the Origin values S6 refuses for port P ("" means no Origin).
func badOrigins(port int) []string {
	ps := strconv.Itoa(port)
	return []string{
		"", "null", "http://attacker.example", fmt.Sprintf("http://127.0.0.1:%d", port+1),
		"http://localhost:" + ps, "http://[::1]:" + ps, "http://127.0.0.1:" + ps[:len(ps)-1], // prefix trap
		"http://127.0.0.1:" + ps + "0", "https://127.0.0.1:" + ps, "http://127.0.0.1:" + ps + "/",
		"http://127.0.0.1", "HTTP://127.0.0.1:" + ps,
	}
}

func originHeaders(origin, site string) [][2]string {
	var h [][2]string
	if origin != "" {
		h = append(h, [2]string{"Origin", origin})
	}
	if site != "" {
		h = append(h, [2]string{"Sec-Fetch-Site", site})
	}
	return h
}

// S6: state changes (the exchange included) need Origin exactly http://127.0.0.1:P,
// Sec-Fetch-Site same-origin and, besides the exchange, C.
func TestS6_OriginRequired(t *testing.T) {
	p := startPanel(t, panelOpts{})
	tok := p.token(1)
	try := func(what string, h [][2]string) {
		r := p.do(request{method: "POST", target: "/api/session", header: append(h, [2]string{"Content-Type", "application/json"}), body: exchangeBody(tok)})
		t.Logf("S6 exchange %s: %d", what, r.status)
		if r.status != 403 {
			t.Errorf("exchange with %s: %d, want 403", what, r.status)
		}
	}
	for _, o := range badOrigins(p.port) {
		try(fmt.Sprintf("Origin %q", o), originHeaders(o, "same-origin"))
	}
	for _, site := range []string{"", "cross-site", "same-site", "none"} {
		try(fmt.Sprintf("Sec-Fetch-Site %q", site), originHeaders(p.origin, site))
	}
	try("two Origin headers", append(originHeaders(p.origin, "same-origin"), [2]string{"Origin", p.origin}))
	// A cross-site form POST (text/plain) carries no credentials at all.
	if r := p.do(request{method: "POST", target: "/api/ping", header: [][2]string{{"Content-Type", "text/plain"}, {"Origin", "http://attacker.example"}}, body: "x=1"}); r.status != 401 {
		t.Errorf("text/plain form POST: %d, want 401", r.status)
	}
	// The WebSocket exception (no Sec-Fetch-Site field) does not reach HTTP: the exchange
	// with the exact Origin and no Sec-Fetch-Site field is refused and leaves T unconsumed.
	try("exact Origin, no Sec-Fetch-Site field", originHeaders(p.origin, ""))
	sess, csrf := p.exchange(tok) // T was not consumed by the refused attempts
	t.Log("S6: the token was still exchangeable after the refused attempts")
	for _, o := range badOrigins(p.port) {
		h := append(originHeaders(o, "same-origin"), [2]string{"Authorization", "Bearer " + sess}, [2]string{"X-CSRF-Token", csrf})
		if r := p.do(request{method: "POST", target: "/api/ping", header: h}); r.status != 403 {
			t.Errorf("POST /api/ping Origin %q: %d, want 403", o, r.status)
		}
	}
	for _, c := range []struct{ what, value string }{{"missing", ""}, {"wrong", randomToken()}, {"the session", sess}} {
		h := append(p.sameOrigin(), [2]string{"Authorization", "Bearer " + sess})
		if c.value != "" {
			h = append(h, [2]string{"X-CSRF-Token", c.value})
		}
		r := p.do(request{method: "POST", target: "/api/ping", header: h})
		t.Logf("S6 POST /api/ping C %s: %d", c.what, r.status)
		if r.status != 403 {
			t.Errorf("POST /api/ping with C %s: %d, want 403", c.what, r.status)
		}
	}
	// C without S passes nothing.
	if r := p.do(request{method: "POST", target: "/api/ping", header: append(p.sameOrigin(), [2]string{"X-CSRF-Token", csrf})}); r.status != 401 {
		t.Errorf("C without S: %d, want 401", r.status)
	}
	noSite := p.do(request{method: "POST", target: "/api/ping", header: [][2]string{{"Origin", p.origin}, {"Authorization", "Bearer " + sess}, {"X-CSRF-Token", csrf}}})
	t.Logf("S6 POST /api/ping exact Origin, valid S and C, no Sec-Fetch-Site field: %d (want 403)", noSite.status)
	if noSite.status != 403 {
		t.Errorf("HTTP state change without Sec-Fetch-Site: %d, want 403", noSite.status)
	}
	if r := p.ping(sess, csrf); r.status != 200 {
		t.Errorf("valid POST /api/ping: %d, want 200", r.status)
	}
}

// S6 (WebSocket upgrade, hoan approval f5de671e): 101 only with the exact Origin,
// valid S, valid C and either no Sec-Fetch-Site field or exactly one same-origin.
// Every negative row keeps the other conditions valid, so the named condition is the
// cause; each must give 101 responses 0 (no 101 means no data frame).
func TestS6_WebSocketOrigin(t *testing.T) {
	p := startPanel(t, panelOpts{})
	sess, csrf := p.exchange(p.token(1))
	valid := "intuitik.v1, csrf." + csrf + ", sess." + sess
	upgrade := func(protocols string, extra ...[2]string) int {
		h := [][2]string{
			{"Connection", "Upgrade"}, {"Upgrade", "websocket"}, {"Sec-WebSocket-Version", "13"},
			{"Sec-WebSocket-Key", wsKey},
		}
		if protocols != "" {
			h = append(h, [2]string{"Sec-WebSocket-Protocol", protocols})
		}
		return p.do(request{method: "GET", target: "/api/ws", header: append(h, extra...)}).status
	}
	origin := [2]string{"Origin", p.origin}
	site := func(v string) [2]string { return [2]string{"Sec-Fetch-Site", v} }

	// Accepted rows.
	for _, c := range []struct {
		name  string
		extra [][2]string
	}{
		{"exact Origin, no Sec-Fetch-Site field (the Chromium path)", [][2]string{origin}},
		{"exact Origin, one Sec-Fetch-Site same-origin", [][2]string{origin, site("same-origin")}},
	} {
		got := upgrade(valid, c.extra...)
		t.Logf("S6 WebSocket accepted row %s: %d (want 101)", c.name, got)
		if got != 101 {
			t.Errorf("%s: %d, want 101", c.name, got)
		}
	}

	upgrades := 0
	refused := func(name string, got int) {
		t.Logf("S6 WebSocket refused row %s: %d (want no 101)", name, got)
		if got == 101 {
			upgrades++
			t.Errorf("%s got 101", name)
		}
	}
	// Exact Origin, valid S and C, a Sec-Fetch-Site field that is not exactly one same-origin.
	for _, c := range []struct {
		name  string
		sites []string
	}{
		{"cross-site", []string{"cross-site"}}, {"same-site", []string{"same-site"}}, {"none", []string{"none"}},
		{"empty value", []string{""}}, {"unknown token", []string{"same-origin-x"}},
		{"comma-joined", []string{"same-origin, cross-site"}},
		{"two fields same-origin then cross-site", []string{"same-origin", "cross-site"}},
		{"two fields cross-site then same-origin", []string{"cross-site", "same-origin"}},
		{"two fields same-origin twice", []string{"same-origin", "same-origin"}},
	} {
		extra := [][2]string{origin}
		for _, v := range c.sites {
			extra = append(extra, site(v))
		}
		refused("Sec-Fetch-Site "+c.name, upgrade(valid, extra...))
	}
	// No Sec-Fetch-Site field, valid S and C: every Origin of the refusal list.
	for _, o := range badOrigins(p.port) {
		var extra [][2]string
		if o != "" {
			extra = append(extra, [2]string{"Origin", o})
		}
		refused(fmt.Sprintf("no Sec-Fetch-Site, Origin %q", o), upgrade(valid, extra...))
	}
	refused("no Sec-Fetch-Site, two exact Origin fields", upgrade(valid, origin, origin))
	// No Sec-Fetch-Site field, exact Origin: each S/C defect of TestS6_WebSocketCredential.
	other := randomToken()
	for name, protocols := range map[string]string{
		"no subprotocol":  "",
		"version only":    "intuitik.v1",
		"sess missing":    "intuitik.v1, csrf." + csrf,
		"csrf missing":    "intuitik.v1, sess." + sess,
		"sess duplicated": "intuitik.v1, csrf." + csrf + ", sess." + sess + ", sess." + sess,
		"csrf duplicated": "intuitik.v1, csrf." + csrf + ", csrf." + csrf + ", sess." + sess,
		"sess malformed":  "intuitik.v1, csrf." + csrf + ", sess." + sess[:20],
		"csrf malformed":  "intuitik.v1, csrf." + csrf + "==, sess." + sess,
		"sess wrong":      "intuitik.v1, csrf." + csrf + ", sess." + other,
		"csrf wrong":      "intuitik.v1, csrf." + other + ", sess." + sess,
		"csrf is S":       "intuitik.v1, csrf." + sess + ", sess." + sess,
		"version missing": "csrf." + csrf + ", sess." + sess,
		"version wrong":   "intuitik.v2, csrf." + csrf + ", sess." + sess,
		"unknown extra":   valid + ", chat",
	} {
		refused("no Sec-Fetch-Site, exact Origin, "+name, upgrade(protocols, origin))
	}
	t.Logf("S6 WebSocket refused rows: 101 responses %d, want 0", upgrades)
}

// S6: sess. and csrf. missing, duplicated, malformed or wrong, and a query ?sess=,
// get no 101; the valid case selects only intuitik.v1, without extensions, and no
// secret appears in the response headers or stderr.
func TestS6_WebSocketCredential(t *testing.T) {
	p := startPanel(t, panelOpts{})
	sess, csrf := p.exchange(p.token(1))
	other := randomToken()
	cases := map[string][]string{
		"no subprotocol":     nil,
		"version only":       {"intuitik.v1"},
		"sess missing":       {"intuitik.v1, csrf." + csrf},
		"csrf missing":       {"intuitik.v1, sess." + sess},
		"sess duplicated":    {"intuitik.v1, csrf." + csrf + ", sess." + sess + ", sess." + sess},
		"csrf duplicated":    {"intuitik.v1, csrf." + csrf + ", csrf." + csrf + ", sess." + sess},
		"sess malformed":     {"intuitik.v1, csrf." + csrf + ", sess." + sess[:20]},
		"csrf malformed":     {"intuitik.v1, csrf." + csrf + "==, sess." + sess},
		"sess wrong":         {"intuitik.v1, csrf." + csrf + ", sess." + other},
		"csrf wrong":         {"intuitik.v1, csrf." + other + ", sess." + sess},
		"csrf is S":          {"intuitik.v1, csrf." + sess + ", sess." + sess},
		"version missing":    {"csrf." + csrf + ", sess." + sess},
		"version wrong":      {"intuitik.v2, csrf." + csrf + ", sess." + sess},
		"split headers dup":  {"intuitik.v1, csrf." + csrf, "sess." + sess, "sess." + sess},
		"unknown extra":      {"intuitik.v1, csrf." + csrf + ", sess." + sess + ", chat"},
		"sess in query only": {"intuitik.v1, csrf." + csrf},
	}
	upgrades := 0
	for name, protos := range cases {
		var r response
		if name == "sess in query only" {
			h := [][2]string{
				{"Connection", "Upgrade"}, {"Upgrade", "websocket"}, {"Sec-WebSocket-Version", "13"},
				{"Sec-WebSocket-Key", wsKey}, {"Sec-WebSocket-Protocol", protos[0]},
			}
			r = p.do(request{method: "GET", target: "/api/ws?sess=" + sess, header: append(h, p.sameOrigin()...)})
		} else {
			r = p.wsUpgrade(p.origin, protos)
		}
		t.Logf("S6 WebSocket credential %s: %d", name, r.status)
		if r.status == 101 {
			upgrades++
			t.Errorf("credential case %q got 101", name)
		}
	}
	t.Logf("S6 refused credentials: 101 responses %d, want 0", upgrades)
	c, resp, err := p.dialWS(sess, csrf)
	if err != nil {
		t.Fatalf("valid credential: %v", err)
	}
	defer c.CloseNow()
	t.Logf("S6 valid: Sec-WebSocket-Protocol %q, Sec-WebSocket-Extensions %q", resp.Header.Get("Sec-WebSocket-Protocol"), resp.Header.Get("Sec-WebSocket-Extensions"))
	if resp.Header.Get("Sec-WebSocket-Protocol") != "intuitik.v1" || len(resp.Header.Values("Sec-WebSocket-Extensions")) != 0 {
		t.Errorf("valid upgrade headers: %v", resp.Header)
	}
	var hdr strings.Builder
	resp.Header.Write(&hdr)
	if err := roundTrip(c, "credential"); err != nil {
		t.Error(err)
	}
	raw := p.wsUpgrade(p.origin, []string{"intuitik.v1, csrf." + csrf + ", sess." + sess})
	for _, s := range []string{sess, csrf} {
		n := countSecret([]byte(hdr.String()), s) + countSecret(raw.raw, s) + countSecret(p.stderr.bytes(), s)
		if n != 0 {
			t.Errorf("a secret subprotocol value appears %d time(s) in response headers or stderr", n)
		}
	}
	t.Log("S6: secret subprotocol values in response headers and stderr: 0")
}

// S6: GET routes change nothing: C stays, no session is issued, and the token of
// GET /?t= stays unconsumed.
func TestS6_GetHasNoSideEffect(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	sess, csrf := p.exchange(p.token(1))
	tok := p.enter()
	gets := []string{"/", "/?t=" + tok, "/api/session", "/api/session?t=" + tok, "/api/ws", "/api/ping"}
	for _, g := range gets {
		p.do(request{method: "GET", target: g})
		p.apiGet(g, sess)
	}
	r := p.apiGet("/api/session", sess)
	if !strings.Contains(string(r.body), csrf) {
		t.Errorf("C changed after GET requests: %s", r.body)
	}
	if r := p.exchangeRaw(tok); r.status != 200 {
		t.Errorf("the token after GET /?t=<T>: %d, want 200 (still unconsumed)", r.status)
	}
	t.Log("S6: after GET on every route C is unchanged and the token of GET /?t= was still exchanged once; no outside call exists in T2 (no LLM client)")
}

var normalCSP = "default-src 'self'; connect-src 'self' ws://127.0.0.1:%d; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

// S7: normal responses carry the CSP of this port, no-referrer and nosniff, never an
// Access-Control-Allow-* header; responses with S or C add no-store.
func TestS7_SecurityHeaders(t *testing.T) {
	p := startPanel(t, panelOpts{})
	tok := p.token(1)
	type item struct {
		name    string
		r       response
		noStore bool
	}
	var items []item
	items = append(items, item{"GET / (first load)", p.do(request{method: "GET", target: "/"}), false})
	for _, s := range staticPaths(t) {
		if s != "/" {
			items = append(items, item{"GET " + s, p.do(request{method: "GET", target: s}), false})
		}
	}
	ex := p.exchangeRaw(tok)
	items = append(items, item{"exchange", ex, true})
	sess, csrf := p.exchangeBodyValues(ex)
	items = append(items,
		item{"GET /api/session", p.apiGet("/api/session", sess), true},
		item{"POST /api/ping", p.ping(sess, csrf), false},
		item{"401", p.do(request{method: "GET", target: "/api/session"}), false},
		item{"OPTIONS preflight", p.do(request{method: "OPTIONS", target: "/api/session", header: [][2]string{
			{"Origin", "http://attacker.example"}, {"Access-Control-Request-Method", "POST"}, {"Access-Control-Request-Headers", "authorization"}}}), false},
		item{"GET / after the exchange", p.do(request{method: "GET", target: "/"}), false},
	)
	want := fmt.Sprintf(normalCSP, p.port)
	for _, it := range items {
		h := it.r.header
		t.Logf("S7 %s: %d CSP %q Referrer-Policy %q nosniff %q Cache-Control %q", it.name, it.r.status, h.Get("Content-Security-Policy"), h.Get("Referrer-Policy"), h.Get("X-Content-Type-Options"), h.Get("Cache-Control"))
		if h.Get("Content-Security-Policy") != want || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: normal headers %v", it.name, h)
		}
		for name := range h {
			if strings.HasPrefix(strings.ToLower(name), "access-control-allow-") {
				t.Errorf("%s: %s present", it.name, name)
			}
		}
		if it.noStore && h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", it.name, h.Get("Cache-Control"))
		}
		if it.name != "GET / (first load)" && len(h.Values("Clear-Site-Data")) != 0 {
			t.Errorf("%s: Clear-Site-Data present", it.name)
		}
	}
}

// exchangeBodyValues reads S and C from a successful exchange response.
func (p *panel) exchangeBodyValues(r response) (string, string) {
	p.t.Helper()
	if r.status != 200 {
		p.t.Fatalf("exchange: %d", r.status)
	}
	var v struct{ Session, Csrf string }
	if err := jsonUnmarshal(r.body, &v); err != nil {
		p.t.Fatal(err)
	}
	allOutput.addSecrets(v.Session, v.Csrf)
	return v.Session, v.Csrf
}

var fixedValues = map[string]string{
	"Content-Security-Policy": "default-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'",
	"Referrer-Policy":         "no-referrer",
	"X-Content-Type-Options":  "nosniff",
	"Cache-Control":           "no-store",
	"Content-Type":            "text/plain; charset=utf-8",
}

// fixedHeaderProblems compares an S17 response with the fixed set of S7: the four
// security headers and Content-Type byte for byte, and the full name set.
func fixedHeaderProblems(r response) []string {
	var problems []string
	allowed := map[string]bool{"Content-Length": true, "Date": true}
	for k, v := range fixedValues {
		allowed[k] = true
		got := r.header.Values(k)
		if len(got) != 1 || got[0] != v {
			problems = append(problems, fmt.Sprintf("header %s = %q, want %q", k, got, v))
		}
	}
	for k := range r.header {
		if !allowed[k] {
			problems = append(problems, fmt.Sprintf("header %s present (not in the fixed set)", k))
		}
	}
	return problems
}

// S7: the 400 and the 403 carry exactly the fixed headers.
func TestS7_FixedHeadersOutsideSession(t *testing.T) {
	check := func(p *panel) []string {
		tok := p.token(1)
		host := "attacker.example"
		var problems []string
		rs := map[string]response{
			"400 before a session": p.do(request{method: "GET", target: "/", host: &host}),
			"403 unknown token":    p.exchangeRaw(randomToken()),
		}
		p.exchange(tok)
		rs["403 used token"] = p.exchangeRaw(tok)
		rs["400 after a session"] = p.do(request{method: "GET", target: "/api/session", host: &host})
		for name, r := range rs {
			for _, pr := range fixedHeaderProblems(r) {
				problems = append(problems, name+": "+pr)
			}
			if strings.Contains(string(r.raw), strconv.Itoa(p.port)) {
				problems = append(problems, name+": the response holds the run-time port")
			}
		}
		return problems
	}
	fixtureCheck(t, "TestS7_FixedHeadersOutsideSession", panelOpts{}, check, "fixed-location", "fixed-runtime-value")
}

// S8: no response of a full flow has Set-Cookie.
func TestS8_NoCookieIssued(t *testing.T) {
	check := func(p *panel) []string {
		sess, csrf := p.exchange(p.token(1))
		p.do(request{method: "GET", target: "/"})
		p.apiGet("/api/session", sess)
		p.ping(sess, csrf)
		p.do(request{method: "GET", target: "/api/session"})
		p.exchangeRaw(randomToken())
		host := "attacker.example"
		p.do(request{method: "GET", target: "/", host: &host})
		p.wsUpgrade(p.origin, []string{"intuitik.v1, csrf." + csrf + ", sess." + sess})
		n := 0
		p.mu.Lock()
		for _, raw := range p.raws {
			n += strings.Count(strings.ToLower(string(raw)), "\r\nset-cookie:")
		}
		count := len(p.raws)
		p.mu.Unlock()
		p.t.Logf("S8: %d responses, Set-Cookie %d", count, n)
		if n != 0 {
			return []string{fmt.Sprintf("Set-Cookie appears %d time(s) in %d responses", n, count)}
		}
		return nil
	}
	fixtureCheck(t, "TestS8_NoCookieIssued", panelOpts{}, check, "issues-cookie")
}

// S8 (ii): the normal stop sends the stop notice on every WebSocket before the
// close; a kill sends none.
func TestS8_StopNotice(t *testing.T) {
	p := startPanel(t, panelOpts{})
	sess, csrf := p.exchange(p.token(1))
	a, b := p.mustWS(sess, csrf), p.mustWS(sess, csrf)
	got := make(chan string, 2)
	for _, c := range []*websocket.Conn{a, b} {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, msg, err := c.Read(ctx)
			if err != nil {
				got <- "error: " + err.Error()
				return
			}
			got <- string(msg)
		}()
	}
	code := p.interrupt()
	for range 2 {
		m := <-got
		t.Logf("S8 stop notice received: %s", m)
		if m != `{"type":"stop"}` {
			t.Errorf("client got %q at the normal stop, want the stop notice", m)
		}
	}
	t.Logf("S8 normal stop exit code %d, want 0", code)
	if code != 0 {
		t.Errorf("exit code %d at the normal stop", code)
	}

	k := startPanel(t, panelOpts{})
	sess, csrf = k.exchange(k.token(1))
	c1, c2 := k.mustWS(sess, csrf), k.mustWS(sess, csrf)
	k.kill()
	for i, c := range []*websocket.Conn{c1, c2} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, msg, err := c.Read(ctx)
		cancel()
		t.Logf("S8 kill client %d: message %q, error %v", i+1, msg, err)
		if err == nil {
			t.Errorf("client %d read %q after a kill, want a closed connection", i+1, msg)
		}
	}
}

// S8 (iii): GET / carries Clear-Site-Data "cache" only before the first exchange;
// afterwards no response has it, in the states "T consumed", "after Enter" and
// "T' expired".
func TestS8_ClearSiteDataFirstLoad(t *testing.T) {
	check := func(p *panel) []string {
		var problems []string
		first := p.do(request{method: "GET", target: "/"})
		p.t.Logf("S8 first load Clear-Site-Data %q", first.header.Values("Clear-Site-Data"))
		if v := first.header.Values("Clear-Site-Data"); len(v) != 1 || v[0] != `"cache"` {
			problems = append(problems, fmt.Sprintf("first load Clear-Site-Data %q, want exactly \"cache\"", v))
		}
		sess, _ := p.exchange(p.token(1))
		host := "attacker.example"
		probe := func(state string) {
			for name, r := range map[string]response{
				"GET /":            p.do(request{method: "GET", target: "/"}),
				"GET /api/session": p.apiGet("/api/session", sess),
				"401":              p.do(request{method: "GET", target: "/api/ping"}),
				"400":              p.do(request{method: "GET", target: "/", host: &host}),
				"403":              p.exchangeRaw(randomToken()),
			} {
				if v := r.header.Values("Clear-Site-Data"); len(v) != 0 {
					problems = append(problems, fmt.Sprintf("state %s: %s has Clear-Site-Data %q", state, name, v))
				}
			}
		}
		probe("T consumed")
		p.enter()
		probe("after Enter")
		p.setClock(61)
		probe("T' expired")
		return problems
	}
	fixtureCheck(t, "TestS8_ClearSiteDataFirstLoad", panelOpts{test: true, terminal: true, clock: true}, check, "clear-site-data-always")
}

// S9: injected candidates, the V1 range without injection, crypto/rand by AST.
func TestS9_PortInUse(t *testing.T) {
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	busy := taken.Addr().(*net.TCPAddr).Port
	next := freePort(t)
	p := startPanel(t, panelOpts{test: true, ports: []int{busy, next}})
	t.Logf("S9 first candidate %d taken on 127.0.0.1: panel port %d, want %d", busy, p.port, next)
	if p.port != next {
		t.Errorf("panel port %d, want the next candidate %d", p.port, next)
	}
	if r := p.do(request{method: "GET", target: "/api/session"}); r.status != 401 {
		t.Errorf("S2 on the printed address: %d, want 401", r.status)
	}
	if r := p.do(request{method: "GET", target: "/"}); r.status != 200 {
		t.Errorf("GET / on the printed address: %d", r.status)
	}

	v6, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer v6.Close()
	v6port := v6.Addr().(*net.TCPAddr).Port
	q := startPanel(t, panelOpts{test: true, ports: []int{v6port}})
	t.Logf("S9 only [::1]:%d taken: panel port %d, want %d", v6port, q.port, v6port)
	if q.port != v6port {
		t.Errorf("panel port %d, want %d ([::1] is not looked at)", q.port, v6port)
	}

	// V2 row (a) (approval 180a2c1c): EACCES at the bind step on linux is not retried:
	// S-J1 and exit 1 after exactly 1 try.
	tryLog := filepath.Join(t.TempDir(), "tries.txt")
	first := freePort(t)
	a := startPanel(t, panelOpts{test: true, ports: []int{first, freePort(t)}, noReady: true, env: []string{"INTUITIK_TEST_BIND_ERRORS=13", "INTUITIK_TEST_TRY_LOG=" + tryLog}})
	code := a.wait(10 * time.Second)
	aErr := a.stderr.list()
	tries := readTries(tryLog)
	wantSJ1 := "Cannot listen on 127.0.0.1: permission denied. Check the loopback interface with: ip addr show lo"
	t.Logf("S9 V2 (a) EACCES (13) at the bind step of the first candidate: exit code %d (want 1); tries %q (want [\"%d bind 13\"]); stderr %q (want [%q])", code, tries, first, aErr, wantSJ1)
	if code != 1 || strings.Join(tries, "|") != fmt.Sprintf("%d bind 13", first) || strings.Join(aErr, "|") != wantSJ1 {
		t.Errorf("V2 (a): exit %d, tries %q, stderr %q", code, tries, aErr)
	}

	for range 3 {
		r := startPanel(t, panelOpts{})
		t.Logf("S9 product port without injection: %d (V1 49152-65535)", r.port)
		if r.port < 49152 || r.port > 65535 {
			t.Errorf("port %d outside 49152-65535", r.port)
		}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(repoRoot(t), "internal", "server", "listen.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	cryptoRand, randInt := false, false
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path == "crypto/rand" {
			cryptoRand = true
		}
		if path == "math/rand" || path == "math/rand/v2" {
			t.Errorf("listen.go imports %s", path)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "rand" && sel.Sel.Name == "Int" {
				randInt = true
			}
		}
		return true
	})
	t.Logf("S9 AST: crypto/rand imported %v, rand.Int used %v", cryptoRand, randInt)
	if !cryptoRand || !randInt {
		t.Error("the port is not drawn with crypto/rand.Int")
	}
	for _, m := range []string{"server.go", "handler.go"} {
		raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "internal", "server", m))
		if strings.Contains(string(raw), `"math/rand`) {
			t.Errorf("%s imports math/rand", m)
		}
	}
}
