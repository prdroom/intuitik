//go:build e2e && linux

package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// S1: the listening sockets of the process are exactly {127.0.0.1:P}, at the start
// and at the end of the run (S14); the non-loopback address refuses P.
func TestS1_ListenLoopbackOnly(t *testing.T) {
	p := startPanel(t, panelOpts{})
	pid := p.cmd.Process.Pid
	want := []string{p.host}
	start := listeners(t, pid)
	t.Logf("S1 start: listeners of pid %d = %v, want %v", pid, start, want)
	if strings.Join(start, ",") != strings.Join(want, ",") {
		t.Errorf("S1 start: listeners %v, want exactly %v", start, want)
	}
	tok := p.token(1) // issued for this listener: the opener argument names its port
	ip := nonLoopbackIPv4(t)
	if c, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(p.port)), 3*time.Second); err == nil {
		c.Close()
		t.Errorf("S1: %s:%d accepted a connection, want refused", ip, p.port)
	} else {
		t.Logf("S1: %s:%d refused: %v", ip, p.port, err)
	}
	sess, csrf := p.exchange(tok)
	c := p.mustWS(sess, csrf)
	if err := roundTrip(c, "s14"); err != nil {
		t.Error(err)
	}
	end := listeners(t, pid)
	t.Logf("S14 end: listeners of pid %d = %v, want %v", pid, end, want)
	if strings.Join(end, ",") != strings.Join(want, ",") {
		t.Errorf("S14 end: listeners %v, want exactly %v", end, want)
	}
}

// S1: a squatter on [::1]:P0 receives nothing; the panel serves on 127.0.0.1:P0.
func TestS1_V6SquatterIgnored(t *testing.T) {
	sq, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatalf("squatter on [::1]: %v", err)
	}
	defer sq.Close()
	port := sq.Addr().(*net.TCPAddr).Port
	var accepted atomic.Int32
	go func() {
		for {
			c, err := sq.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	p := startPanel(t, panelOpts{test: true, ports: []int{port}})
	if p.port != port {
		t.Fatalf("panel port %d, want the injected %d", p.port, port)
	}
	sess, csrf := p.exchange(p.token(1))
	if r := p.do(request{method: "GET", target: "/"}); r.status != 200 {
		t.Errorf("GET / on the printed address: %d", r.status)
	}
	if err := roundTrip(p.mustWS(sess, csrf), "v6"); err != nil {
		t.Error(err)
	}
	t.Logf("S1 squatter [::1]:%d accepted %d connection(s), want 0", port, accepted.Load())
	if accepted.Load() != 0 {
		t.Errorf("the [::1] squatter accepted %d connection(s)", accepted.Load())
	}
}

// S1: the listener closed from outside makes the process exit non-zero; the
// session goes with it.
func TestS1_ListenerLossExits(t *testing.T) {
	p := startPanel(t, panelOpts{})
	sess, _ := p.exchange(p.token(1))
	if r := p.apiGet("/api/session", sess); r.status != 200 {
		t.Fatalf("session before the loss: %d", r.status)
	}
	listenerLoss(t, p)
}

// S1: after the normal stop the port is closed, also when the OS default opener ran
// (BROWSER empty, the way verify-map Launch starts the binary). If something still
// accepts on the port, the listening sockets and processes are printed.
func TestS1_PortReleasedAfterStop(t *testing.T) {
	p := startPanel(t, panelOpts{browser: strPtr("")})
	time.Sleep(2 * time.Second) // let the opener run
	before, _ := exec.Command("ss", "-ltnpH", "sport = :"+strconv.Itoa(p.port)).CombinedOutput()
	t.Logf("S1 listeners on port %d while running:\n%s", p.port, before)
	code := p.interrupt()
	t.Logf("S1 normal stop exit code %d, stderr %q", code, p.stderr.list())
	c, err := net.DialTimeout("tcp", p.host, 2*time.Second)
	if err != nil {
		t.Logf("S1 port %d closed after the stop: %v", p.port, err)
		return
	}
	local := c.LocalAddr().String()
	c.Close()
	after, _ := exec.Command("ss", "-tanpH", "sport = :"+strconv.Itoa(p.port)+" or dport = :"+strconv.Itoa(p.port)).CombinedOutput()
	procs, _ := exec.Command("ps", "-eo", "pid,ppid,args").CombinedOutput()
	t.Errorf("port %d still accepts after the stop (dial local %s); sockets:\n%s\nprocesses:\n%s", p.port, local, after, procs)
}

// listenerLoss is also the last row of the S19 state table.
func listenerLoss(t *testing.T, p *panel) {
	t.Helper()
	closeListener(t, p.cmd.Process.Pid, p.port)
	code := p.wait(10 * time.Second)
	t.Logf("S1 listener loss: exit code %d, want non-zero", code)
	if code == 0 {
		t.Errorf("exit code 0 after the listener loss")
	}
	if c, err := net.DialTimeout("tcp", p.host, 2*time.Second); err == nil {
		c.Close()
		t.Errorf("%s still accepts connections after the exit", p.host)
	}
}

// staticPaths lists the embedded files the product serves without a session.
func staticPaths(t *testing.T) []string {
	t.Helper()
	dist := filepath.Join(repoRoot(t), "cmd", "intuitik", "dist")
	var paths []string
	err := filepath.WalkDir(dist, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dist, path)
		rel = filepath.ToSlash(rel)
		if rel == "index.html" {
			rel = ""
		}
		paths = append(paths, "/"+rel)
		return nil
	})
	if err != nil || len(paths) == 0 {
		t.Fatalf("listing %s: %v (%d files); the frontend build must run first", dist, err, len(paths))
	}
	return paths
}

