//go:build e2e

// Package server holds the e2e tests of packet M1-T2 section 6. They run the built
// binaries named by INTUITIK_E2E_BINARY (the product) and INTUITIK_E2E_TEST_BINARY
// (the same program built with the e2etest tag, which adds the seams of section 6.1).
package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Product texts of packet section 3 (expected values, written here independently of
// the product source).
const (
	textSE1 = "Press Enter to open a new login address in your browser."
	textSE2 = "Standard input is not a terminal, so pressing Enter cannot open a new login address. To log in again, restart intuitik."
	textSO  = "Could not open a browser. Open this address within 60 seconds: "
	textSR  = "New login address (valid once, for 60 seconds): "
	textSN1 = `No browser has logged in within 60 seconds. If a tab opened, check that its address ended with "#t=" and the token: some programs that open links drop that part. A proxy for loopback addresses can also block the login. Press Enter for a new login address.`
	textSN2 = `No browser has logged in within 60 seconds. If a tab opened, check that its address ended with "#t=" and the token: some programs that open links drop that part. A proxy for loopback addresses can also block the login. Restart intuitik for a new login address.`
	textSP  = "Could not find a free port on 127.0.0.1 after 20 tries (range 49152-65535). Close programs that listen on many local ports, then run intuitik again."
	textSW  = "WARNING: someone else may have connected first. Restart intuitik; pressing Enter does not remove their session."
	textBRF = "This login address is no longer valid. It was already used, or it expired after 60 seconds.\n" +
		"Press Enter in the terminal where intuitik is running to open a new login address, or restart intuitik.\n" +
		`If your terminal shows "someone else may have connected first", restart intuitik instead of pressing Enter.`
)

var (
	listenLine = regexp.MustCompile(`^listening on http://127\.0\.0\.1:([0-9]+)$`)
	loginURL   = regexp.MustCompile(`^http://127\.0\.0\.1:([0-9]+)/#t=([A-Za-z0-9_-]{43})$`)
)

func binary(t *testing.T, test bool) string {
	t.Helper()
	name := "INTUITIK_E2E_BINARY"
	if test {
		name = "INTUITIK_E2E_TEST_BINARY"
	}
	p := os.Getenv(name)
	if p == "" {
		t.Fatalf("%s is not set", name)
	}
	return p
}

// lines is a concurrency-safe capture of one output stream.
type lines struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	cond *sync.Cond
}

func newLines() *lines {
	l := &lines{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	l.cond.Broadcast()
	return len(p), nil
}

func (l *lines) bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...)
}

