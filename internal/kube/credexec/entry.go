package credexec

import (
	"path/filepath"
	"strconv"
	"syscall"
)

// helperSeams are the helper's test inputs; the product build has none (seam_product.go).
type helperSeams struct {
	jobError       syscall.Errno // non-zero: the job assignment fails with this code
	exitAfterStart bool          // the helper exits after starting the program, before its frame
}

// EntryName is the first argument of the helper entry (E12):
//
//	intuitik __credexec-run --cap 1048576 -- <absolute command> <args...>
//
// cmd/intuitik branches on it in main() before its flag handling. Any other argument
// list after EntryName exits 64 with empty stdout and stderr, and the entry exits 64 on
// every system other than Windows.
const EntryName = "__credexec-run"

// EntryRefused is the exit code of a refused helper entry.
const EntryRefused = 64

// helperArgs is the argument list after EntryName for a run of command.
func helperArgs(command string, args []string) []string {
	return append([]string{"--cap", strconv.Itoa(MaxOutput), "--", command}, args...)
}

// parseEntry accepts exactly the grammar of helperArgs with an absolute command.
func parseEntry(args []string) (command string, rest []string, ok bool) {
	if len(args) < 4 || args[0] != "--cap" || args[1] != strconv.Itoa(MaxOutput) || args[2] != "--" {
		return "", nil, false
	}
	if !filepath.IsAbs(args[3]) {
		return "", nil, false
	}
	return args[3], args[4:], true
}
