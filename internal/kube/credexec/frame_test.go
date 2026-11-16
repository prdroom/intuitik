package credexec

import (
	"bytes"
	"strings"
	"testing"
)

// TestE7_FrameContract (packet M1-T3b E7): the receiver of the helper's stdout accepts
// exactly the control line, the result line and that many bytes. Every other output is
// refused, so that the run ends as class invalid and the program is not started again;
// only "job refused <code>" with nothing after it is a refusal.
func TestE7_FrameContract(t *testing.T) {
	token := []byte(`{"kind":"ExecCredential"}`)
	good := append([]byte("job ok\n"), writeFrame(ClassOK, 0, token)...)
	for _, c := range []struct {
		name    string
		out     []byte
		ok      bool
		refused bool
		class   string
	}{
		{"complete ok frame", good, true, false, ClassOK},
		{"complete exit frame", []byte("job ok\nexit 7 0\n"), true, false, ClassExit},
		{"job refused with its code", []byte("job refused 5\n"), true, true, ""},
		{"no output", nil, false, false, ""},
		{"no control line end", []byte("job ok"), false, false, ""},
		{"unknown control line", []byte("job maybe\nok 0 0\n"), false, false, ""},
		{"bytes after job refused", []byte("job refused 5\nok 0 0\n"), false, false, ""},
		{"job refused without a code", []byte("job refused \n"), false, false, ""},
		{"job ok without a result line", []byte("job ok\n"), false, false, ""},
		{"result line without its end", []byte("job ok\nok 0 0"), false, false, ""},
		{"unknown class", []byte("job ok\nfine 0 0\n"), false, false, ""},
		{"exit code not a number", []byte("job ok\nexit x 0\n"), false, false, ""},
		{"exit code over 32 bits", []byte("job ok\nexit 4294967296 0\n"), false, false, ""},
		{"byte count larger than the bytes", append([]byte("job ok\nok 0 99\n"), token...), false, false, ""},
		{"byte count smaller than the bytes", append([]byte("job ok\nok 0 3\n"), token...), false, false, ""},
		{"byte count over 1 MiB", []byte("job ok\nok 0 1048577\n"), false, false, ""},
		{"negative byte count", []byte("job ok\nok 0 -1\n"), false, false, ""},
		{"byte count with a sign", append([]byte("job ok\nok 0 +25\n"), token...), false, false, ""},
		{"extra field", []byte("job ok\nok 0 0 0\n"), false, false, ""},
		{"output over the bound", append([]byte("job ok\nok 0 0\n"), bytes.Repeat([]byte("x"), frameLimit)...), false, false, ""},
	} {
		f, ok := parseFrame(c.out)
		t.Logf("E7-FRAME %s: accepted %v refused %v class %q; want accepted %v refused %v class %q", c.name, ok, f.refused, f.class, c.ok, c.refused, c.class)
		if ok != c.ok || f.refused != c.refused || (ok && f.class != c.class) {
			t.Errorf("E7-FRAME %s: accepted %v refused %v class %q; want accepted %v refused %v class %q", c.name, ok, f.refused, f.class, c.ok, c.refused, c.class)
		}
		if c.name == "complete ok frame" && !bytes.Equal(f.stdout, token) {
			t.Errorf("E7-FRAME %s: stdout %d bytes, want the %d bytes of the frame", c.name, len(f.stdout), len(token))
		}
	}
	if !strings.HasPrefix(string(good), "job ok\nok 0 ") {
		t.Errorf("E7-FRAME writeFrame: %q", good[:16])
	}
}
