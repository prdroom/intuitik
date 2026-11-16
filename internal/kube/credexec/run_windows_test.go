//go:build windows

package credexec

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The process tests of packet M1-T3b E3, E11 and E12 on Windows (credexec-windows job).
// The runner starts the helper entry of the release binary that the job builds
// (INTUITIK_E2E_BINARY); TestRun_JobRefused uses the e2etest binary
// (INTUITIK_E2E_TEST_BINARY) and its job-refusal seam. Every test other than
// TestRun_JobRefused requires "job ok": a runner that refuses the job fails them.

func binary(t *testing.T, name string) string {
	t.Helper()
	bin := os.Getenv(name)
	if bin == "" {
		t.Fatalf("%s is not set", name)
	}
	return bin
}

func runHelper(t *testing.T, f *fakeRun, helper string, helperEnv []string, limit time.Duration) (Result, time.Duration) {
	t.Helper()
	prog, _ := fakeProgram(t)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	r := Run(ctx, Spec{Command: prog, Env: f.env, Helper: helper, HelperEnv: helperEnv}, openAdmit)
	return r, time.Since(start)
}

func requireJob(t *testing.T, label string, r Result) {
	t.Helper()
	t.Logf("%s: job %q", label, r.Job)
	if r.Job != JobOK {
		t.Errorf("%s: the helper reported %q, want %q (a runner that refuses the job object is STOP-8)", label, r.Job, JobOK)
	}
}

func checkTree(t *testing.T, label string, f *fakeRun, limit time.Duration) {
	t.Helper()
	pids := f.pids()
	alive, took := waitGone(pids, limit)
	t.Logf("%s: recorded PIDs %v (program and %d descendants); alive after %s: %v (limit %s)", label, pids, len(f.descendants()), took.Round(time.Millisecond), alive, limit)
	if len(f.descendants()) == 0 {
		t.Errorf("%s: the program recorded no descendant, so the case did not happen", label)
	}
	if len(alive) != 0 {
		t.Errorf("%s: PIDs %v still alive %s after the run ended, want none within %s", label, alive, took.Round(time.Millisecond), limit)
	}
}

func TestRun_NormalExitEndsDescendant(t *testing.T) {
	tok := runToken(t)
	f := newFakeRun(t, "child=detached;token="+tok)
	r, took := runHelper(t, f, binary(t, "INTUITIK_E2E_BINARY"), nil, 20*time.Second)
	t.Logf("E11-NORMAL: class %s code %d stdout %d bytes, run took %s", r.Class, r.Code, len(r.Stdout), took.Round(time.Millisecond))
	requireJob(t, "E11-NORMAL", r)
	if r.Class != ClassOK || !strings.Contains(string(r.Stdout), tok) {
		t.Errorf("E11-NORMAL: class %q, credential delivered %v; want ok with the credential", r.Class, strings.Contains(string(r.Stdout), tok))
	}
	checkTree(t, "E11-NORMAL", f, 2*time.Second)
}

func TestRun_ParentExitsFirst(t *testing.T) {
	f := newFakeRun(t, "child=detached;exit=7")
	r, took := runHelper(t, f, binary(t, "INTUITIK_E2E_BINARY"), nil, 20*time.Second)
	t.Logf("E11-PARENT: class %s code %d, run took %s", r.Class, r.Code, took.Round(time.Millisecond))
	requireJob(t, "E11-PARENT", r)
	if r.Class != ClassExit || r.Code != 7 {
		t.Errorf("E11-PARENT: class %q code %d, want exit 7", r.Class, r.Code)
	}
	checkTree(t, "E11-PARENT", f, 2*time.Second)
}

func TestRun_DeadlineEndsTree(t *testing.T) {
	f := newFakeRun(t, "child=detached;hang")
	r, took := runHelper(t, f, binary(t, "INTUITIK_E2E_BINARY"), nil, 5*time.Second)
	t.Logf("E11-DEADLINE: class %s, run took %s (deadline 5s)", r.Class, took.Round(time.Millisecond))
	requireJob(t, "E11-DEADLINE", r)
	if r.Class != ClassTimeout || took > 6*time.Second {
		t.Errorf("E11-DEADLINE: class %q after %s, want timeout within 6s", r.Class, took.Round(time.Millisecond))
	}
	checkTree(t, "E11-DEADLINE", f, 2*time.Second)
}

func TestRun_DescendantHoldsPipe(t *testing.T) {
	tok := runToken(t)
	f := newFakeRun(t, "child=holdpipe;token="+tok)
	r, took := runHelper(t, f, binary(t, "INTUITIK_E2E_BINARY"), nil, 20*time.Second)
	t.Logf("E11-PIPE: class %s stdout %d bytes, run took %s", r.Class, len(r.Stdout), took.Round(time.Millisecond))
	requireJob(t, "E11-PIPE", r)
	if r.Class != ClassOK || !strings.Contains(string(r.Stdout), tok) {
		t.Errorf("E11-PIPE: class %q, credential delivered %v; want ok with the credential", r.Class, strings.Contains(string(r.Stdout), tok))
	}
	if took > 3*time.Second {
		t.Errorf("E11-PIPE: the run took %s, want it to end within 1 s of the program's exit", took.Round(time.Millisecond))
	}
	checkTree(t, "E11-PIPE", f, 2*time.Second)
}

