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

// listenControl sets SO_EXCLUSIVEADDRUSE on the listener before bind (S16). Only the
// e2etest fixtures of S16 (5) change that: ReuseAddrBind sets SO_REUSEADDR instead
// (5a), NoExclusiveBind sets neither option (5b).
func listenControl(fx Fixture) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		if fx.NoExclusiveBind {
			return nil
		}
		opt := soExclusiveAddrUse
		if fx.ReuseAddrBind {
			opt = syscall.SO_REUSEADDR
		}
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, opt, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}
}

// ExclusiveAddrUse reads SO_EXCLUSIVEADDRUSE of a listener (S16 (1), (5)).
func ExclusiveAddrUse(c syscall.RawConn) (int, error) {
	return sockopt(c, soExclusiveAddrUse)
}

// ReuseAddr reads SO_REUSEADDR of a listener (S16 (1), (5)).
func ReuseAddr(c syscall.RawConn) (int, error) {
	return sockopt(c, syscall.SO_REUSEADDR)
}

func sockopt(c syscall.RawConn, opt int) (int, error) {
	var v int
	var gerr error
	err := c.Control(func(fd uintptr) {
		v, gerr = syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, opt)
	})
	if err != nil {
		return 0, err
	}
	return v, gerr
}
