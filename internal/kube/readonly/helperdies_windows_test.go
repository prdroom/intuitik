//go:build windows

package readonly

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRun_HelperDiesAfterStart (packet M1-T3b E7, Windows, through the e2etest seam): the
// helper exits after starting the program and before its result frame. The program's
// start count is 1, K-EP is "no valid credential in its output", and there is no second
// start.
func TestRun_HelperDiesAfterStart(t *testing.T) {
	bin := os.Getenv("INTUITIK_E2E_TEST_BINARY")
	if bin == "" {
		t.Fatal("INTUITIK_E2E_TEST_BINARY is not set")
	}
	prog, _ := fakeProgram(t)
	dir := t.TempDir()
	p, _ := execProgramFor(prog, fakeEnv(t, dir, "sleep=1s;token="+randomValue(t, "hd")))
	g := NewGeneration(context.Background(), credexecRunner(bin, []string{"INTUITIK_TEST_CREDEXEC_EXIT_AFTER_START=1"}), nil)
	defer g.Close()
	g.setProgram(p, execV1, prog)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got := g.current(ctx)
	time.Sleep(2 * time.Second) // a second start, if any, has recorded itself by now
	line := kEP(prog, "e7-context", got.fail)
	t.Logf("E7 helper dies: credential %v, reason %q, program starts %d; K-EP %q", got.cred != nil, got.fail, fakeStarts(dir), line)
	if got.cred != nil || got.fail != "no valid credential in its output" || fakeStarts(dir) != 1 {
		t.Errorf("E7 helper dies: credential %v reason %q starts %d; want no credential, %q and exactly 1 start", got.cred != nil, got.fail, fakeStarts(dir), "no valid credential in its output")
	}
}
