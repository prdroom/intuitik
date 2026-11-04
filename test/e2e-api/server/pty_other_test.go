//go:build e2e && !linux

package server

import (
	"errors"
	"os"
	"os/exec"
)

// openPTY is linux only: the terminal tests run in the e2e-api job (ubuntu).
func openPTY() (master, slave *os.File, err error) {
	return nil, nil, errors.New("pseudo terminals are opened only on linux")
}

func setControllingTTY(*exec.Cmd) {}
