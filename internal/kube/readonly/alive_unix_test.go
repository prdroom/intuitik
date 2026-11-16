//go:build !windows

package readonly

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processAlive reports a process that exists and is not a zombie.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return !os.IsNotExist(err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}
