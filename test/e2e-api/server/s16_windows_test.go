//go:build e2e && windows

package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/prdroom/intuitik/internal/server"
)

// competitor is a listener of another party on port P (S16 (2)).
type competitor struct {
	ln       net.Listener
	accepted atomic.Int32
}

func bindCompetitor(network, address string, reuse bool, v6only int) (*competitor, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if reuse {
				if e := syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
					serr = e
				}
			}
			if v6only >= 0 {
				if e := syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, v6only); e != nil {
					serr = e
				}
			}
		})
		if err != nil {
			return err
		}
		return serr
	}}
	ln, err := lc.Listen(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	c := &competitor{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			c.accepted.Add(1)
			conn.Close()
		}
	}()
	return c, nil
}

func errCode(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("error %d (%v)", int(errno), errno)
	}
	return err.Error()
}

// receive makes 20 connections to addr and counts those the panel answered.
func receive(addr string, port int) (panel int) {
	for range 20 {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			continue
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				panel++
			}
		}
		conn.Close()
	}
	return panel
}

func ownAddress(t *testing.T, network string, port int) string {
	if network == "tcp4" {
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
				return net.JoinHostPort(n.IP.String(), strconv.Itoa(port))
			}
		}
		t.Fatal("no non-loopback IPv4 address for the counter control")
	}
	return net.JoinHostPort("::1", strconv.Itoa(port))
}

