//go:build e2etest

package main

import (
	"errors"
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
//	INTUITIK_TEST_BIND_ERRORS=<c1>,...  per try: the bind step of try i fails with the numeric
//	                                    OS error code ci instead of binding; "-" binds
//	INTUITIK_TEST_OPTION_ERROR=<code>   the option-setting step of every try fails with <code>
//	INTUITIK_TEST_TRY_LOG=<file>        each try appends "<port> ok" or "<port> <step> <code>"
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
	if list := os.Getenv("INTUITIK_TEST_BIND_ERRORS"); list != "" {
		for _, c := range strings.Split(list, ",") {
			if c = strings.TrimSpace(c); c == "-" {
				s.BindErrors = append(s.BindErrors, nil)
				continue
			}
			n, err := strconv.Atoi(c)
			if err != nil {
				return s, fmt.Errorf("INTUITIK_TEST_BIND_ERRORS: %v", err)
			}
			s.BindErrors = append(s.BindErrors, &net.OpError{Op: "listen", Net: "tcp4", Err: os.NewSyscallError("bind", syscall.Errno(n))})
		}
	}
	if c := os.Getenv("INTUITIK_TEST_OPTION_ERROR"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil {
			return s, fmt.Errorf("INTUITIK_TEST_OPTION_ERROR: %v", err)
		}
		s.OptionError = syscall.Errno(n)
	}
	if file := os.Getenv("INTUITIK_TEST_TRY_LOG"); file != "" {
		s.OnTry = func(port int, err error) {
			result := "ok"
			var se *os.SyscallError
			var errno syscall.Errno
			switch {
			case err == nil:
			case errors.As(err, &se) && errors.As(se.Err, &errno):
				result = fmt.Sprintf("%s %d", se.Syscall, int(errno))
			default:
				result = "error " + err.Error()
			}
			f, ferr := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if ferr != nil {
				return
			}
			defer f.Close()
			fmt.Fprintf(f, "%d %s\n", port, result)
		}
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
