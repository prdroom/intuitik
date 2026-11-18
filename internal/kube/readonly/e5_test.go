package readonly

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// e5Generation is a generation whose source holds the fake program with plan, run
// through credexec, recording into a new directory.
func e5Generation(t *testing.T, plan string) (*Generation, string) {
	t.Helper()
	prog, _ := fakeProgram(t)
	dir := t.TempDir()
	cfg, err := execProgramFor(prog, fakeEnv(t, dir, plan))
	if err != nil {
		t.Fatal(err)
	}
	g := NewGeneration(context.Background(), credexecRunner(helperBinary(t), nil), nil)
	g.setProgram(cfg, execV1, prog)
	return g, dir
}

// execProgramFor is the run of the fake program with env, built as for a kubeconfig exec
// entry without cluster information.
func execProgramFor(prog string, env map[string]string) (Program, error) {
	p := Program{Command: prog}
	for k, v := range env {
		p.Env = append(p.Env, k+"="+v)
	}
	p.Env = append(p.Env, execInfoEnv+`={"apiVersion":"`+execV1+`","kind":"ExecCredential","spec":{"interactive":false}}`)
	return p, nil
}

// recordedPIDs are the PIDs that the fake program and its descendants recorded in dir.
func recordedPIDs(dir string) []int {
	var pids []int
	raw, _ := os.ReadFile(filepath.Join(dir, "starts"))
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) >= 2 {
			n, _ := strconv.Atoi(f[1])
			pids = append(pids, n)
		}
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "descendants"))
	for _, l := range strings.Split(string(raw), "\n") {
		if n, err := strconv.Atoi(strings.TrimSpace(l)); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

// TestE5_CloseBeforeStart (packet M1-T3b E5): a run is held before it takes the source
// lock to start its process; the generation is closed; released, the run sees the closed
// source and starts no process.
func TestE5_CloseBeforeStart(t *testing.T) {
	g, dir := e5Generation(t, "token="+randomValue(t, "e5c"))
	reached, release := make(chan struct{}), make(chan struct{})
	admitHooks.before = func() {
		close(reached)
		<-release
	}
	t.Cleanup(func() { admitHooks.before = nil })
	done := make(chan obtained, 1)
	go func() { done <- g.current(nil) }()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not reach the start gate within 10 s")
	}
	g.Close()
	close(release)
	var got obtained
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not return within 10 s of its release")
	}
	starts := fakeStarts(dir)
	t.Logf("E5-CLOSE-FIRST: run held before the source lock, generation closed, run released: closed %v, credential %v, program starts %d", got.closed, got.cred != nil, starts)
	if !got.closed || got.cred != nil || starts != 0 {
		t.Errorf("E5-CLOSE-FIRST: closed %v credential %v starts %d; want a closed result and no process started", got.closed, got.cred != nil, starts)
	}
}

// TestE5_StartBeforeClose (E5): a run is held inside the source lock right after its
// process started (and recorded itself and a descendant). A close is requested and waits
// for the source lock; released, the close completes and the process tree is gone within
// 2 s of the close.
func TestE5_StartBeforeClose(t *testing.T) {
	g, dir := e5Generation(t, "child=detached;hang")
	reached, release := make(chan struct{}), make(chan struct{})
	admitHooks.started = func() {
		// Wait until the program and its descendant have recorded their PIDs.
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if len(recordedPIDs(dir)) >= 2 {
				break
			}
		}
		close(reached)
		<-release
	}
	t.Cleanup(func() { admitHooks.started = nil })
	done := make(chan obtained, 1)
	go func() { done <- g.current(nil) }()
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the run did not start its process within 15 s")
	}
	pids := recordedPIDs(dir)
	closed := make(chan time.Time, 1)
	go func() {
		g.Close()
		closed <- time.Now()
	}()
	waited := false
	select {
	case <-closed:
	case <-time.After(300 * time.Millisecond):
		waited = true
	}
	close(release)
	var closedAt time.Time
	if waited {
		closedAt = <-closed
	} else {
		closedAt = time.Now()
	}
	alive, took := waitGoneSince(pids, closedAt, 2*time.Second)
	<-done
	t.Logf("E5-START-FIRST: recorded PIDs %v; the close waited for the source lock %v; alive %v, %s after the close", pids, waited, alive, took.Round(time.Millisecond))
	if len(pids) < 2 {
		t.Errorf("E5-START-FIRST: %d PIDs recorded, want the program and its descendant", len(pids))
	}
	if !waited {
		t.Errorf("E5-START-FIRST: the close returned while the run held the source lock")
	}
	if len(alive) != 0 {
		t.Errorf("E5-START-FIRST: PIDs %v alive %s after the close, want none within 2 s", alive, took.Round(time.Millisecond))
	}
}

// waitGoneSince waits until none of pids is alive, at most limit after since, and
// returns the PIDs alive then and the time from since.
func waitGoneSince(pids []int, since time.Time, limit time.Duration) ([]int, time.Duration) {
	for {
		var alive []int
		for _, p := range pids {
			if processAlive(p) {
				alive = append(alive, p)
			}
		}
		if len(alive) == 0 || time.Since(since) > limit {
			return alive, time.Since(since)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