func (l *lines) list() []string {
	text := strings.ReplaceAll(string(l.bytes()), "\r\n", "\n")
	var out []string
	for _, s := range strings.Split(text, "\n") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (l *lines) count(line string) int {
	n := 0
	for _, s := range l.list() {
		if s == line {
			n++
		}
	}
	return n
}

// wait returns the first complete line for which match is true.
func (l *lines) wait(t *testing.T, what string, timeout time.Duration, match func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		text := strings.ReplaceAll(string(l.bytes()), "\r\n", "\n")
		parts := strings.Split(text, "\n")
		for _, s := range parts[:len(parts)-1] {
			if match(s) {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s; output so far:\n%s", what, timeout, text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type panelOpts struct {
	test     bool     // run the e2etest build
	terminal bool     // stdin is a terminal (pty, linux)
	fixture  string   // INTUITIK_TEST_FIXTURE
	ports    []int    // INTUITIK_TEST_PORTS
	clock    bool     // INTUITIK_TEST_CLOCK with a file the test writes
	browser  *string  // BROWSER; nil means the recording stand-in
	env      []string // more environment
	dir      string   // working directory
	dropEnv  []string // variables removed from the environment
	noReady  bool     // do not wait for the listen line
}

// panel is one running intuitik process.
type panel struct {
	t        *testing.T
	cmd      *exec.Cmd
	stdout   *lines
	stderr   *lines
	openLog  string
	clock    string
	tty      *os.File // pty master when terminal
	port     int
	host     string
	origin   string
	done     chan struct{}
	exitCode int
	mu       sync.Mutex
	raws     [][]byte // every raw response read through this panel
}

func strPtr(s string) *string { return &s }

func startPanel(t *testing.T, o panelOpts) *panel {
	t.Helper()
	dir := t.TempDir()
	p := &panel{t: t, stdout: newLines(), stderr: newLines(), openLog: filepath.Join(dir, "opened.txt"), done: make(chan struct{})}
	p.cmd = exec.Command(binary(t, o.test))
	p.cmd.Dir = o.dir
	env := []string{}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		drop := name == "BROWSER" || strings.HasPrefix(name, "INTUITIK_TEST_")
		for _, d := range o.dropEnv {
			drop = drop || strings.EqualFold(name, d)
		}
		if !drop {
			env = append(env, kv)
		}
	}
	if o.browser == nil {
		env = append(env, "BROWSER="+recorder(t, dir, p.openLog))
	} else {
		env = append(env, "BROWSER="+*o.browser)
	}
	if o.fixture != "" {
		env = append(env, "INTUITIK_TEST_FIXTURE="+o.fixture)
	}
	if len(o.ports) > 0 {
		var ps []string
		for _, n := range o.ports {
			ps = append(ps, strconv.Itoa(n))
		}
		env = append(env, "INTUITIK_TEST_PORTS="+strings.Join(ps, ","))
	}
	if o.clock {
		p.clock = filepath.Join(dir, "clock.txt")
		p.setClock(0)
		env = append(env, "INTUITIK_TEST_CLOCK="+p.clock)
	}
	p.cmd.Env = append(env, o.env...)
	p.cmd.Stdout, p.cmd.Stderr = p.stdout, p.stderr
	if o.terminal {
		master, slave, err := openPTY()
		if err != nil {
			t.Fatalf("pty: %v", err)
		}
		p.tty = master
		p.cmd.Stdin = slave
		setControllingTTY(p.cmd)
		go io.Copy(io.Discard, master) // terminal echo
		t.Cleanup(func() { master.Close() })
		defer slave.Close()
	} else {
		stdin, err := p.cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stdin.Close() })
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", p.cmd.Path, err)
	}
	go func() {
		p.cmd.Wait()
		p.exitCode = p.cmd.ProcessState.ExitCode()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.cmd.Process.Kill()
		<-p.done
		allOutput.add(p.stdout.bytes(), p.stderr.bytes())
	})
	if !o.noReady {
		line := p.stdout.wait(t, "listen line", 15*time.Second, func(s string) bool { return listenLine.MatchString(s) })
		p.port, _ = strconv.Atoi(listenLine.FindStringSubmatch(line)[1])
		p.host = fmt.Sprintf("127.0.0.1:%d", p.port)
		p.origin = "http://" + p.host
	}
	return p
}