// TestRun_NoConsole (E3): the program opens CONIN$ and reads it. Started with
// DETACHED_PROCESS it has no console: it fails at once with its own code 3, class exit,
// long before the run's deadline.
func TestRun_NoConsole(t *testing.T) {
	f := newFakeRun(t, "tty")
	r, took := runHelper(t, f, binary(t, "INTUITIK_E2E_BINARY"), nil, 8*time.Second)
	t.Logf("E3-NOCONSOLE: class %s code %d, run took %s (deadline 8s)", r.Class, r.Code, took.Round(time.Millisecond))
	requireJob(t, "E3-NOCONSOLE", r)
	if r.Class != ClassExit || r.Code != 3 || took > 5*time.Second {
		t.Errorf("E3-NOCONSOLE: class %q code %d after %s, want exit 3 before the deadline", r.Class, r.Code, took.Round(time.Millisecond))
	}
}

// TestRun_JobHandleNotInherited (E11): the program counts the job handles it holds; the
// helper's job handle is not inheritable, so the count is 0.
func TestRun_JobHandleNotInherited(t *testing.T) {
	f := newFakeRun(t, "jobcheck;token="+runToken(t))
	r, _ := runHelper(t, f, binary(t, "INTUITIK_E2E_BINARY"), nil, 20*time.Second)
	requireJob(t, "E11-HANDLE", r)
	count := f.lines("jobhandles-1.txt")
	t.Logf("E11-HANDLE: class %s, job handles held by the program %v", r.Class, count)
	if r.Class != ClassOK || len(count) != 1 || count[0] != "0" {
		t.Errorf("E11-HANDLE: class %q, job handles %v; want ok and 0", r.Class, count)
	}
}

// TestRun_JobRefused (O-T3b-1 (b), E11): the e2etest seam makes the helper's job
// assignment fail with code 5. The helper writes exactly "job refused 5" and a line end
// and exits; intuitik starts the program itself, once, and ends it at the run's end.
func TestRun_JobRefused(t *testing.T) {
	seam := []string{"INTUITIK_TEST_CREDEXEC_JOB_ERROR=5"}
	t.Run("ok", func(t *testing.T) {
		// The helper's own stdout, started directly with the seam: exactly the control
		// line, and the program not started by it.
		prog, _ := fakeProgram(t)
		tok := runToken(t)
		direct := newFakeRun(t, "token="+tok)
		cmd := exec.Command(binary(t, "INTUITIK_E2E_TEST_BINARY"), append([]string{EntryName}, helperArgs(prog, nil)...)...)
		cmd.Env = append(append(os.Environ(), direct.env...), seam...)
		out, err := cmd.Output()
		t.Logf("E11-REFUSED helper stdout %q, exit %v, program starts by the helper %d", out, err, len(direct.starts()))
		if string(out) != "job refused 5\n" || err != nil || len(direct.starts()) != 0 {
			t.Errorf("E11-REFUSED helper: stdout %q exit %v starts %d; want exactly %q, exit 0 and no start", out, err, len(direct.starts()), "job refused 5\n")
		}

		f := newFakeRun(t, "token="+tok)
		r, _ := runHelper(t, f, binary(t, "INTUITIK_E2E_TEST_BINARY"), seam, 20*time.Second)
		starts := f.starts()
		t.Logf("E11-REFUSED ok: class %s job %q starts %v (runner PID %d)", r.Class, r.Job, starts, os.Getpid())
		if r.Class != ClassOK || r.Job != JobRefused || !strings.Contains(string(r.Stdout), tok) {
			t.Errorf("E11-REFUSED ok: class %q job %q; want ok, %q and the token", r.Class, r.Job, JobRefused)
		}
		if len(starts) != 1 || starts[0][1] != os.Getpid() {
			t.Errorf("E11-REFUSED ok: starts %v; want one start whose parent is the runner's process %d", starts, os.Getpid())
		}
		if alive, _ := waitGone(f.pids(), 2*time.Second); len(alive) != 0 {
			t.Errorf("E11-REFUSED ok: the program %v is alive after the run", alive)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		f := newFakeRun(t, "hang")
		r, took := runHelper(t, f, binary(t, "INTUITIK_E2E_TEST_BINARY"), seam, 4*time.Second)
		starts := f.starts()
		alive, gone := waitGone(f.pids(), 2*time.Second)
		t.Logf("E11-REFUSED deadline: class %s job %q starts %d after %s; alive %v %s after the end", r.Class, r.Job, len(starts), took.Round(time.Millisecond), alive, gone.Round(time.Millisecond))
		if r.Class != ClassTimeout || len(starts) != 1 || len(alive) != 0 {
			t.Errorf("E11-REFUSED deadline: class %q, %d starts, alive %v; want timeout, 1 start and the program gone within 2 s", r.Class, len(starts), alive)
		}
	})
}
