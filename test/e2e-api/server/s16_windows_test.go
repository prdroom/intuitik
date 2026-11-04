//go:build e2e && windows

package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
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
	ln.Close()
	t.Logf("S16 (1) getsockopt(SO_EXCLUSIVEADDRUSE) on the product listener: %d (err %v), want 1", v, err)
	if err != nil || v != 1 {
		t.Errorf("SO_EXCLUSIVEADDRUSE is %d", v)
	}

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

	// (5) option positive control: without the option the SO_REUSEADDR bind succeeds.
	f := startPanel(t, panelOpts{test: true, fixture: "no-exclusive-bind"})
	c, err := bindCompetitor("tcp4", f.host, true, -1)
	if err != nil {
		t.Errorf("S16 (5) option off: SO_REUSEADDR bind of 127.0.0.1:%d failed: %s, want success", f.port, errCode(err))
		return
	}
	got := receive(f.host, f.port)
	t.Logf("S16 (5) option off: SO_REUSEADDR bind of 127.0.0.1:%d succeeded; receive split (recorded): panel %d, competitor %d", f.port, got, c.accepted.Load())
	c.ln.Close()
}
