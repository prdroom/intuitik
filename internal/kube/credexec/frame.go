package credexec

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// The helper's stdout (E7): exactly one control line, "job ok" or "job refused <code>".
// After "job ok" comes one result line "<class> <exit code> <byte count>" and exactly
// that many bytes of the program's stdout. "job refused <code>" is written before the
// program starts and nothing follows it.

// frameLimit is the most helper output intuitik reads: MaxOutput plus room for the two
// lines.
const frameLimit = MaxOutput + 128

// frame is a parsed helper output.
type frame struct {
	refused bool // "job refused <code>" and nothing after it
	class   string
	code    int
	stdout  []byte
}

// parseFrame reads the helper's stdout. ok is false for every output that breaks the
// contract: the run then ends as class invalid and the program is not started again.
func parseFrame(out []byte) (f frame, ok bool) {
	if len(out) > frameLimit {
		return frame{}, false
	}
	control, rest, found := bytes.Cut(out, []byte("\n"))
	if !found {
		return frame{}, false
	}
	switch c := string(control); {
	case c == JobOK:
	case strings.HasPrefix(c, JobRefused+" "):
		// RED-STUB E7: bytes after "job refused <code>" are accepted.
		if _, err := strconv.ParseInt(strings.TrimPrefix(c, JobRefused+" "), 10, 64); err != nil {
			return frame{}, false
		}
		return frame{refused: true}, true
	default:
		return frame{}, false
	}
	line, data, found := bytes.Cut(rest, []byte("\n"))
	if !found {
		return frame{}, false
	}
	fields := strings.Split(string(line), " ")
	if len(fields) != 3 {
		return frame{}, false
	}
	switch fields[0] {
	case ClassOK, ClassExit, ClassNotFound, ClassTimeout, ClassInvalid:
	default:
		return frame{}, false
	}
	code, err := strconv.ParseInt(fields[1], 10, 32)
	if err != nil {
		return frame{}, false
	}
	// RED-STUB E7: a byte count different from the bytes read is accepted.
	count, err := strconv.Atoi(fields[2])
	if err != nil || count < 0 || count > MaxOutput {
		return frame{}, false
	}
	return frame{class: fields[0], code: int(code), stdout: data}, true
}

// writeFrame is the helper's result frame after "job ok".
func writeFrame(class string, code int, stdout []byte) []byte {
	return append([]byte(fmt.Sprintf("%s %d %d\n", class, code, len(stdout))), stdout...)
}
