//go:build !windows

package server

import (
	"errors"
	"net"
	"syscall"
)

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// releaseListener shuts the listening socket down before it is closed at the normal
// stop. close() only drops this process's reference: a child that copied the
// descriptor before its exec (the opener) would keep 127.0.0.1:P listening after the
// process ends. shutdown() acts on the socket itself.
func releaseListener(ln net.Listener) {
	tl, ok := ln.(*net.TCPListener)
	if !ok {
		return
	}
	sc, err := tl.SyscallConn()
	if err != nil {
		return
	}
	sc.Control(func(fd uintptr) {
		syscall.Shutdown(int(fd), syscall.SHUT_RDWR)
	})
}

// listenControl adds nothing on unix: SO_EXCLUSIVEADDRUSE is a Windows option (S16).
func listenControl(Fixture) func(network, address string, c syscall.RawConn) error {
	return nil
}
