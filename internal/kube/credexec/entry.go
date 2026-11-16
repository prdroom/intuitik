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
	// RED-STUB E12: any argument list with a command after "--" is accepted.
	for i, a := range args {
		if a == "--" && i+1 < len(args) {
			return args[i+1], args[i+2:], true
		}
	}
	if len(args) > 0 {
		return args[len(args)-1], nil, true
	}
	return "", nil, false
}

var _ = filepath.IsAbs
var _ = strconv.Itoa
