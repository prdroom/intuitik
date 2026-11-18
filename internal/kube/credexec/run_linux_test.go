//go:build linux

package credexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// The process tests of packet M1-T3b E3 and E11 on Linux, in process with real child
// processes (readonly-gate job). Each checks in the same run the class and credential,
// and that every recorded process is gone.

// checkTree waits for every recorded PID to be gone within limit after the run ended.
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

func runFake(t *testing.T, f *fakeRun, limit time.Duration) (Result, time.Duration) {
	t.Helper()
	prog, _ := fakeProgram(t)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	r := Run(ctx, Spec{Command: prog, Env: f.env}, openAdmit)
	return r, time.Since(start)
}

// TestRun_NormalExitEndsDescendant: the program starts a descendant, prints a credential
// and exits 0. Class ok with the credential; the descendant is gone within 2 s.
func TestRun_NormalExitEndsDescendant(t *testing.T) {
	tok := runToken(t)
	f := newFakeRun(t, "child=detached;token="+tok)
	r, took := runFake(t, f, 10*time.Second)
	t.Logf("E11-NORMAL: class %s code %d stdout %d bytes, run took %s", r.Class, r.Code, len(r.Stdout), took.Round(time.Millisecond))
	if r.Class != ClassOK || !strings.Contains(string(r.Stdout), tok) {
		t.Errorf("E11-NORMAL: class %q, credential delivered %v; want ok with the credential", r.Class, strings.Contains(string(r.Stdout), tok))
	}
	checkTree(t, "E11-NORMAL", f, 2*time.Second)
}

// TestRun_ParentExitsFirst: the program starts a descendant and exits 7 while the
// descendant runs. Class exit with code 7; the descendant is gone within 2 s.
func TestRun_ParentExitsFirst(t *testing.T) {
	f := newFakeRun(t, "child=detached;exit=7")
	r, took := runFake(t, f, 10*time.Second)
	t.Logf("E11-PARENT: class %s code %d, run took %s", r.Class, r.Code, took.Round(time.Millisecond))
	if r.Class != ClassExit || r.Code != 7 {
		t.Errorf("E11-PARENT: class %q code %d, want exit 7", r.Class, r.Code)
	}
	checkTree(t, "E11-PARENT", f, 2*time.Second)
}

// TestRun_DeadlineEndsTree: the program starts a descendant and hangs. Class timeout at
// the run context's end; the program and the descendant are gone within 2 s of it.
func TestRun_DeadlineEndsTree(t *testing.T) {
	f := newFakeRun(t, "child=detached;hang")
	r, took := runFake(t, f, 2*time.Second)
	t.Logf("E11-DEADLINE: class %s, run took %s (deadline 2s)", r.Class, took.Round(time.Millisecond))
	if r.Class != ClassTimeout || took > 3*time.Second {
		t.Errorf("E11-DEADLINE: class %q after %s, want timeout within 3s", r.Class, took.Round(time.Millisecond))
	}
	checkTree(t, "E11-DEADLINE", f, 2*time.Second)
}

// TestRun_DescendantHoldsPipe: a descendant holds the program's stdout. The credential is
// delivered with class ok, the run ends within 1 s of the program's exit, and the
// descendant is gone.
func TestRun_DescendantHoldsPipe(t *testing.T) {
	tok := runToken(t)
	f := newFakeRun(t, "child=holdpipe;token="+tok)
	r, took := runFake(t, f, 20*time.Second)
	t.Logf("E11-PIPE: class %s stdout %d bytes, run took %s", r.Class, len(r.Stdout), took.Round(time.Millisecond))
	if r.Class != ClassOK || !strings.Contains(string(r.Stdout), tok) {
		t.Errorf("E11-PIPE: class %q, credential delivered %v; want ok with the credential", r.Class, strings.Contains(string(r.Stdout), tok))
	}
	// The program exits right after printing; its start and the descendant's start take
	// well under a second, so the run ends within 1 s of the exit when it took under 2 s.
	if took > 2*time.Second {
		t.Errorf("E11-PIPE: the run took %s, want it to end within 1 s of the program's exit", took.Round(time.Millisecond))
	}
	checkTree(t, "E11-PIPE", f, 2*time.Second)
}

// TestRun_NoTTY (E3): intuitik on a terminal. The test runs itself again as a child in a
// new session whose controlling terminal is a pseudo terminal (positive control: the
// child opens /dev/tty), and the child runs a program that opens /dev/tty and reads it.
// The program has no controlling terminal: it fails at once with its own code 3, class
// exit, long before the run's 8 s deadline.
func TestRun_NoTTY(t *testing.T) {
	if out := os.Getenv("CREDEXEC_NOTTY_RESULT"); out != "" {
		noTTYChild(t, out)
		return
	}
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("pseudo terminal: %v", err)
	}
	defer master.Close()
	go func() {
		b := make([]byte, 1024)
		for {
			if _, err := master.Read(b); err != nil {
				return
			}
		}
	}()
	result := filepath.Join(t.TempDir(), "result")
	fakeProgram(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestRun_NoTTY$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CREDEXEC_NOTTY_RESULT="+result)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("the child on the pseudo terminal did not end within 30 s")
	}
	raw, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the child wrote no result: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, l := range lines {
		t.Logf("E3-NOTTY child: %s", l)
	}
	want := []string{"control tty opened", "class exit code 3"}
	if len(lines) < 3 || lines[0] != want[0] || lines[1] != want[1] {
		t.Errorf("E3-NOTTY: child result %q, want %q then the time", lines, want)
	}
	if len(lines) >= 3 {
		var ms int
		fmt.Sscanf(lines[2], "took %dms", &ms)
		if ms > 5000 {
			t.Errorf("E3-NOTTY: the program ended %d ms after its start, want before the 8 s deadline with its own code", ms)
		}
	}
}

// noTTYChild is the child of TestRun_NoTTY: it has the pseudo terminal as its
// controlling terminal and stdin.
func noTTYChild(t *testing.T, result string) {
	var lines []string
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		f.Close()
		lines = append(lines, "control tty opened")
	} else {
		lines = append(lines, "control tty not opened: "+err.Error())
	}
	f := newFakeRun(t, "tty")
	prog, _ := fakeProgram(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	r := Run(ctx, Spec{Command: prog, Env: f.env}, openAdmit)
	took := time.Since(start)
	lines = append(lines, fmt.Sprintf("class %s code %d", r.Class, r.Code), fmt.Sprintf("took %dms", took.Milliseconds()))
	os.WriteFile(result, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// openPTY opens a pseudo terminal pair.
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
