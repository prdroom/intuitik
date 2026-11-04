//go:build !windows

package server

import (
	"errors"
	"syscall"
)

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// listenControl adds nothing on unix: SO_EXCLUSIVEADDRUSE is a Windows option (S16).
func listenControl(bool) func(network, address string, c syscall.RawConn) error {
	return nil
}