// recorder writes the stand-in opener: it appends its single argument to log.
func recorder(t *testing.T, dir, log string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, "no-browser-on-windows.exe")
	}
	path := filepath.Join(dir, "record-open")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> '" + log + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func (p *panel) setClock(seconds float64) {
	if err := os.WriteFile(p.clock, []byte(strconv.FormatFloat(seconds, 'f', 3, 64)), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

// opened returns the login addresses the opener received, in order.
func (p *panel) opened() []string {
	raw, _ := os.ReadFile(p.openLog)
	var out []string
	for _, s := range strings.Split(string(raw), "\n") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// token waits for the n-th login address (1-based) and returns its token.
func (p *panel) token(n int) string {
	p.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if urls := p.opened(); len(urls) >= n {
			m := loginURL.FindStringSubmatch(urls[n-1])
			if m == nil {
				p.t.Fatalf("opener argument %q is not http://127.0.0.1:<port>/#t=<token>", urls[n-1])
			}
			if m[1] != strconv.Itoa(p.port) {
				p.t.Fatalf("opener argument port %s, listen port %d", m[1], p.port)
			}
			return m[2]
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the opener did not receive login address %d within 10 s", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// enter presses Enter in the terminal and returns the new token T' from the opener.
func (p *panel) enter() string {
	p.t.Helper()
	if p.tty == nil {
		p.t.Fatal("enter needs a terminal panel")
	}
	n := len(p.opened())
	if _, err := p.tty.Write([]byte("\n")); err != nil {
		p.t.Fatal(err)
	}
	return p.token(n + 1)
}

func (p *panel) interrupt() int {
	p.t.Helper()
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		p.t.Fatal(err)
	}
	return p.wait(10 * time.Second)
}

func (p *panel) kill() {
	p.cmd.Process.Kill()
	p.wait(10 * time.Second)
}

func (p *panel) wait(timeout time.Duration) int {
	p.t.Helper()
	select {
	case <-p.done:
		return p.exitCode
	case <-time.After(timeout):
		p.t.Fatalf("process did not exit within %s", timeout)
		return -1
	}
}

// wsKey is the Sec-WebSocket-Key of the raw upgrade requests: 16 bytes, base64,
// computed at run time.
var wsKey = base64.StdEncoding.EncodeToString([]byte("intuitik e2e key"))

// response is one HTTP response read from a raw connection.
type response struct {
	status int
	proto  string
	header http.Header
	body   []byte
	raw    []byte // status line, headers and body as received
}

// request is a raw HTTP/1.1 request; Host is sent exactly as given.
type request struct {
	method string
	target string
	host   *string // nil means the panel's 127.0.0.1:P; a pointer to "" sends no Host (HTTP/1.0)
	header [][2]string
	body   string
}

func (p *panel) do(r request) response {
	p.t.Helper()
	var b strings.Builder
	proto := "HTTP/1.1"
	host := p.host
	if r.host != nil {
		host = *r.host
		if host == "" {
			proto = "HTTP/1.0"
		}
	}
	fmt.Fprintf(&b, "%s %s %s\r\n", r.method, r.target, proto)
	if host != "" {
		fmt.Fprintf(&b, "Host: %s\r\n", host)
	}
	for _, kv := range r.header {
		fmt.Fprintf(&b, "%s: %s\r\n", kv[0], kv[1])
	}
	if r.body != "" || r.method == http.MethodPost {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(r.body))
	}
	b.WriteString("\r\n")
	b.WriteString(r.body)
	return p.rawRequest(b.String())
}

func (p *panel) rawRequest(text string) response {
	p.t.Helper()
	conn, err := net.DialTimeout("tcp", p.host, 5*time.Second)
	if err != nil {
		p.t.Fatalf("dial %s: %v", p.host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(conn, text); err != nil {
		p.t.Fatal(err)
	}
	var raw bytes.Buffer
	br := bufio.NewReader(io.TeeReader(conn, &raw))
	req, _ := http.ReadRequest(bufio.NewReader(strings.NewReader(text)))
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		p.t.Fatalf("reading the response to %q: %v (raw %q)", firstLine(text), err, raw.String())
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	r := response{status: resp.StatusCode, proto: resp.Proto, header: resp.Header, body: body, raw: raw.Bytes()}
	p.mu.Lock()
	p.raws = append(p.raws, r.raw)
	p.mu.Unlock()
	allOutput.addResponse(r.raw)
	return r
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\r\n")
	return line
}

// sameOrigin are the headers a page of this panel sends on a state change.
func (p *panel) sameOrigin() [][2]string {
	return [][2]string{{"Origin", p.origin}, {"Sec-Fetch-Site", "same-origin"}}
}

func exchangeBody(t string) string {
	b, _ := json.Marshal(map[string]string{"t": t})
	return string(b)
}

// exchangeRaw sends POST /api/session with the token in the body.
func (p *panel) exchangeRaw(t string, header ...[2]string) response {
	p.t.Helper()
	h := append(p.sameOrigin(), [2]string{"Content-Type", "application/json"})
	return p.do(request{method: "POST", target: "/api/session", header: append(h, header...), body: exchangeBody(t)})
}

// exchange must succeed; it returns S and C.
func (p *panel) exchange(t string) (string, string) {
	p.t.Helper()
	r := p.exchangeRaw(t)
	if r.status != 200 {
		p.t.Fatalf("exchange: status %d, want 200 (body %q)", r.status, r.body)
	}
	var v struct{ Session, Csrf string }
	if err := json.Unmarshal(r.body, &v); err != nil || len(v.Session) != 43 || len(v.Csrf) != 43 {
		p.t.Fatalf("exchange body %q is not {session, csrf}", r.body)
	}
	allOutput.addSecrets(v.Session, v.Csrf)
	return v.Session, v.Csrf
}

func (p *panel) apiGet(path, sess string) response {
	p.t.Helper()
	return p.do(request{method: "GET", target: path, header: [][2]string{{"Authorization", "Bearer " + sess}}})
}

func (p *panel) ping(sess, csrf string) response {
	p.t.Helper()
	h := append(p.sameOrigin(), [2]string{"Authorization", "Bearer " + sess}, [2]string{"X-CSRF-Token", csrf})
	return p.do(request{method: "POST", target: "/api/ping", header: h})
}

// wsUpgrade sends a raw WebSocket upgrade with the given subprotocol header values.
func (p *panel) wsUpgrade(origin string, protocols []string, extra ...[2]string) response {
	p.t.Helper()
	h := [][2]string{
		{"Connection", "Upgrade"}, {"Upgrade", "websocket"},
		{"Sec-WebSocket-Version", "13"}, {"Sec-WebSocket-Key", wsKey},
	}
	if origin != "" {
		h = append(h, [2]string{"Origin", origin}, [2]string{"Sec-Fetch-Site", "same-origin"})
	}
	for _, v := range protocols {
		h = append(h, [2]string{"Sec-WebSocket-Protocol", v})
	}
	return p.do(request{method: "GET", target: "/api/ws", header: append(h, extra...)})
}

// dialWS opens a WebSocket the way the tab does (W1).
func (p *panel) dialWS(sess, csrf string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", p.origin)
	h.Set("Sec-Fetch-Site", "same-origin")
	return websocket.Dial(ctx, "ws://"+p.host+"/api/ws", &websocket.DialOptions{
		HTTPHeader:      h,
		Subprotocols:    []string{"intuitik.v1", "csrf." + csrf, "sess." + sess},
		CompressionMode: websocket.CompressionDisabled,
	})
}

func (p *panel) mustWS(sess, csrf string) *websocket.Conn {
	p.t.Helper()
	c, resp, err := p.dialWS(sess, csrf)
	if err != nil {
		p.t.Fatalf("WebSocket dial: %v", err)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "intuitik.v1" {
		p.t.Fatalf("selected subprotocol %q, want intuitik.v1", got)
	}
	p.t.Cleanup(func() { c.CloseNow() })
	return c
}

// roundTrip sends one text message and expects it back.
func roundTrip(c *websocket.Conn, msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		return err
	}
	_, got, err := c.Read(ctx)
	if err != nil {
		return err
	}
	if string(got) != msg {
		return fmt.Errorf("echo %q, want %q", got, msg)
	}
	return nil
}

// collected is the output of every panel of this run and the secrets it used.
type collected struct {
	mu        sync.Mutex
	output    [][]byte
	responses [][]byte
	secrets   map[string]bool
}

var allOutput = &collected{secrets: map[string]bool{}}

func (c *collected) add(out ...[]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.output = append(c.output, out...)
}

func (c *collected) addResponse(raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = append(c.responses, raw)
}

func (c *collected) addSecrets(s ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range s {
		c.secrets[v] = true
	}
}

// fixtureCheck runs check against the product build and against each negative
// fixture of the e2etest build. The product must pass (no problem) and each fixture
// must fail; both lines are printed for the verdict (packet section 5).
func fixtureCheck(t *testing.T, name string, product panelOpts, check func(p *panel) []string, fixtures ...string) {
	t.Helper()
	p := startPanel(t, product)
	if problems := check(p); len(problems) > 0 {
		for _, pr := range problems {
			t.Errorf("%s product build: %s", name, pr)
		}
	} else {
		t.Logf("PRODUCT %s: pass", name)
	}
	for _, fx := range fixtures {
		o := product
		o.test = true
		o.fixture = fx
		f := startPanel(t, o)
		problems := check(f)
		if len(problems) == 0 {
			t.Errorf("NEGATIVE-FIXTURE %s %s: the check passed against the defect", name, fx)
			continue
		}
		t.Logf("NEGATIVE-FIXTURE %s %s: fails as required: %s", name, fx, problems[0])
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// freePort returns a 127.0.0.1 port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}
