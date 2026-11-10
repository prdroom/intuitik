package server

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether f is a terminal (S-E1 or S-E2).
func IsTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
