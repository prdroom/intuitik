//go:build kind

package connect

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Replaced attempts and blocked kubeconfig reads (acceptance 4 and 5, E5, E14), on the
// release binary.

// TestK_ExecCancelledBySwitch (acceptance 4a, E5): a switch after the first run's start
// marker. The final state is the new attempt's; the old program and its descendant are
// gone within 2 s of the switch; past the old attempt's W1 there is no line or state of it.
func TestK_ExecCancelledBySwitch(t *testing.T) {
	prog, user := newProgram(t, "IfAvailable", "child=detached;hang")
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), execKube(t, "kind-ok", user))
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	if o := p.mustSettle(); o.State != "connected" {
		t.Fatalf("preparation: kind-ok did not connect: %s", str(o.Message))
	}
	p.switchTo("exec-ctx")
	if !prog.waitStarts(1, 5*time.Second) || !prog.waitDescendants(1, 5*time.Second) {
		t.Fatal("preparation: the program did not start with its descendant")
	}
	runStart := prog.startTimes()[0]
	errBefore := len(p.stderr.list())
	switched := time.Now()
	if r := p.switchTo("kind-ok"); r.status != 202 {
		t.Fatalf("switch back: status %d", r.status)
	}
	pids := prog.pids(1)
	alive, took := gone(pids, switched, 2*time.Second)
	final := p.mustSettle()
	time.Sleep(time.Until(runStart.Add(w1 + 3*time.Second)))
	later, _ := p.cluster()
	newErr := p.stderr.list()[errBefore:]
	t.Logf("K-EXEC-SWITCH: old PIDs %v alive %v %s after the switch; final %s %s; past the old W1 state %s, stderr since the switch %q",
		pids, alive, took.Round(10*time.Millisecond), final.State, str(final.Context), later.State, newErr)
	if len(alive) != 0 {
		t.Errorf("K-EXEC-SWITCH: PIDs %v alive, want the old program gone within 2 s of the switch", alive)
	}
	if final.State != "connected" || later.State != "connected" || str(later.Context) != "kind-ok" || len(newErr) != 0 {
		t.Errorf("K-EXEC-SWITCH: state %q then %q (%s), stderr %q; want connected on kind-ok and no line of the old attempt", final.State, later.State, str(later.Context), newErr)
	}
}

// TestK_ExecRetryRunsAgain (acceptance 4b, E5): a retry after a failure runs the program
// again; a retry while it runs starts a new attempt that runs it again, the earlier
// program is gone within 2 s, and only the newest attempt's line and state appear.
func TestK_ExecRetryRunsAgain(t *testing.T) {
	token := serviceAccountToken(t, "intuitik-exec-retry")
	prog, user := newProgram(t, "IfAvailable", "exit=1", "child=detached;hang", "tokenfile="+writeToken(t, token))
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), execKube(t, "exec-ctx", user))
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	failed := fmt.Sprintf(textEP, prog.path, "exec-ctx", "exit code 1")
	t.Logf("K-EXEC-RETRY first attempt: state %s, starts %d, message %q", o.State, prog.starts(), str(o.Message))
	if o.State != "credential-program-failed" || str(o.Message) != failed {
		t.Errorf("K-EXEC-RETRY first attempt: state %q message %q, want the exact K-EP with exit code 1", o.State, str(o.Message))
	}
	p.retry()
	if !prog.waitStarts(2, 5*time.Second) || !prog.waitDescendants(1, 5*time.Second) {
		t.Fatalf("K-EXEC-RETRY: the retry after the failure did not run the program again (starts %d)", prog.starts())
	}
	secondStart := prog.startTimes()[1]
	errBefore := len(p.stderr.list())
	retried := time.Now()
	p.retry()
	pids := prog.pids(2)
	alive, took := gone(pids, retried, 2*time.Second)
	final := p.mustSettle()
	time.Sleep(time.Until(secondStart.Add(w1 + 3*time.Second)))
	later, _ := p.cluster()
	newErr := p.stderr.list()[errBefore:]
	ok := fmt.Sprintf(textOK, "kind-c", "exec-ctx")
	t.Logf("K-EXEC-RETRY: starts %d; second run PIDs %v alive %v %s after the retry; final %s, past the second attempt's W1 %s; stderr since the retry %q; K-OK lines %d",
		prog.starts(), pids, alive, took.Round(10*time.Millisecond), final.State, later.State, newErr, p.stdout.count(ok))
	if prog.starts() != 3 || len(alive) != 0 {
		t.Errorf("K-EXEC-RETRY: starts %d, alive %v; want 3 starts and the second run gone within 2 s of the retry", prog.starts(), alive)
	}
	if final.State != "connected" || later.State != "connected" || len(newErr) != 0 || p.stdout.count(ok) != 1 {
		t.Errorf("K-EXEC-RETRY: state %q then %q, stderr %q, K-OK %d; want connected, no line of the replaced attempt, K-OK once", final.State, later.State, newErr, p.stdout.count(ok))
	}
}

// heldKubeconfig is a kubeconfig path that is a named pipe: a reader blocks until
// release. replace puts a regular file at the path; a reader already blocked stays on
// the pipe.
type heldKubeconfig struct {
	path, link string
	released   bool
}

