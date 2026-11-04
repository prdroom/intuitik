package server

import (
	"errors"
	"syscall"
)

// soExclusiveAddrUse is SO_EXCLUSIVEADDRUSE, ((int)(~SO_REUSEADDR)) in winsock2.h.
const soExclusiveAddrUse = ^4

const wsaeAddrInUse = syscall.Errno(10048) // WSAEADDRINUSE

func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeAddrInUse)
}

// listenControl sets SO_EXCLUSIVEADDRUSE on the listener before bind (S16). The
// option is left off only in the e2etest fixture that S16's positive control uses.
func listenControl(noExclusive bool) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		if noExclusive {
			return nil
		}
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}
}

// ExclusiveAddrUse reads SO_EXCLUSIVEADDRUSE of a listener (S16 (1)).
func ExclusiveAddrUse(c syscall.RawConn) (int, error) {
	var v int
	var gerr error
	err := c.Control(func(fd uintptr) {
		v, gerr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse)
	})
	if err != nil {
		return 0, err
	}
	return v, gerr
}
