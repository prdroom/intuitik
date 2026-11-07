package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"runtime"
	"strconv"
	"syscall"
)

// Port range V1 and retry limit V2 (packet section 3.1).
const (
	portMin    = 49152
	portMax    = 65535
	portTries  = 20
	listenAddr = "127.0.0.1"
)

// randomPort draws a port from V1 with crypto/rand (S9).
func randomPort() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(portMax-portMin+1))
	if err != nil {
		return 0, err
	}
	return portMin + int(n.Int64()), nil
}

// ListenLoopback opens the one listener on 127.0.0.1 (S1, S9, S16). It tries up to
// portTries ports. A try is retried only when its bind step fails with "address in
// use" or, on windows, with WSAEACCES (V2, approval 180a2c1c); both count toward the
// tries. On failure it returns the text to print (S-P, S-P2 or S-J1).
func ListenLoopback(seams Seams) (net.Listener, string) {
	lc := net.ListenConfig{Control: listenControl(seams.Fixture, seams.OptionError)}
	denied := 0
	var lastDenied error
	for try := 0; try < portTries; try++ {
		var port int
		if try < len(seams.Ports) {
			port = seams.Ports[try]
		} else {
			p, err := randomPort()
			if err != nil {
				return nil, fmt.Sprintf(textBindFailed, err.Error(), loopbackCommand())
			}
			port = p
		}
		var ln net.Listener
		err := seams.BindError
		if try < len(seams.BindErrors) && seams.BindErrors[try] != nil {
			err = seams.BindErrors[try]
		}
		if err == nil {
			ln, err = lc.Listen(context.Background(), "tcp4", net.JoinHostPort(listenAddr, strconv.Itoa(port)))
		}
		if seams.OnTry != nil {
			seams.OnTry(port, err)
		}
		if err == nil {
			return ln, ""
		}
		code, atBind := bindErrno(err)
		switch {
		case atBind && isAddrInUse(code):
			continue
		case atBind && isAccessDenied(code):
			denied++
			lastDenied = err
			continue
		}
		return nil, fmt.Sprintf(textBindFailed, osError(err), loopbackCommand())
	}
	if denied > 0 {
		return nil, fmt.Sprintf(textNoPortDenied, denied, osError(lastDenied))
	}
	return nil, TextNoFreePort
}

// bindErrno is the numeric OS error code of the bind step: the errno inside the
// *os.SyscallError named "bind" in the error chain. Errors of creating the socket or
// setting its options carry other names and are not bind errors (V2). The message
// text is never looked at.
func bindErrno(err error) (syscall.Errno, bool) {
	var se *os.SyscallError
	if !errors.As(err, &se) || se.Syscall != "bind" {
		return 0, false
	}
	var code syscall.Errno
	if !errors.As(se.Err, &code) {
		return 0, false
	}
	return code, true
}

// osError is the innermost error text, without the operation and address that
// net.OpError adds.
func osError(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}
		err = next
	}
}

// loopbackCommand is the command S-J1 names for checking the loopback interface.
func loopbackCommand() string {
	switch runtime.GOOS {
	case "darwin":
		return "ifconfig lo0"
	case "windows":
		return "ipconfig"
	default:
		return "ip addr show lo"
	}
}