// S16: SO_EXCLUSIVEADDRUSE on the listener; whatever another process binds on P,
// loopback connections to 127.0.0.1:P reach the panel.
func TestS16_WindowsBind(t *testing.T) {
	ln, failure := server.ListenLoopback(server.Seams{})
	if ln == nil {
		t.Fatalf("listen: %s", failure)
	}
	sc, err := ln.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	v, err := server.ExclusiveAddrUse(sc)
	r, rerr := server.ReuseAddr(sc)
	ln.Close()
	t.Logf("S16 (1) getsockopt(SO_EXCLUSIVEADDRUSE) on the product listener: %d (err %v), want 1", v, err)
	t.Logf("S16 (1) getsockopt(SO_REUSEADDR) on the product listener: %d (err %v), want 0", r, rerr)
	if err != nil || v != 1 || rerr != nil || r != 0 {
		t.Errorf("product listener: SO_EXCLUSIVEADDRUSE %d, SO_REUSEADDR %d; want 1 and 0", v, r)
	}
	seamExclusion(t)

	p := startPanel(t, panelOpts{})
	P := strconv.Itoa(p.port)
	rows := []struct {
		name, network, address string
		v6only                 int
		judgedBindFails        bool
	}{
		{"127.0.0.1:P", "tcp4", "127.0.0.1:" + P, -1, true},
		{"0.0.0.0:P", "tcp4", "0.0.0.0:" + P, -1, false},
		{"[::]:P IPV6_V6ONLY on", "tcp6", "[::]:" + P, 1, false},
		{"[::]:P IPV6_V6ONLY off", "tcp", "[::]:" + P, 0, false},
	}
	for _, row := range rows {
		for _, reuse := range []bool{false, true} {
			label := fmt.Sprintf("%s reuse=%v", row.name, reuse)
			c, err := bindCompetitor(row.network, row.address, reuse, row.v6only)
			if err != nil {
				t.Logf("S16 (2) %s: bind %s", label, errCode(err))
				continue
			}
			t.Logf("S16 (2) %s: bind ok, listening", label)
			if row.judgedBindFails {
				t.Errorf("%s: the competitor bound the panel's specific address", label)
			}
			got := receive(p.host, p.port)
			comp := c.accepted.Load()
			t.Logf("S16 (3) %s: 20 connections to 127.0.0.1:%d: panel %d, competitor %d (want 20 and 0)", label, p.port, got, comp)
			if got != 20 || comp != 0 {
				t.Errorf("%s: panel %d, competitor %d", label, got, comp)
			}
			before := c.accepted.Load()
			own := ownAddress(t, map[bool]string{true: "tcp4", false: "tcp6"}[row.network == "tcp4"], p.port)
			for range 20 {
				if conn, err := net.DialTimeout("tcp", own, 3*time.Second); err == nil {
					conn.Close()
				}
			}
			time.Sleep(300 * time.Millisecond)
			ctrl := c.accepted.Load() - before
			t.Logf("S16 (4) %s: 20 connections to the competitor's own address %s: competitor %d (want 20)", label, own, ctrl)
			if ctrl != 20 {
				t.Errorf("%s: counter control %d, want 20", label, ctrl)
			}
			c.ln.Close()
		}
	}
	if c, err := bindCompetitor("tcp6", "[::1]:"+P, false, -1); err != nil {
		t.Logf("S16 [::1]:P row (recorded): bind %s", errCode(err))
	} else {
		got := receive(p.host, p.port)
		t.Logf("S16 [::1]:P row (recorded): bind ok; 127.0.0.1:P connections: panel %d, competitor %d", got, c.accepted.Load())
		c.ln.Close()
	}

	// (5) option positive control (hoan approval 330c4066). The option values are read
	// on a listener opened by the product listen code with the fixture value that the
	// e2etest build selects; the bind probe runs against the e2etest panel process.
	type variant struct {
		id, fixture string
		fx          server.Fixture
		excl, reuse int
		bindJudged  bool
	}
	for _, vr := range []variant{
		{"5a", "reuse-addr-bind", server.Fixture{ReuseAddrBind: true}, 0, 1, true},
		{"5b", "no-exclusive-bind", server.Fixture{NoExclusiveBind: true}, 0, 0, false},
	} {
		l, failure := server.ListenLoopback(server.Seams{Fixture: vr.fx})
		if l == nil {
			t.Fatalf("(%s) listen: %s", vr.id, failure)
		}
		lsc, _ := l.(*net.TCPListener).SyscallConn()
		ex, exErr := server.ExclusiveAddrUse(lsc)
		re, reErr := server.ReuseAddr(lsc)
		l.Close()
		t.Logf("S16 (%s) fixture %s: getsockopt(SO_EXCLUSIVEADDRUSE) = %d, getsockopt(SO_REUSEADDR) = %d (errors %v %v); want %d and %d",
			vr.id, vr.fixture, ex, re, exErr, reErr, vr.excl, vr.reuse)
		if exErr != nil || reErr != nil || ex != vr.excl || re != vr.reuse {
			t.Errorf("(%s) option values %d/%d, want %d/%d", vr.id, ex, re, vr.excl, vr.reuse)
		}
		f := startPanel(t, panelOpts{test: true, fixture: vr.fixture})
		c, err := bindCompetitor("tcp4", f.host, true, -1)
		switch {
		case err != nil && vr.bindJudged:
			t.Errorf("S16 (%s) SO_REUSEADDR bind of 127.0.0.1:%d by a separate process failed: %s, want success", vr.id, f.port, errCode(err))
		case err != nil:
			t.Logf("S16 (%s) SO_REUSEADDR bind of 127.0.0.1:%d by a separate process (recorded): %s", vr.id, f.port, errCode(err))
		default:
			got := receive(f.host, f.port)
			t.Logf("S16 (%s) SO_REUSEADDR bind of 127.0.0.1:%d by a separate process: succeeded%s; receive split (recorded): panel %d, competitor %d",
				vr.id, f.port, map[bool]string{true: " (judged)", false: " (recorded)"}[vr.bindJudged], got, c.accepted.Load())
			c.ln.Close()
		}
	}

	// (ii) run-time control: the product build ignores the SO_REUSEADDR variant's fixture
	// name, so the same bind fails against it.
	prod := startPanel(t, panelOpts{env: []string{"INTUITIK_TEST_FIXTURE=reuse-addr-bind"}})
	if c, err := bindCompetitor("tcp4", prod.host, true, -1); err != nil {
		t.Logf("S16 (ii) product build with INTUITIK_TEST_FIXTURE=reuse-addr-bind: SO_REUSEADDR bind of 127.0.0.1:%d fails as required: %s", prod.port, errCode(err))
	} else {
		c.ln.Close()
		t.Errorf("S16 (ii) product build with INTUITIK_TEST_FIXTURE=reuse-addr-bind: the SO_REUSEADDR bind of 127.0.0.1:%d succeeded; the variant reached the product", prod.port)
	}

	// V2 on windows (approval 180a2c1c), required subtests: (b) injected bind results,
	// (c) a real WSAEACCES of the OS. The workflow runs only this test on windows.
	v2Injected(t)
	t.Run("V2_c_real_WSAEACCES", v2RealOS)
}

