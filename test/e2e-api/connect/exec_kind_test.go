//go:build kind

package connect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Credential program contexts (packet M1-T3b sections 3 and 6.2) through the release
// binary, unless the test says it uses the e2etest binary with the read trigger of
// READ-TRIGGER-T3b. The fake program records every start, its KUBERNETES_EXEC_INFO and,
// where asked, its descendants.

// execKube is a kubeconfig with the kind context kind-ok and the exec context exec-ctx on
// the kind cluster; current is the current context.
func execKube(t *testing.T, current string, user map[string]any) []byte {
	return (&kube{current: current}).withKind(t).user("exec-user", user).context("exec-ctx", "kind-c", "exec-user").bytes()
}

// TestK_ExecConnects (acceptance 1, E2, P-T3b-3): an IfAvailable program prints a valid
// token without input. The state is connected and K-OK is on stdout once, within W1.
func TestK_ExecConnects(t *testing.T) {
	token := serviceAccountToken(t, "intuitik-exec-connects")
	prog, user := newProgram(t, "IfAvailable", "tokenfile="+writeToken(t, token))
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), execKube(t, "exec-ctx", user))
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	took := time.Since(p.started)
	want := fmt.Sprintf(textOK, "kind-c", "exec-ctx")
	n := waitCount(p.stdout, want, 1)
	t.Logf("K-PROGRAM-OK binary %s: state %s after %s (W1 %s), program starts %d; K-OK on stdout %d times\n  expected %q\n  actual   %q",
		binaryInfo(t, p), o.State, took.Round(10*time.Millisecond), w1, prog.starts(), n, want, str(o.Message))
	if o.State != "connected" {
		t.Fatalf("K-PROGRAM-OK: state %q message %q, want connected", o.State, str(o.Message))
	}
	if str(o.Message) != want || n != 1 || took > w1 {
		t.Errorf("K-PROGRAM-OK: message %q, K-OK lines %d, after %s; want the exact K-OK once within W1", str(o.Message), n, took.Round(10*time.Millisecond))
	}
}

// TestK_ExecAlwaysNotRun (acceptance 2, E2): an exec entry with interactiveMode Always is
// credential-program-failed with K-EP "needs input from a terminal" once, decided before
// any run: the program never starts.
func TestK_ExecAlwaysNotRun(t *testing.T) {
	prog, user := newProgram(t, "Always", "token=never-used")
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), execKube(t, "exec-ctx", user))
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	want := fmt.Sprintf(textEP, prog.path, "exec-ctx", "needs input from a terminal")
	n := waitCount(p.stderr, want, 1)
	t.Logf("K-PROGRAM-ALWAYS: state %s, program starts %d, K-EP on stderr %d times\n  expected %q\n  actual   %q", o.State, prog.starts(), n, want, str(o.Message))
	if o.State != "credential-program-failed" || str(o.Message) != want || n != 1 {
		t.Errorf("K-PROGRAM-ALWAYS: state %q message %q, %d lines; want credential-program-failed with the exact K-EP once", o.State, str(o.Message), n)
	}
	if prog.starts() != 0 {
		t.Errorf("K-PROGRAM-ALWAYS: the program started %d times, want never", prog.starts())
	}
}

// TestK_ExecTimeout (acceptance 2, E4): a program that hangs, with a descendant, is
// credential-program-failed with K-EP "no answer within 10 s" decided within W1 + 1 s of
// the attempt's start, and every recorded PID is gone within W1 + 2 s.
func TestK_ExecTimeout(t *testing.T) {
	prog, user := newProgram(t, "IfAvailable", "child=detached;hang")
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), execKube(t, "exec-ctx", user))
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o, _ := p.settle(w1 + 5*time.Second)
	decided := time.Since(p.started)
	want := fmt.Sprintf(textEP, prog.path, "exec-ctx", "no answer within 10 s")
	n := waitCount(p.stderr, want, 1)
	alive, _ := gone(prog.pids(1), p.started, w1+2*time.Second)
	cleaned := time.Since(p.started)
	t.Logf("K-PROGRAM-TIMEOUT: state %s decided %s after the attempt start (bound W1 + 1 s = %s); K-EP lines %d; PIDs %v, alive at %s: %v (bound W1 + 2 s)\n  expected %q\n  actual   %q",
		o.State, decided.Round(10*time.Millisecond), w1+time.Second, n, prog.pids(1), cleaned.Round(10*time.Millisecond), alive, want, str(o.Message))
	if o.State != "credential-program-failed" || str(o.Message) != want || n != 1 {
		t.Errorf("K-PROGRAM-TIMEOUT: state %q message %q, %d lines; want the exact K-EP once", o.State, str(o.Message), n)
	}
	if decided > w1+time.Second {
		t.Errorf("K-PROGRAM-TIMEOUT: decided %s after the start, want within W1 + 1 s", decided.Round(10*time.Millisecond))
	}
	if len(prog.pids(1)) < 2 || len(alive) != 0 {
		t.Errorf("K-PROGRAM-TIMEOUT: PIDs %v, alive %v; want the program and its descendant gone within W1 + 2 s", prog.pids(1), alive)
	}
}

