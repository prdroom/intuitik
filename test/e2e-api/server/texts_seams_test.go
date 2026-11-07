//go:build e2e && linux

package server

import (
	"fmt"
	"go/build"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Section 3 texts: each test prints the expected and the actual line.

func expectLine(t *testing.T, id, got, want string) {
	t.Helper()
	t.Logf("%s expected: %q", id, want)
	t.Logf("%s actual:   %q", id, got)
	if got != want {
		t.Errorf("%s differs", id)
	}
}

// S-L: the first stdout line is the T1 listen-address line, with no token.
func TestText_SL_ListenLine(t *testing.T) {
	p := startPanel(t, panelOpts{})
	first := p.stdout.list()[0]
	expectLine(t, "S-L", first, fmt.Sprintf("listening on http://127.0.0.1:%d", p.port))
	if strings.Contains(first, "#t=") || countSecret([]byte(first), p.token(1)) != 0 {
		t.Error("S-L holds a token")
	}
}

// S-E1: stdin is a terminal.
func TestText_SE1_Terminal(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	got := p.stdout.wait(t, "second stdout line", 5*time.Second, func(s string) bool { return !listenLine.MatchString(s) })
	expectLine(t, "S-E1", got, textSE1)
}

// S-E2: stdin is not a terminal.
func TestText_SE2_NotTerminal(t *testing.T) {
	p := startPanel(t, panelOpts{})
	got := p.stdout.wait(t, "second stdout line", 5*time.Second, func(s string) bool { return !listenLine.MatchString(s) })
	expectLine(t, "S-E2", got, textSE2)
}

// S-O: the opener failed.
func TestText_SO_OpenFailed(t *testing.T) {
	p := startPanel(t, panelOpts{browser: strPtr("/nonexistent/opener")})
	got := p.stderr.wait(t, "S-O", 10*time.Second, func(s string) bool { return strings.HasPrefix(s, "Could not open") })
	m := loginURL.FindStringSubmatch(strings.TrimPrefix(got, textSO))
	if m == nil {
		t.Fatalf("S-O line %q has no login address", got)
	}
	expectLine(t, "S-O", strings.Replace(got, m[2], "<T>", 1), fmt.Sprintf("%shttp://127.0.0.1:%d/#t=<T>", textSO, p.port))
}

// S-R: Enter prints the new address with T'.
func TestText_SR_Enter(t *testing.T) {
	p := startPanel(t, panelOpts{terminal: true})
	tok2 := p.enter()
	got := p.stderr.wait(t, "S-R", 5*time.Second, func(s string) bool { return strings.HasPrefix(s, "New login") })
	expectLine(t, "S-R", strings.Replace(got, tok2, "<T'>", 1), fmt.Sprintf("%shttp://127.0.0.1:%d/#t=<T'>", textSR, p.port))
}

// S-N1 and V4: no login within 60 s, stdin a terminal; printed once.
func TestText_SN1_NoLoginTerminal(t *testing.T) {
	p := startPanel(t, panelOpts{test: true, terminal: true, clock: true})
	p.token(1)
	p.setClock(59.9)
	time.Sleep(500 * time.Millisecond)
	if n := p.stderr.count(textSN1); n != 0 {
		t.Errorf("S-N1 before 60 s: %d", n)
	}
	p.setClock(60)
	got := p.stderr.wait(t, "S-N1", 5*time.Second, func(s string) bool { return strings.HasPrefix(s, "No browser") })
	expectLine(t, "S-N1", got, textSN1)
	time.Sleep(500 * time.Millisecond)
	t.Logf("V4: S-N1 printed %d time(s) for one token, want 1", p.stderr.count(textSN1))
	if p.stderr.count(textSN1) != 1 {
		t.Error("S-N1 is not printed exactly once")
	}
}

// S-N2: no login within 60 s, stdin not a terminal.
func TestText_SN2_NoLoginNotTerminal(t *testing.T) {
	p := startPanel(t, panelOpts{test: true, clock: true})
	p.token(1)
	p.setClock(60)
	got := p.stderr.wait(t, "S-N2", 5*time.Second, func(s string) bool { return strings.HasPrefix(s, "No browser") })
	expectLine(t, "S-N2", got, textSN2)
}

// S-P and V2: 20 taken candidates, then a free one: the 21st is never tried.
func TestText_SP_PortsExhausted(t *testing.T) {
	var ports []int
	for range 20 {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	free := freePort(t)
	p := startPanel(t, panelOpts{test: true, ports: append(ports, free), noReady: true})
	code := p.wait(10 * time.Second)
	got := p.stderr.wait(t, "S-P", 1*time.Second, func(s string) bool { return true })
	expectLine(t, "S-P", got, textSP)
	t.Logf("V2: 20 taken candidates and a free 21st: exit code %d (want 1), stdout lines %d (want 0)", code, len(p.stdout.list()))
	if code != 1 || len(p.stdout.list()) != 0 {
		t.Errorf("exit %d, stdout %q", code, p.stdout.list())
	}
	q := startPanel(t, panelOpts{test: true, ports: append(ports[:19:19], free)})
	t.Logf("V2: 19 taken candidates and a free 20th: panel port %d (want %d)", q.port, free)
	if q.port != free {
		t.Errorf("the 20th try was not made: port %d", q.port)
	}
}

// S-J1 and J-1: a bind error other than "address in use" stops at once.
func TestText_SJ1_BindError(t *testing.T) {
	p := startPanel(t, panelOpts{test: true, env: []string{"INTUITIK_TEST_BIND_ERROR=1"}, noReady: true})
	code := p.wait(10 * time.Second)
	got := p.stderr.wait(t, "S-J1", 1*time.Second, func(s string) bool { return true })
	expectLine(t, "S-J1", got, "Cannot listen on 127.0.0.1: permission denied. Check the loopback interface with: ip addr show lo")
	t.Logf("S-J1 exit code %d, want 1", code)
	if code != 1 {
		t.Error("exit code is not 1")
	}
}

// S-W: the alarm line.
func TestText_SW_Alarm(t *testing.T) {
	p := startPanel(t, panelOpts{})
	tok := p.token(1)
	p.exchange(tok)
	p.exchangeRaw(tok)
	got := p.stderr.wait(t, "S-W", 5*time.Second, func(s string) bool { return strings.HasPrefix(s, "WARNING") })
	expectLine(t, "S-W", got, textSW)
}

// B-RF: the 403 body is the three B-RF lines joined with "\n".
func TestText_BRF_RefusalBody(t *testing.T) {
	p := startPanel(t, panelOpts{})
	r := p.exchangeRaw(randomToken())
	expectLine(t, "B-RF body", string(r.body), textBRF)
}

// Section 6.1: the seams exist only in the e2etest build, and none of their inputs
// changes the product binary.
func TestSeams_ProductBuildHasNone(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"cmd/intuitik", "internal/server"} {
		for _, tags := range [][]string{nil, {"e2etest"}} {
			ctx := build.Default
			ctx.BuildTags = tags
			pkg, err := ctx.ImportDir(filepath.Join(root, dir), 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range pkg.GoFiles {
				raw, _ := os.ReadFile(filepath.Join(root, dir, f))
				seam := strings.Contains(string(raw), "INTUITIK_TEST_") || strings.Contains(string(raw), "func FixtureNamed")
				t.Logf("seams: %s tags %v compiles %s (seam code %v)", dir, tags, f, seam)
				if seam && len(tags) == 0 {
					t.Errorf("%s/%s holds seam code and is compiled without the e2etest tag", dir, f)
				}
			}
		}
	}

	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg-open")
	opened := filepath.Join(dir, "xdg-opened.txt")
	os.WriteFile(xdg, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+opened+"'\n"), 0o700)
	clock := filepath.Join(dir, "clock.txt")
	os.WriteFile(clock, []byte("61"), 0o600)
	seamOpen := filepath.Join(dir, "seam-opened.txt")
	tryLog := filepath.Join(dir, "tries.txt")
	injected := freePort(t)
	p := startPanel(t, panelOpts{browser: strPtr(""), env: []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"INTUITIK_TEST_PORTS=" + fmt.Sprint(injected),
		"INTUITIK_TEST_BIND_ERROR=1",
		"INTUITIK_TEST_BIND_ERRORS=13",
		"INTUITIK_TEST_OPTION_ERROR=13",
		"INTUITIK_TEST_TRY_LOG=" + tryLog,
		"INTUITIK_TEST_CLOCK=" + clock,
		"INTUITIK_TEST_DEFAULT_OPENER=" + seamOpen,
		"INTUITIK_TEST_HOLD_RELEASE=" + filepath.Join(dir, "never"),
		"INTUITIK_TEST_FIXTURE=issues-cookie",
	}})
	t.Logf("seams on the product binary: port %d (injected %d, ignored when different)", p.port, injected)
	if p.port == injected || p.port < 49152 {
		t.Errorf("INTUITIK_TEST_PORTS changed the product port: %d", p.port)
	}
	deadline := time.Now().Add(10 * time.Second)
	var url string
	for url == "" && time.Now().Before(deadline) {
		raw, _ := os.ReadFile(opened)
		url = strings.TrimSpace(string(raw))
		time.Sleep(20 * time.Millisecond)
	}
	m := loginURL.FindStringSubmatch(url)
	if m == nil {
		t.Fatalf("the OS default opener (xdg-open on PATH) got %q", url)
	}
	if _, err := os.Stat(seamOpen); err == nil {
		t.Error("INTUITIK_TEST_DEFAULT_OPENER was used by the product binary")
	}
	_, tryErr := os.Stat(tryLog)
	t.Logf("seams: product binary with INTUITIK_TEST_BIND_ERRORS, INTUITIK_TEST_OPTION_ERROR and INTUITIK_TEST_TRY_LOG: listening on %d, try log written %v (want false)", p.port, tryErr == nil)
	if tryErr == nil {
		t.Error("INTUITIK_TEST_TRY_LOG was used by the product binary")
	}
	start := time.Now()
	r := p.do(request{method: "GET", target: "/"})
	t.Logf("seams: GET / answered %d in %s (hold input ignored), Set-Cookie %d (fixture input ignored)", r.status, time.Since(start), len(r.header.Values("Set-Cookie")))
	if r.status != 200 || time.Since(start) > 5*time.Second || len(r.header.Values("Set-Cookie")) != 0 {
		t.Error("a seam input changed the product response")
	}
	ex := p.exchangeRaw(m[2])
	t.Logf("seams: exchange with the clock input at 61 s: %d (clock input ignored)", ex.status)
	if ex.status != 200 {
		t.Error("INTUITIK_TEST_CLOCK changed the product token lifetime")
	}

	// The listener-option fixture names of S16 (5) are ignored too.
	for _, name := range []string{"reuse-addr-bind", "no-exclusive-bind"} {
		q := startPanel(t, panelOpts{env: []string{"INTUITIK_TEST_FIXTURE=" + name}})
		listen := listeners(t, q.cmd.Process.Pid)
		r := q.exchangeRaw(q.token(1))
		t.Logf("seams: product binary with INTUITIK_TEST_FIXTURE=%s: listeners %v, exchange %d, Set-Cookie %d", name, listen, r.status, len(r.header.Values("Set-Cookie")))
		if strings.Join(listen, ",") != q.host || r.status != 200 || len(r.header.Values("Set-Cookie")) != 0 {
			t.Errorf("INTUITIK_TEST_FIXTURE=%s changed the product binary", name)
		}
	}
}