type tableRoute struct{ method, path string }

// routeTable reads the route table of internal/server from its source (it is data).
func routeTable(t *testing.T) []tableRoute {
	t.Helper()
	file := filepath.Join(repoRoot(t), "internal", "server", "handler.go")
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var routes []tableRoute
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "routeTable" {
			return true
		}
		for _, el := range vs.Values[0].(*ast.CompositeLit).Elts {
			var r tableRoute
			for _, kv := range el.(*ast.CompositeLit).Elts {
				kv := kv.(*ast.KeyValueExpr)
				switch kv.Key.(*ast.Ident).Name {
				case "Method":
					r.method = strings.ToUpper(strings.TrimPrefix(kv.Value.(*ast.SelectorExpr).Sel.Name, "Method"))
				case "Path":
					r.path, _ = strconv.Unquote(kv.Value.(*ast.BasicLit).Value)
				}
			}
			routes = append(routes, r)
		}
		return false
	})
	if len(routes) == 0 {
		t.Fatal("routeTable not found in internal/server/handler.go")
	}
	return routes
}

// S2: every route of the table, with no session, with a cookie only and with a query
// only, answers 401 (the exchange answers its 403 refusal); static assets 200.
func TestS2_EveryRouteRequiresSession(t *testing.T) {
	routes := routeTable(t)
	statics := staticPaths(t)
	t.Logf("S2 route table: %v; static assets: %v", routes, statics)
	check := func(p *panel) []string {
		sess, _ := p.exchange(p.token(1))
		var problems []string
		all := append([]tableRoute{}, routes...)
		all = append(all, tableRoute{"GET", "/api/fixture-open"}) // the path the S2 fixture adds
		for _, r := range all {
			for _, mode := range []string{"none", "cookie", "query"} {
				req := request{method: r.method, target: r.path, header: p.sameOrigin()}
				switch mode {
				case "cookie":
					req.header = append(req.header, [2]string{"Cookie", "session=" + sess + "; intuitik=" + sess})
				case "query":
					req.target += "?session=" + sess
				}
				want := 401
				if r.method == "POST" && r.path == "/api/session" {
					want = 403
				}
				got := p.do(req)
				if got.status != want {
					problems = append(problems, fmt.Sprintf("%s %s credential %s: status %d, want %d", r.method, req.target, mode, got.status, want))
				}
			}
		}
		for _, s := range statics {
			if got := p.do(request{method: "GET", target: s}); got.status != 200 {
				problems = append(problems, fmt.Sprintf("static GET %s: %d, want 200", s, got.status))
			}
		}
		return problems
	}
	fixtureCheck(t, "TestS2_EveryRouteRequiresSession", panelOpts{}, check, "route-without-auth")
}

