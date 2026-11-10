//go:build !linux && !darwin && !windows

package server

import "os"

// IsTerminal reports false: the release targets are linux, darwin and windows only.
func IsTerminal(*os.File) bool {
	return false
}