func newHeldKubeconfig(t *testing.T) *heldKubeconfig {
	t.Helper()
	dir := t.TempDir()
	h := &heldKubeconfig{path: filepath.Join(dir, "kubeconfig"), link: filepath.Join(dir, "held-pipe")}
	if err := syscall.Mkfifo(h.path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if err := os.Link(h.path, h.link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !h.released {
			if f, err := os.OpenFile(h.link, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				f.Close()
			}
		}
	})
	return h
}

func (h *heldKubeconfig) replace(t *testing.T, content []byte) {
	t.Helper()
	tmp := h.path + ".new"
	writeFile(t, tmp, content)
	if err := os.Rename(tmp, h.path); err != nil {
		t.Fatal(err)
	}
}

// release gives content to the reader blocked on the pipe.
func (h *heldKubeconfig) release(t *testing.T, content []byte) {
	t.Helper()
	h.released = true
	f, err := os.OpenFile(h.link, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("release: no reader is blocked on the kubeconfig pipe: %v", err)
	}
	f.Write(content)
	f.Close()
}

// urBlocked is the K-UR line of a kubeconfig read that W1 passed (UV, UR-K).
func urBlocked() string {
	return "Cannot reach the API server (unknown) of context (unknown): the kubeconfig was not read within 10 s. Check your VPN or network connection, then retry from the intuitik tab or restart intuitik."
}

// TestK_KubeconfigReadBlocked (acceptance 5, E14): a kubeconfig read held past W1 gives
// the exact K-UR line of section 3 once. A retry at W1 + 1 s, with the file readable, is
// decided normally; releasing the held read at W1 + 3 s prints nothing and changes no
// state.
func TestK_KubeconfigReadBlocked(t *testing.T) {
	h := newHeldKubeconfig(t)
	content := (&kube{current: "kind-ok"}).withKind(t).bytes()
	p := startPanel(t, panelOpts{kubeconfig: strPtr(h.path)})
	o, _ := p.settle(w1 + 5*time.Second)
	want := urBlocked()
	n := waitCount(p.stderr, str(o.Message), 1)
	t.Logf("K-BLOCKED: state %s after %s; lines %d\n  expected %q\n  actual   %q", o.State, time.Since(p.started).Round(10*time.Millisecond), n, want, str(o.Message))
	if o.State != "unreachable" || str(o.Message) != want || n != 1 {
		t.Errorf("K-BLOCKED: state %q message %q, %d lines; want unreachable with the exact line once", o.State, str(o.Message), n)
	}
	untilAfterStart(p, w1+time.Second)
	h.replace(t, content)
	p.retry()
	retried := p.mustSettle()
	untilAfterStart(p, w1+3*time.Second)
	errBefore, outBefore := len(p.stderr.list()), len(p.stdout.list())
	h.release(t, content)
	newErr := window(p.stderr, errBefore, 3*time.Second)
	newOut := p.stdout.list()[outBefore:]
	later, _ := p.cluster()
	t.Logf("K-BLOCKED: retry at W1 + 1 s decided %s; after the release at W1 + 3 s: stdout %q, stderr %q, state %s", retried.State, newOut, newErr, later.State)
	if retried.State != "connected" || later.State != "connected" || len(newErr) != 0 || len(newOut) != 0 {
		t.Errorf("K-BLOCKED: retry %q, state after the release %q, new lines %q %q; want connected and no line", retried.State, later.State, newOut, newErr)
	}
}

// TestK_ExecLateKubeconfigRead (E5, D7): the held kubeconfig names an exec context. After
// W1 a retry (W1 + 1 s, file readable) runs the program once and connects; releasing the
// held read at W1 + 3 s starts no program: the replaced attempt's generation is closed
// and its W1 has passed.
func TestK_ExecLateKubeconfigRead(t *testing.T) {
	token := serviceAccountToken(t, "intuitik-exec-late")
	prog, user := newProgram(t, "IfAvailable", "tokenfile="+writeToken(t, token))
	content := execKube(t, "exec-ctx", user)
	h := newHeldKubeconfig(t)
	p := startPanel(t, panelOpts{kubeconfig: strPtr(h.path)})
	first, _ := p.settle(w1 + 5*time.Second)
	untilAfterStart(p, w1+time.Second)
	h.replace(t, content)
	p.retry()
	retried := p.mustSettle()
	untilAfterStart(p, w1+3*time.Second)
	startsBefore := prog.starts()
	errBefore := len(p.stderr.list())
	h.release(t, content)
	newErr := window(p.stderr, errBefore, 3*time.Second)
	later, _ := p.cluster()
	t.Logf("K-EXEC-LATE: first attempt %s; retry %s with %d program start(s); after the release at W1 + 3 s: starts %d, stderr %q, state %s",
		first.State, retried.State, startsBefore, prog.starts(), newErr, later.State)
	if retried.State != "connected" || startsBefore != 1 {
		t.Fatalf("preparation: the retry did not connect with one run (state %s, starts %d)", retried.State, startsBefore)
	}
	if prog.starts() != startsBefore || len(newErr) != 0 || later.State != "connected" {
		t.Errorf("K-EXEC-LATE: starts %d (want %d), stderr %q, state %s; want no program start and no line after the release", prog.starts(), startsBefore, newErr, later.State)
	}
}