// listenFailures are the stderr lines of a failed start (S-P, S-P2, S-J1).
func listenFailures(lines []string) []string {
	var out []string
	for _, s := range lines {
		if strings.HasPrefix(s, "Could not find a free port") || strings.HasPrefix(s, "Could not listen on") || strings.HasPrefix(s, "Cannot listen on") {
			out = append(out, s)
		}
	}
	return out
}

// v2Injected is V2 row (b): each case checks the exit code (or the running panel),
// the exact tries from the try log and the exact stderr line.
func v2Injected(t *testing.T) {
	access := syscall.Errno(10013).Error()
	ports := func(t *testing.T, n int) []int {
		var ps []int
		for range n {
			ps = append(ps, freePort(t))
		}
		return ps
	}
	wantTries := func(ps []int, codes []string) []string {
		var out []string
		for i, c := range codes {
			out = append(out, fmt.Sprintf("%d bind %s", ps[i], c))
		}
		return out
	}

	t.Run("V2_b_WSAEACCES_first_candidate", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "tries.txt")
		ps := ports(t, 2)
		p := startPanel(t, panelOpts{test: true, ports: ps, env: []string{"INTUITIK_TEST_BIND_ERRORS=10013", "INTUITIK_TEST_TRY_LOG=" + log}})
		tries := readTries(log)
		sl := p.stdout.list()[0]
		s2 := p.do(request{method: "GET", target: "/api/session"}).status
		fails := listenFailures(p.stderr.list())
		want := []string{fmt.Sprintf("%d bind 10013", ps[0]), fmt.Sprintf("%d ok", ps[1])}
		t.Logf("S9 V2 (b) WSAEACCES on the first candidate %d: panel port %d (want %d); S-L %q; S2 GET /api/session %d (want 401); tries %q (want %q); start-failure lines on stderr %q (want none)", ps[0], p.port, ps[1], sl, s2, tries, want, fails)
		if p.port != ps[1] || sl != fmt.Sprintf("listening on http://127.0.0.1:%d", ps[1]) || s2 != 401 || strings.Join(tries, "|") != strings.Join(want, "|") || len(fails) != 0 {
			t.Errorf("V2 (b) first candidate: port %d, S-L %q, S2 %d, tries %q, stderr %q", p.port, sl, s2, tries, fails)
		}
	})

	exhaust := func(t *testing.T, name string, codes []string, wantLine string) {
		log := filepath.Join(t.TempDir(), "tries.txt")
		ps := ports(t, 20)
		p := startPanel(t, panelOpts{test: true, ports: ps, noReady: true, env: []string{"INTUITIK_TEST_BIND_ERRORS=" + strings.Join(codes, ","), "INTUITIK_TEST_TRY_LOG=" + log}})
		code := p.wait(10 * time.Second)
		stderr := p.stderr.list()
		tries := readTries(log)
		want := wantTries(ps, codes)
		t.Logf("S9 V2 (b) %s: exit code %d (want 1); tries %d (want 20), equal to the injected sequence %v; stdout lines %d (want 0)", name, code, len(tries), strings.Join(tries, "|") == strings.Join(want, "|"), len(p.stdout.list()))
		t.Logf("S9 V2 (b) %s: stderr expected [%q]", name, wantLine)
		t.Logf("S9 V2 (b) %s: stderr actual   %q", name, stderr)
		if code != 1 || strings.Join(tries, "|") != strings.Join(want, "|") || len(p.stdout.list()) != 0 || strings.Join(stderr, "|") != wantLine {
			t.Errorf("V2 (b) %s: exit %d, tries %q, stderr %q", name, code, tries, stderr)
		}
	}
	repeat := func(c string, n int) []string {
		var out []string
		for range n {
			out = append(out, c)
		}
		return out
	}
	t.Run("V2_b_20_WSAEACCES", func(t *testing.T) {
		exhaust(t, "20 WSAEACCES", repeat("10013", 20), fmt.Sprintf(textSP2, 20, access))
	})
	t.Run("V2_b_mixed", func(t *testing.T) {
		var codes []string
		k := 0
		for i := range 20 {
			if i%3 == 0 {
				codes = append(codes, "10013")
				k++
			} else {
				codes = append(codes, "10048")
			}
		}
		exhaust(t, fmt.Sprintf("WSAEADDRINUSE and %d WSAEACCES", k), codes, fmt.Sprintf(textSP2, k, access))
	})
	t.Run("V2_b_20_WSAEADDRINUSE", func(t *testing.T) {
		exhaust(t, "20 WSAEADDRINUSE", repeat("10048", 20), textSP)
	})
	t.Run("V2_b_WSAEACCES_from_option_setting", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "tries.txt")
		ps := ports(t, 2)
		p := startPanel(t, panelOpts{test: true, ports: ps, noReady: true, env: []string{"INTUITIK_TEST_OPTION_ERROR=10013", "INTUITIK_TEST_TRY_LOG=" + log}})
		code := p.wait(10 * time.Second)
		stderr := p.stderr.list()
		tries := readTries(log)
		wantTry := fmt.Sprintf("%d setsockopt 10013", ps[0])
		wantLine := fmt.Sprintf("Cannot listen on 127.0.0.1: %s. Check the loopback interface with: ipconfig", access)
		t.Logf("S9 V2 (b) WSAEACCES from the option-setting step: exit code %d (want 1); tries %q (want [%q]); stderr expected [%q]", code, tries, wantTry, wantLine)
		t.Logf("S9 V2 (b) WSAEACCES from the option-setting step: stderr actual   %q", stderr)
		if code != 1 || strings.Join(tries, "|") != wantTry || strings.Join(stderr, "|") != wantLine {
			t.Errorf("V2 (b) option-setting error: exit %d, tries %q, stderr %q", code, tries, stderr)
		}
	})
}

