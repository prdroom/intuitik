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
	"path/filepath"
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