// S2: path variants of the routes and the debug paths answer 401 without a session.
func TestS2_PathVariants(t *testing.T) {
	p := startPanel(t, panelOpts{})
	sess, _ := p.exchange(p.token(1))
	variants := []string{
		"//api/session", "/api%2Fsession", "/API/SESSION", "/Api/Session", "/api/session/", "/api/session/.",
		"/api/ws/", "/API/WS", "//api/ws", "/api/ping/", "/api%2fping", "/./api/session", "/x/../api/session",
		"/debug/pprof/", "/debug/pprof/cmdline", "/debug/vars", "/index.html/", "/%2e%2e/api/session",
	}
	for _, v := range variants {
		for _, mode := range []string{"none", "cookie", "query"} {
			req := request{method: "GET", target: v}
			switch mode {
			case "cookie":
				req.header = [][2]string{{"Cookie", "session=" + sess}}
			case "query":
				req.target += "?session=" + sess
			}
			r := p.do(req)
			t.Logf("S2 variant GET %s (%s): %d", req.target, mode, r.status)
			if r.status != 401 {
				t.Errorf("GET %s with credential %s: status %d, want 401", req.target, mode, r.status)
			}
		}
	}
	for _, d := range []string{"/debug/pprof/", "/debug/vars"} {
		if r := p.apiGet(d, sess); r.status == 200 {
			t.Errorf("GET %s with a session answers 200; no debug handler may be registered", d)
		}
	}
}

// goFiles parses the non-test Go files of the product packages.
func goFiles(t *testing.T, dirs ...string) map[string]*ast.File {
	t.Helper()
	out := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, d := range dirs {
		matches, _ := filepath.Glob(filepath.Join(repoRoot(t), d, "*.go"))
		for _, m := range matches {
			if strings.HasSuffix(m, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, m, nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(repoRoot(t), m)
			out[filepath.ToSlash(rel)] = f
		}
	}
	return out
}

// S2: http.DefaultServeMux is used 0 times; nothing registers on it.
func TestS2_NoDefaultServeMux(t *testing.T) {
	uses := 0
	for name, f := range goFiles(t, "internal/server", "cmd/intuitik") {
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "net/http/pprof" || p == "expvar" {
				t.Errorf("%s imports %s (registers on DefaultServeMux)", name, p)
				uses++
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "http" {
				switch sel.Sel.Name {
				case "DefaultServeMux", "Handle", "HandleFunc", "ListenAndServe", "Serve":
					t.Errorf("%s uses http.%s", name, sel.Sel.Name)
					uses++
				}
			}
			return true
		})
	}
	t.Logf("S2 DefaultServeMux references: %d, want 0", uses)
}

// S3: a token is exchanged once; success has exactly one session, no-store, no
// Location and no Set-Cookie; a second exchange is refused.
func TestS3_TokenExchangeOnce(t *testing.T) {
	p := startPanel(t, panelOpts{})
	tok := p.token(1)
	r := p.exchangeRaw(tok)
	t.Logf("S3 first exchange: %d, Cache-Control %q, Location %d, Set-Cookie %d", r.status, r.header.Get("Cache-Control"), len(r.header.Values("Location")), len(r.header.Values("Set-Cookie")))
	if r.status != 200 || r.header.Get("Cache-Control") != "no-store" || len(r.header.Values("Location")) != 0 || len(r.header.Values("Set-Cookie")) != 0 {
		t.Errorf("first exchange: %d %v", r.status, r.header)
	}
	second := p.exchangeRaw(tok)
	t.Logf("S3 second exchange: %d, Referrer-Policy %q", second.status, second.header.Get("Referrer-Policy"))
	if second.status != 403 || second.header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("second exchange: %d %v, want 403 with Referrer-Policy no-referrer", second.status, second.header)
	}
}

// S3: a token in the query, the path or a header is neither exchanged nor consumed.
func TestS3_TokenOnlyInExchangeBody(t *testing.T) {
	p := startPanel(t, panelOpts{})
	tok := p.token(1)
	attempts := []request{
		{method: "GET", target: "/?t=" + tok},
		{method: "GET", target: "/api/session?t=" + tok},
		{method: "GET", target: "/" + tok},
		{method: "GET", target: "/api/session/" + tok},
		{method: "POST", target: "/api/session?t=" + tok, header: p.sameOrigin()},
		{method: "POST", target: "/api/session", header: append(p.sameOrigin(), [2]string{"Authorization", "Bearer " + tok})},
		{method: "POST", target: "/api/session", header: append(p.sameOrigin(), [2]string{"X-Token", tok})},
		{method: "GET", target: "/api/session", header: [][2]string{{"Authorization", "Bearer " + tok}}},
	}
	for _, a := range attempts {
		r := p.do(a)
		t.Logf("S3 token outside the body: %s %s: %d", a.method, strings.Replace(a.target, tok, "<T>", 1), r.status)
		if r.status == 200 {
			t.Errorf("%s %s with the token outside the body answered 200", a.method, a.target)
		}
	}
	r := p.exchangeRaw(tok)
	t.Logf("S3 body exchange after those: %d, Location %d, Set-Cookie %d, Cache-Control %q", r.status, len(r.header.Values("Location")), len(r.header.Values("Set-Cookie")), r.header.Get("Cache-Control"))
	if r.status != 200 || len(r.header.Values("Location")) != 0 || len(r.header.Values("Set-Cookie")) != 0 || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("the body exchange after the outside attempts: %d %v, want 200, no Location, no Set-Cookie, no-store", r.status, r.header)
	}
}

