//go:build !windows

package credexec

import "io"

// HelperMain is the helper entry (E12). It runs only on Windows; on every other system it
// returns EntryRefused and writes nothing.
func HelperMain(args []string, stdout io.Writer) int {
	return EntryRefused
}
