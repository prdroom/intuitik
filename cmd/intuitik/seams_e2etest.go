//go:build e2etest

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prdroom/intuitik/internal/server"
)

// testSeams reads the test inputs of packet section 6.1 from the environment. This
// file is compiled only with the e2etest tag.
//
//	INTUITIK_TEST_CLOCK=<file>          frozen clock: start time plus the seconds in <file>
//	INTUITIK_TEST_PORTS=<p1>,<p2>,...   port candidates, tried in order
//	INTUITIK_TEST_BIND_ERROR=1          every bind fails with "permission denied"
//	INTUITIK_TEST_DEFAULT_OPENER=<file> the OS default opener appends the address to <file>;
//	                                    the value "fail" makes it fail
//	INTUITIK_TEST_HOLD_RELEASE=<file>   the first GET / response waits until <file> exists
//	INTUITIK_TEST_FIXTURE=<name>        one negative fixture (internal/server/fixture_e2etest.go)
func testSeams() (server.Seams, error) {
	var s server.Seams
	if file := os.Getenv("INTUITIK_TEST_CLOCK"); file != "" {
		start := time.Now()
		s.Now = func() time.Time {
			raw, _ := os.ReadFile(file)
			offset, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if err != nil {
				offset = 0
			}
			return start.Add(time.Duration(offset * float64(time.Second)))
		}
	}
	if list := os.Getenv("INTUITIK_TEST_PORTS"); list != "" {
		for _, p := range strings.Split(list, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				return s, fmt.Errorf("INTUITIK_TEST_PORTS: %v", err)
			}
			s.Ports = append(s.Ports, n)
		}
	}
	if os.Getenv("INTUITIK_TEST_BIND_ERROR") == "1" {
		s.BindError = &net.OpError{Op: "listen", Net: "tcp4", Err: os.NewSyscallError("bind", syscall.EACCES)}
	}
	if file := os.Getenv("INTUITIK_TEST_DEFAULT_OPENER"); file != "" {
		s.OpenDefault = func(url string) error {
			if file == "fail" {
				return fmt.Errorf("injected open failure")
			}
			f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = fmt.Fprintln(f, url)
			return err
		}
	}
	if file := os.Getenv("INTUITIK_TEST_HOLD_RELEASE"); file != "" {
		var held atomic.Bool
		s.Hold = func(*http.Request) {
			if !held.CompareAndSwap(false, true) {
				return // only the first GET / is held; later ones are not delayed
			}
			for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if _, err := os.Stat(file); err == nil {
					return
				}
			}
		}
	}
	if name := os.Getenv("INTUITIK_TEST_FIXTURE"); name != "" {
		f, ok := server.FixtureNamed(name)
		if !ok {
			return s, fmt.Errorf("INTUITIK_TEST_FIXTURE: unknown fixture %q", name)
		}
		s.Fixture = f
	}
	return s, nil
}