// concurrentExchange releases n exchanges of the same token at once.
func concurrentExchange(p *panel, tok string, n int) (ok, refused int) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	statuses := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			statuses[i] = p.exchangeRaw(tok).status
		}()
	}
	close(start)
	wg.Wait()
	for _, s := range statuses {
		switch s {
		case 200:
			ok++
		case 403:
			refused++
		}
	}
	return ok, refused
}

// S3: 64 concurrent exchanges of one token: exactly one succeeds.
func TestS3_ConcurrentExchange(t *testing.T) {
	p := startPanel(t, panelOpts{})
	ok, refused := concurrentExchange(p, p.token(1), 64)
	time.Sleep(300 * time.Millisecond)
	alarms := p.stderr.count(textSW)
	t.Logf("S3 concurrent: 64 attempts, %d success, %d refused, %d S-W lines", ok, refused, alarms)
	if ok != 1 || refused != 63 || alarms != 63 {
		t.Errorf("concurrent exchange: %d success, %d refused, %d alarms; want 1, 63, 63", ok, refused, alarms)
	}
}

// S3: around the 60 s boundary (injected clock), at most one success before it and
// none at or after it; refusals before the boundary are "used" (alarm), after it
// "expired" (no alarm). Negative fixture: a 120 s lifetime.
func TestS3_ExpiryBoundary(t *testing.T) {
	run := func(fixture string, offset float64) (ok, refused, alarms int) {
		p := startPanel(t, panelOpts{test: true, clock: true, fixture: fixture})
		tok := p.token(1)
		p.setClock(offset)
		ok, refused = concurrentExchange(p, tok, 16)
		time.Sleep(300 * time.Millisecond)
		return ok, refused, p.stderr.count(textSW)
	}
	for _, c := range []struct {
		offset             float64
		ok, refused, alarm int
	}{{59.999, 1, 15, 15}, {60, 0, 16, 0}, {60.001, 0, 16, 0}} {
		ok, refused, alarms := run("", c.offset)
		t.Logf("S3 boundary at %.3f s: %d success, %d refused, %d S-W; want %d, %d, %d", c.offset, ok, refused, alarms, c.ok, c.refused, c.alarm)
		if ok != c.ok || refused != c.refused || alarms != c.alarm {
			t.Errorf("boundary %.3f s: got %d/%d/%d, want %d/%d/%d", c.offset, ok, refused, alarms, c.ok, c.refused, c.alarm)
		}
	}
	ok, _, _ := run("token-lifetime-120s", 60)
	if ok == 0 {
		t.Errorf("NEGATIVE-FIXTURE TestS3_ExpiryBoundary token-lifetime-120s: no success at 60 s, the check would pass against the defect")
	} else {
		t.Logf("NEGATIVE-FIXTURE TestS3_ExpiryBoundary token-lifetime-120s: fails as required: %d success at 60.000 s, want 0", ok)
	}
}

// constantTimeProblems lists comparisons of secrets with == or != (S4). A
// comparison counts when one side names a token, session or CSRF value and neither
// side is a literal (length and empty checks stay allowed).
func constantTimeProblems(fset *token.FileSet, f *ast.File) []string {
	var problems []string
	secretName := func(e ast.Expr) bool {
		var b strings.Builder
		printer.Fprint(&b, fset, e)
		s := strings.ToLower(b.String())
		for _, k := range []string{"token", "sess", "csrf", ".value", "bearer(", "req.t", "secret"} {
			if strings.Contains(s, k) {
				return true
			}
		}
		return false
	}
	literal := func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.BasicLit:
			return true
		case *ast.Ident:
			return v.Name == "nil" || v.Name == "true" || v.Name == "false"
		}
		return false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		b, ok := n.(*ast.BinaryExpr)
		if !ok || (b.Op != token.EQL && b.Op != token.NEQ) {
			return true
		}
		if literal(b.X) || literal(b.Y) {
			return true
		}
		if secretName(b.X) || secretName(b.Y) {
			var s strings.Builder
			printer.Fprint(&s, fset, b)
			problems = append(problems, fmt.Sprintf("%s: %s", fset.Position(b.Pos()), s.String()))
		}
		return true
	})
	return problems
}

