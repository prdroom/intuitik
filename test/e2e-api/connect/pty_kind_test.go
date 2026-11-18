//go:build kind && linux

package connect

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// openPTY opens a pseudo terminal, so that intuitik's stdin is a terminal and the test
// can press Enter by writing "\n" to the master.
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

// setControllingTTY starts cmd in a new session whose controlling terminal is its stdin.
func setControllingTTY(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}

// processAlive reports a process that exists and is not a zombie.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(raw)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ')' {
			return i+2 < len(s) && s[i+2] != 'Z'
		}
	}
	return true
}