// TestK_ExecStderrPassesThrough (acceptance 7, E8): what the program writes to its stderr
// reaches intuitik's stderr (the decided trust boundary).
func TestK_ExecStderrPassesThrough(t *testing.T) {
	marker := "intuitik-program-stderr-" + randomHex(8)
	token := serviceAccountToken(t, "intuitik-exec-stderr")
	_, user := newProgram(t, "IfAvailable", "stderr="+marker+";tokenfile="+writeToken(t, token))
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), execKube(t, "exec-ctx", user))
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	n := waitCount(p.stderr, marker, 1)
	t.Logf("K-PROGRAM-STDERR: state %s, the program's stderr marker on intuitik's stderr %d times", o.State, n)
	if n < 1 {
		t.Errorf("K-PROGRAM-STDERR: the marker is not on intuitik's stderr: %q", p.stderr.list())
	}
}

// TestK_ExecNoStdinOnTerminal (acceptance 6, E2, E3): intuitik runs on a pseudo terminal.
// An IfAvailable program records spec.interactive false and reads end of file on stdin,
// and intuitik's own Enter handling still works (a second login address). A program that
// opens /dev/tty fails at once with K-EP "exit code 3".
func TestK_ExecNoStdinOnTerminal(t *testing.T) {
	token := serviceAccountToken(t, "intuitik-exec-stdin")
	prog, user := newProgram(t, "IfAvailable", "stdin;tokenfile="+writeToken(t, token))
	ttyProg, ttyUser := newProgram(t, "IfAvailable", "tty")
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "exec-ctx"}).withKind(t).
		user("exec-user", user).context("exec-ctx", "kind-c", "exec-user").
		user("tty-user", ttyUser).context("tty-ctx", "kind-c", "tty-user").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc), pty: true})
	o := p.mustSettle()
	interactive := prog.info(1)["spec"]
	stdin := prog.lines("stdin-1.txt")
	t.Logf("K-PROGRAM-STDIN: state %s; the program's spec %v; stdin %v", o.State, interactive, stdin)
	if spec, _ := interactive.(map[string]any); spec == nil || spec["interactive"] != false {
		t.Errorf("K-PROGRAM-STDIN: spec %v, want interactive false", interactive)
	}
	if len(stdin) != 1 || stdin[0] != "eof 0" {
		t.Errorf("K-PROGRAM-STDIN: stdin result %v, want end of file with 0 bytes", stdin)
	}
	if o.State != "connected" {
		t.Errorf("K-PROGRAM-STDIN: state %q message %q, want connected", o.State, str(o.Message))
	}
	before := len(strings.Split(strings.TrimSpace(readText(p.openLog)), "\n"))
	p.master.Write([]byte("\n"))
	deadline := time.Now().Add(5 * time.Second)
	after := before
	for after == before && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		after = len(strings.Split(strings.TrimSpace(readText(p.openLog)), "\n"))
	}
	t.Logf("K-PROGRAM-STDIN: login addresses the opener received before Enter %d, after %d", before, after)
	if after != before+1 {
		t.Errorf("K-PROGRAM-STDIN: Enter gave %d new addresses, want 1 (intuitik's Enter handling)", after-before)
	}

	switched := time.Now()
	p.switchTo("tty-ctx")
	o = p.mustSettle()
	took := time.Since(switched)
	want := fmt.Sprintf(textEP, ttyProg.path, "tty-ctx", "exit code 3")
	t.Logf("K-PROGRAM-TTY: state %s after %s\n  expected %q\n  actual   %q", o.State, took.Round(10*time.Millisecond), want, str(o.Message))
	if o.State != "credential-program-failed" || str(o.Message) != want || took > w1/2 {
		t.Errorf("K-PROGRAM-TTY: state %q message %q after %s; want the exact K-EP with exit code 3 well before W1", o.State, str(o.Message), took.Round(10*time.Millisecond))
	}
}

func readText(path string) string {
	raw, _ := os.ReadFile(path)
	return string(raw)
}
