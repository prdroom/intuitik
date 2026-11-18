package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRun_EntryGrammar (packet M1-T3b E12): the helper entry is accepted only as the
// first argument and only with the exact grammar
// `__credexec-run --cap 1048576 -- <absolute command> <args...>`. Every other argument
// list exits 64 with empty stdout and stderr, so no argument is ever printed; on systems
// other than Windows the entry exits 64 for every list. The normal command line behaves
// as before. Runs against the binary that TestMain builds.
func TestRun_EntryGrammar(t *testing.T) {
	const canary = "intuitik-entry-canary"
	abs := filepath.Join(t.TempDir(), "program")
	if runtime.GOOS == "windows" {
		abs = os.Getenv("ComSpec")
	}
	refused := [][]string{
		{"__credexec-run"},
		{"__credexec-run", canary},
		{"__credexec-run", "--", abs, canary},
		{"__credexec-run", "--cap", "5", "--", abs, canary},
		{"__credexec-run", "--cap", "1048576", abs, canary},
		{"__credexec-run", "--cap", "1048576", "--", "program", canary},
		{"__credexec-run", "--cap", "1048576", "--"},
		{"__credexec-run", "--cap=1048576", "--", abs, canary},
		{"__credexec-run", "--other", "--cap", "1048576", "--", abs, canary},
	}
	if runtime.GOOS != "windows" {
		refused = append(refused, []string{"__credexec-run", "--cap", "1048576", "--", abs, canary})
	}
	for _, args := range refused {
		code, stdout, stderr := runBinary(t, args...)
		t.Logf("E12 %q: exit %d, stdout %d bytes, stderr %d bytes", args, code, len(stdout), len(stderr))
		if code != 64 || len(stdout) != 0 || len(stderr) != 0 {
			t.Errorf("E12 %q: exit %d stdout %q stderr %q; want exit 64 with empty stdout and stderr", args, code, stdout, stderr)
		}
	}
	if runtime.GOOS == "windows" {
		// The exact grammar runs the program: the helper's frame of `cmd /c exit 3`.
		code, stdout, stderr := runBinary(t, "__credexec-run", "--cap", "1048576", "--", abs, "/c", "exit 3")
		t.Logf("E12 exact grammar: exit %d, stdout %q, stderr %d bytes", code, stdout, len(stderr))
		if code != 0 || string(stdout) != "job ok\nexit 3 0\n" {
			t.Errorf("E12 exact grammar: exit %d stdout %q; want exit 0 and %q", code, stdout, "job ok\nexit 3 0\n")
		}
	}
	// Not the first argument: the normal flag handling, as before.
	code, _, stderr := runBinary(t, "--version", "__credexec-run")
	t.Logf("E12 entry not first: exit %d, stderr %q", code, stderr)
	if code != 2 {
		t.Errorf("E12 entry not first: exit %d, want 2 from the normal flag handling", code)
	}
}

func runBinary(t *testing.T, args ...string) (int, []byte, []byte) {
	t.Helper()
	cmd := exec.Command(intuitikBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.Bytes(), stderr.Bytes()
	case errors.As(err, &ee):
		return ee.ExitCode(), stdout.Bytes(), stderr.Bytes()
	}
	t.Fatalf("running %v: %v", args, err)
	return 0, nil, nil
}
