package server

import (
	"os"
	"syscall"
)

// IsTerminal reports whether f is a console (S-E1 or S-E2).
func IsTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
