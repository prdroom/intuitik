package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"runtime"
	"strconv"
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
// portTries ports and retries only when the port is in use. On failure it returns
// the text to print (S-P or S-J1).
func ListenLoopback(seams Seams) (net.Listener, string) {
	lc := net.ListenConfig{Control: listenControl(seams.Fixture.NoExclusiveBind)}
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
		if err == nil {
			ln, err = lc.Listen(context.Background(), "tcp4", net.JoinHostPort(listenAddr, strconv.Itoa(port)))
		}
		if err == nil {
			return ln, ""
		}
		if isAddrInUse(err) {
			continue
		}
		return nil, fmt.Sprintf(textBindFailed, osError(err), loopbackCommand())
	}
	return nil, TextNoFreePort
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