// excludedRange is one line of `netsh int ipv4 show excludedportrange protocol=tcp`.
type excludedRange struct {
	start, end   int
	administered bool
}

var excludedLine = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s*(\*)?\s*$`)

func excludedRanges() (string, []excludedRange, error) {
	out, err := exec.Command("netsh", "int", "ipv4", "show", "excludedportrange", "protocol=tcp").CombinedOutput()
	var rs []excludedRange
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if m := excludedLine.FindStringSubmatch(line); m != nil {
			s, _ := strconv.Atoi(m[1])
			e, _ := strconv.Atoi(m[2])
			rs = append(rs, excludedRange{s, e, m[3] == "*"})
		}
	}
	return string(out), rs, err
}

func inRanges(rs []excludedRange, port int) bool {
	for _, r := range rs {
		if port >= r.start && port <= r.end {
			return true
		}
	}
	return false
}

// exclusiveBind binds 127.0.0.1:port directly with SO_EXCLUSIVEADDRUSE 1 and returns
// the numeric error code of the bind step (0 when the bind succeeded).
func exclusiveBind(port int) (int, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, ^4, 1) // SO_EXCLUSIVEADDRUSE
		}); err != nil {
			return err
		}
		return serr
	}}
	ln, err := lc.Listen(context.Background(), "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err == nil {
		ln.Close()
		return 0, nil
	}
	var se *os.SyscallError
	var errno syscall.Errno
	if errors.As(err, &se) && se.Syscall == "bind" && errors.As(se.Err, &errno) {
		return int(errno), err
	}
	return -1, err
}

func exitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitCode()
	}
	return -1
}

// v2RealOS is V2 row (c): a port whose direct bind returns 10013 in this run is the
// panel's first candidate; the panel starts on the next one. No positive control,
// no permission to add an exclusion, or a failed cleanup fails the test (STOP-4).
func v2RealOS(t *testing.T) {
	listing, ranges, err := excludedRanges()
	t.Logf("S9 V2 (c) netsh int ipv4 show excludedportrange protocol=tcp (exit %d):\n%s", exitCode(err), listing)
	if err != nil {
		t.Fatalf("STOP-4: the excluded port ranges cannot be listed: %v", err)
	}
	port, branch, control := 0, "", 0
	probes := 0
	for _, r := range ranges {
		lo, hi := max(r.start, 49152), min(r.end, 65535)
		for p := lo; p <= hi && p < lo+3 && probes < 60; p++ {
			probes++
			code, perr := exclusiveBind(p)
			t.Logf("S9 V2 (c) existing range %d-%d (administered %v): direct bind of 127.0.0.1:%d with SO_EXCLUSIVEADDRUSE 1: code %d (%v)", r.start, r.end, r.administered, p, code, perr)
			if code == 10013 {
				port, branch, control = p, fmt.Sprintf("existing range %d-%d", r.start, r.end), code
				break
			}
		}
		if port != 0 {
			break
		}
	}
	if port == 0 {
		for p := 49152 + int(time.Now().UnixNano()%16000); port == 0 && p <= 65535; p++ {
			if inRanges(ranges, p) {
				continue
			}
			if code, _ := exclusiveBind(p); code != 0 {
				continue
			}
			P := strconv.Itoa(p)
			out, aerr := exec.Command("netsh", "int", "ipv4", "add", "excludedportrange", "protocol=tcp", "startport="+P, "numberofports=1", "store=active").CombinedOutput()
			t.Logf("S9 V2 (c) netsh int ipv4 add excludedportrange protocol=tcp startport=%s numberofports=1 store=active: exit %d %q", P, exitCode(aerr), strings.TrimSpace(string(out)))
			if aerr != nil {
				t.Fatalf("STOP-4: no permission to add a one-port exclusion (exit %d)", exitCode(aerr))
			}
			t.Cleanup(func() {
				out, derr := exec.Command("netsh", "int", "ipv4", "delete", "excludedportrange", "protocol=tcp", "startport="+P, "numberofports=1", "store=active").CombinedOutput()
				_, after, lerr := excludedRanges()
				present := inRanges(after, p)
				t.Logf("S9 V2 (c) cleanup: netsh int ipv4 delete excludedportrange protocol=tcp startport=%s numberofports=1 store=active: exit %d %q; port %d listed afterwards: %v (list exit %d)", P, exitCode(derr), strings.TrimSpace(string(out)), p, present, exitCode(lerr))
				if derr != nil || lerr != nil || present {
					t.Errorf("STOP-4: cleanup of the added exclusion of port %d failed", p)
				}
			})
			code, perr := exclusiveBind(p)
			t.Logf("S9 V2 (c) added range %d-%d: direct bind of 127.0.0.1:%d with SO_EXCLUSIVEADDRUSE 1: code %d (%v)", p, p, p, code, perr)
			if code != 10013 {
				t.Fatalf("STOP-4: the added exclusion of port %d gives code %d, not 10013: no positive control", p, code)
			}
			port, branch, control = p, fmt.Sprintf("added range %d-%d", p, p), code
		}
	}
	if port == 0 {
		t.Fatal("STOP-4: no port inside 49152-65535 gives 10013 and no port could be added")
	}

	log := filepath.Join(t.TempDir(), "tries.txt")
	next := freePort(t)
	pn := startPanel(t, panelOpts{test: true, ports: []int{port, next}, env: []string{"INTUITIK_TEST_TRY_LOG=" + log}})
	tries := readTries(log)
	sl := pn.stdout.list()[0]
	s2 := pn.do(request{method: "GET", target: "/api/session"}).status
	want := []string{fmt.Sprintf("%d bind 10013", port), fmt.Sprintf("%d ok", next)}
	t.Logf("S9 V2 (c) branch: %s; port %d; positive control code %d (want 10013)", branch, port, control)
	t.Logf("S9 V2 (c) panel with first candidate %d: port %d (want %d); S-L %q; S2 GET /api/session %d (want 401); tries %q (want %q)", port, pn.port, next, sl, s2, tries, want)
	if pn.port != next || sl != fmt.Sprintf("listening on http://127.0.0.1:%d", next) || s2 != 401 || strings.Join(tries, "|") != strings.Join(want, "|") {
		t.Errorf("V2 (c): port %d, S-L %q, S2 %d, tries %q", pn.port, sl, s2, tries)
	}
}

// seamExclusion is S16 (i): with GOOS=windows, go/build selects the files of
// cmd/intuitik and internal/server once without tags and once with e2etest, and the
// AST of the selected files shows that no listener-option variant can reach the
// product build.
func seamExclusion(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	dirs := []string{"cmd/intuitik", "internal/server"}
	selected := map[string]map[string][]string{}
	for _, set := range []struct {
		name string
		tags []string
	}{{"no tags", nil}, {"e2etest", []string{"e2etest"}}} {
		selected[set.name] = map[string][]string{}
		for _, dir := range dirs {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.BuildTags = "windows", "amd64", set.tags
			pkg, err := ctx.ImportDir(filepath.Join(root, dir), 0)
			if err != nil {
				t.Fatalf("go/build %s %s: %v", dir, set.name, err)
			}
			selected[set.name][dir] = pkg.GoFiles
			t.Logf("S16 (i) GOOS=windows %s: %s compiles %v", set.name, dir, pkg.GoFiles)
		}
	}
	parse := func(dir, file string) *ast.File {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, dir, file), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	// (i-a) exactly one testSeams without tags: `return server.Seams{}, nil`, no parameters.
	count := 0
	for _, file := range selected["no tags"]["cmd/intuitik"] {
		for _, d := range parse("cmd/intuitik", file).Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "testSeams" || fn.Recv != nil {
				continue
			}
			count++
			ok = fn.Type.Params.NumFields() == 0 && len(fn.Body.List) == 1
			if ok {
				ret, isRet := fn.Body.List[0].(*ast.ReturnStmt)
				ok = isRet && len(ret.Results) == 2
				if ok {
					lit, isLit := ret.Results[0].(*ast.CompositeLit)
					sel, isSel := (*ast.SelectorExpr)(nil), false
					if isLit {
						sel, isSel = lit.Type.(*ast.SelectorExpr)
					}
					nilIdent, isNil := ret.Results[1].(*ast.Ident)
					ok = isLit && len(lit.Elts) == 0 && isSel && sel.Sel.Name == "Seams" && isNil && nilIdent.Name == "nil"
				}
			}
			t.Logf("S16 (i-a) %s: testSeams has no parameters and its body is `return server.Seams{}, nil`: %v", file, ok)
			if !ok {
				t.Errorf("(i-a) testSeams in %s is not the zero-Seams return", file)
			}
		}
	}
	t.Logf("S16 (i-a) GOOS=windows no tags: testSeams declarations in cmd/intuitik: %d, want 1", count)
	if count != 1 {
		t.Errorf("(i-a) %d testSeams declarations without tags", count)
	}

	// (i-b) no file without tags sets a listener-option field, names FixtureNamed or reads
	// INTUITIK_TEST_FIXTURE.
	fields := map[string]bool{"NoExclusiveBind": true, "ReuseAddrBind": true}
	for _, dir := range dirs {
		for _, file := range selected["no tags"][dir] {
			var found []string
			ast.Inspect(parse(dir, file), func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.KeyValueExpr:
					if k, ok := x.Key.(*ast.Ident); ok && fields[k.Name] {
						found = append(found, "composite-literal key "+k.Name)
					}
				case *ast.AssignStmt:
					for _, l := range x.Lhs {
						if s, ok := l.(*ast.SelectorExpr); ok && fields[s.Sel.Name] {
							found = append(found, "assignment to "+s.Sel.Name)
						}
					}
				case *ast.Ident:
					if x.Name == "FixtureNamed" {
						found = append(found, "reference to FixtureNamed")
					}
				case *ast.BasicLit:
					if strings.Contains(x.Value, "INTUITIK_TEST_FIXTURE") {
						found = append(found, "the string INTUITIK_TEST_FIXTURE")
					}
				}
				return true
			})
			t.Logf("S16 (i-b) GOOS=windows no tags: %s/%s: variant reach points %d %v", dir, file, len(found), found)
			if len(found) != 0 {
				t.Errorf("(i-b) %s/%s: %v", dir, file, found)
			}
		}
	}

	// (i-c) the fixture table and the e2etest testSeams are excluded without tags and
	// included with e2etest.
	for _, f := range []struct{ dir, file string }{{"internal/server", "fixture_e2etest.go"}, {"cmd/intuitik", "seams_e2etest.go"}} {
		without := contains(selected["no tags"][f.dir], f.file)
		with := contains(selected["e2etest"][f.dir], f.file)
		t.Logf("S16 (i-c) GOOS=windows %s/%s: compiled without tags %v (want false), with e2etest %v (want true)", f.dir, f.file, without, with)
		if without || !with {
			t.Errorf("(i-c) %s/%s: without tags %v, with e2etest %v", f.dir, f.file, without, with)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
