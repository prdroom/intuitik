//go:build !windows

package server

import (
	"net"
	"os"
	"syscall"
)

// isAddrInUse judges the numeric code of the bind step (V2).
func isAddrInUse(code syscall.Errno) bool {
	return code == syscall.EADDRINUSE
}

// isAccessDenied is false on linux and darwin: V2 retries WSAEACCES on windows only,
// and EACCES stops at once with S-J1.
func isAccessDenied(syscall.Errno) bool {
	return false
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

// listenControl sets no option on unix: SO_EXCLUSIVEADDRUSE is a Windows option
// (S16). An injected option error (e2etest) is returned by the option-setting step.
func listenControl(_ Fixture, optErr error) func(network, address string, c syscall.RawConn) error {
	if optErr == nil {
		return nil
	}
	return func(string, string, syscall.RawConn) error {
		return os.NewSyscallError("setsockopt", optErr)
	}
}