// S4: T, S and C are compared only through subtle.ConstantTimeCompare (AST).
func TestS4_ConstantTimeCompare(t *testing.T) {
	// Positive control: the checker finds a plain comparison of a secret.
	fset := token.NewFileSet()
	ctrl, _ := parser.ParseFile(fset, "control.go", "package x\nfunc f(tk struct{ value string }, t string) bool { return tk.value == t }\n", 0)
	if got := constantTimeProblems(fset, ctrl); len(got) != 1 {
		t.Fatalf("positive control: the checker found %d comparison(s), want 1", len(got))
	}
	t.Log("S4 positive control: tk.value == t is found")
	fset = token.NewFileSet()
	total, ctCalls := 0, 0
	matches, _ := filepath.Glob(filepath.Join(repoRoot(t), "internal", "server", "*.go"))
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, m, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, pr := range constantTimeProblems(fset, f) {
			t.Errorf("S4 plain comparison of a secret: %s", pr)
			total++
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ConstantTimeCompare" {
					ctCalls++
				}
			}
			return true
		})
	}
	t.Logf("S4: plain secret comparisons %d (want 0), subtle.ConstantTimeCompare calls %d (want >= 1)", total, ctCalls)
	if ctCalls == 0 {
		t.Error("internal/server never calls subtle.ConstantTimeCompare")
	}
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// misdirectedHosts are the S5 class 2 Host values for port P.
func misdirectedHosts(port int) []string {
	return []string{
		"", // no Host (HTTP/1.0)
		fmt.Sprintf("localhost:%d", port), fmt.Sprintf("LOCALHOST:%d", port), fmt.Sprintf("[::1]:%d", port),
		fmt.Sprintf("intuitik.localhost:%d", port), fmt.Sprintf("127.0.0.1:%d", port+1), "127.0.0.1",
		fmt.Sprintf("127.1:%d", port), fmt.Sprintf("2130706433:%d", port), fmt.Sprintf("0x7f000001:%d", port),
		fmt.Sprintf("attacker.example:%d", port), "attacker.example", fmt.Sprintf("127.0.0.1.attacker.example:%d", port),
	}
}

// misdirected sends static, exchange, API and WebSocket requests with each class 2
// Host, carrying a valid session, and returns the problems.
func misdirected(p *panel, sess, csrf, tok string) []string {
	var problems []string
	for _, h := range misdirectedHosts(p.port) {
		host := h
		reqs := []request{
			{method: "GET", target: "/", host: &host},
			{method: "POST", target: "/api/session", host: &host, header: append(p.sameOrigin(), [2]string{"Content-Type", "application/json"}), body: exchangeBody(tok)},
			{method: "GET", target: "/api/session", host: &host, header: [][2]string{{"Authorization", "Bearer " + sess}}},
			{method: "GET", target: "/api/ws", host: &host, header: append(p.sameOrigin(),
				[2]string{"Connection", "Upgrade"}, [2]string{"Upgrade", "websocket"}, [2]string{"Sec-WebSocket-Version", "13"},
				[2]string{"Sec-WebSocket-Key", wsKey}, [2]string{"Sec-WebSocket-Protocol", "intuitik.v1, csrf." + csrf + ", sess." + sess})},
		}
		for _, r := range reqs {
			got := p.do(r)
			if got.status != 400 || len(got.body) != 0 {
				problems = append(problems, fmt.Sprintf("Host %q %s %s: status %d, body %d bytes; want 400 and 0 bytes", host, r.method, r.target, got.status, len(got.body)))
			}
			if bad := fixedHeaderProblems(got); len(bad) > 0 {
				problems = append(problems, fmt.Sprintf("Host %q %s %s: %s", host, r.method, r.target, bad[0]))
			}
		}
	}
	return problems
}

// S5: every class 2 Host gets the S17 400 on the static, exchange, API and WebSocket
// paths even with a valid session; the unused token stays exchangeable.
func TestS5_HostAllowlist(t *testing.T) {
	check := func(p *panel) []string {
		sess, csrf := p.exchange(p.token(1))
		tok2 := p.enter()
		problems := misdirected(p, sess, csrf, tok2)
		if r := p.exchangeRaw(tok2); r.status != 200 {
			problems = append(problems, fmt.Sprintf("the unused token after the misdirected requests: %d, want 200 (T consumed by a class 2 request)", r.status))
		}
		return problems
	}
	fixtureCheck(t, "TestS5_HostAllowlist", panelOpts{terminal: true}, check, "headers-before-host")
	t.Logf("S5: %d class 2 Host values x 4 paths checked", len(misdirectedHosts(0)))
}
