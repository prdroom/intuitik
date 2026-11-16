//go:build kind

package connect

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Refresh after connected (acceptance 3 and 4a, E4, E5, E6). These tests start the
// e2etest binary with the fixture "cluster-read-route" (READ-TRIGGER-T3b): its route
// GET /api/fixture-cluster-read makes one Client.Get of /api, the only read after
// connected that intuitik has. The credential source, the runner, refresh, state changes
// and output are the product's. The fake API server records the token of each request.

// refreshSetup starts the e2etest binary on a kubeconfig whose current context exec-ctx
// uses the fake server, with the kind context kind-ok beside it. It waits for connected
// and returns the panel, the program and the token names.
func refreshSetup(t *testing.T, srv *credServer, plans ...string) (*panel, *program) {
	t.Helper()
	prog, user := newProgram(t, "IfAvailable", plans...)
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "exec-ctx"}).withKind(t).
		withServer("fake-c", srv).user("exec-user", user).context("exec-ctx", "fake-c", "exec-user").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc), test: true, fixture: readFixture})
	o := p.mustSettle()
	t.Logf("preparation: binary %s, fixture %s; state %s after %s, program starts %d", binaryInfo(t, p), readFixture, o.State, time.Since(p.started).Round(10*time.Millisecond), prog.starts())
	if o.State != "connected" {
		t.Fatalf("preparation: the exec context did not connect: state %s message %q", o.State, str(o.Message))
	}
	return p, prog
}

// untilAfterStart sleeps until d after the attempt's start.
func untilAfterStart(p *panel, d time.Duration) {
	if wait := d - time.Since(p.started); wait > 0 {
		time.Sleep(wait)
	}
}

// TestK_ExecRefresh (acceptance 3, E6): through the read trigger, after connected: an
// expired credential is replaced by a new run; a 401 runs the program once and repeats the
// read once with the new credential; a new credential rejected again is K-AU once; a
// failed refresh run is K-EP once and the program does not run again; two reads at once
// cause one run. A successful refresh keeps connected and prints nothing (3 s window).
func TestK_ExecRefresh(t *testing.T) {
	tokA, tokB := "tok-a-"+randomHex(8), "tok-b-"+randomHex(8)
	names := map[string]string{tokA: "A", tokB: "B"}
	quiet := func(t *testing.T, p *panel, label string, outBefore, errBefore int) {
		t.Helper()
		newErr := window(p.stderr, errBefore, 3*time.Second)
		newOut := p.stdout.list()[outBefore:]
		o, _ := p.cluster()
		t.Logf("%s: window of 3 s after the refresh: stdout %q, stderr %q, state %s", label, newOut, newErr, o.State)
		if len(newOut) != 0 || len(newErr) != 0 || o.State != "connected" {
			t.Errorf("%s: new lines stdout %q stderr %q, state %s; want no line and connected", label, newOut, newErr, o.State)
		}
	}

	t.Run("expiry", func(t *testing.T) {
		srv := newCredServer(t)
		p, prog := refreshSetup(t, srv, "token="+tokA+";expiry=3", "token="+tokB)
		outBefore, errBefore, before := len(p.stdout.list()), len(p.stderr.list()), srv.count()
		untilAfterStart(p, 4*time.Second)
		forced := time.Now()
		status := p.readOnce(30 * time.Second)
		got := srv.received(before, names)
		t.Logf("K-REFRESH expiry: read forced %s after the start answered %d; program starts %d; server received %v (stub (ii) would send A)", forced.Sub(p.started).Round(10*time.Millisecond), status, prog.starts(), got)
		if status != 200 || prog.starts() != 2 || len(got) != 1 || got[0] != "B" {
			t.Errorf("K-REFRESH expiry: read %d, starts %d, server received %v; want 200, 2 starts and the new credential B only", status, prog.starts(), got)
		}
		quiet(t, p, "K-REFRESH expiry", outBefore, errBefore)
	})

	t.Run("401 after connected", func(t *testing.T) {
		srv := newCredServer(t)
		p, prog := refreshSetup(t, srv, "token="+tokA, "token="+tokB)
		outBefore, errBefore, before := len(p.stdout.list()), len(p.stderr.list()), srv.count()
		srv.reject(tokA)
		status := p.readOnce(30 * time.Second)
		got := srv.received(before, names)
		t.Logf("K-REFRESH 401: read answered %d; program starts %d; server received %v (A rejected; stub (ii) would repeat with A)", status, prog.starts(), got)
		if status != 200 || prog.starts() != 2 || fmt.Sprint(got) != "[A B]" {
			t.Errorf("K-REFRESH 401: read %d, starts %d, server received %v; want 200, 2 starts and [A B] (one repeat)", status, prog.starts(), got)
		}
		quiet(t, p, "K-REFRESH 401", outBefore, errBefore)
	})

	t.Run("new credential rejected again", func(t *testing.T) {
		srv := newCredServer(t)
		p, prog := refreshSetup(t, srv, "token="+tokA, "token="+tokB)
		before := srv.count()
		srv.reject(tokA, tokB)
		status := p.readOnce(30 * time.Second)
		o, _ := p.settle(5 * time.Second)
		want := fmt.Sprintf(textAU, "fake-c", "exec-ctx")
		n := waitCount(p.stderr, want, 1)
		got := srv.received(before, names)
		t.Logf("K-REFRESH rejected: read %d; starts %d; server received %v; state %s, K-AU lines %d", status, prog.starts(), got, o.State, n)
		if o.State != "credentials-rejected" || str(o.Message) != want || n != 1 || fmt.Sprint(got) != "[A B]" {
			t.Errorf("K-REFRESH rejected: state %q message %q, %d lines, server %v; want credentials-rejected with K-AU once after [A B]", o.State, str(o.Message), n, got)
		}
	})

	t.Run("failed refresh run", func(t *testing.T) {
		srv := newCredServer(t)
		p, prog := refreshSetup(t, srv, "token="+tokA, "exit=4")
		srv.reject(tokA)
		p.readOnce(30 * time.Second)
		second := p.readOnce(30 * time.Second)
		o, _ := p.settle(5 * time.Second)
		want := fmt.Sprintf(textEP, prog.path, "exec-ctx", "exit code 4")
		n := waitCount(p.stderr, want, 1)
		t.Logf("K-REFRESH failed run: second read %d; starts %d; state %s, K-EP lines %d", second, prog.starts(), o.State, n)
		if o.State != "credential-program-failed" || str(o.Message) != want || n != 1 {
			t.Errorf("K-REFRESH failed run: state %q message %q, %d lines; want credential-program-failed with the exact K-EP once", o.State, str(o.Message), n)
		}
		if prog.starts() != 2 {
			t.Errorf("K-REFRESH failed run: %d starts, want 2 (no run after the failed one)", prog.starts())
		}
	})

	t.Run("two reads at once", func(t *testing.T) {
		srv := newCredServer(t)
		p, prog := refreshSetup(t, srv, "token="+tokA+";expiry=3", "sleep=1s;token="+tokB)
		before := srv.count()
		untilAfterStart(p, 4*time.Second)
		var wg sync.WaitGroup
		statuses := make([]int, 2)
		for i := range statuses {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses[i] = p.readOnce(30 * time.Second)
			}()
		}
		wg.Wait()
		got := srv.received(before, names)
		t.Logf("K-REFRESH two reads: answers %v; program starts %d; server received %v (stub (ii) would send A)", statuses, prog.starts(), got)
		if statuses[0] != 200 || statuses[1] != 200 || prog.starts() != 2 || fmt.Sprint(got) != "[B B]" {
			t.Errorf("K-REFRESH two reads: answers %v, starts %d, server %v; want 200 200, one refresh run (2 starts) and [B B]", statuses, prog.starts(), got)
		}
	})
}

// TestK_ExecRefreshBeforeW1 (E4, Devred ea70d55e item 2): connected about 1 s after the
// attempt's start; a refresh forced about 3.5 s after it takes 9 s and succeeds, past the
// attempt's W1 instant: the refresh run has its own 10 s.
func TestK_ExecRefreshBeforeW1(t *testing.T) {
	srv := newCredServer(t)
	tokA, tokB := "tok-a-"+randomHex(8), "tok-b-"+randomHex(8)
	p, prog := refreshSetup(t, srv, "sleep=1s;token="+tokA+";expiry=2", "sleep=9s;token="+tokB)
	before := srv.count()
	untilAfterStart(p, 3500*time.Millisecond)
	status := p.readOnce(30 * time.Second)
	starts := prog.startTimes()
	o, _ := p.cluster()
	got := srv.received(before, map[string]string{tokA: "A", tokB: "B"})
	if len(starts) == 2 {
		t.Logf("K-REFRESH-BEFORE-W1: refresh run started %s after the attempt start, the read answered %d at %s (W1 %s); state %s; server received %v",
			starts[1].Sub(p.started).Round(10*time.Millisecond), status, time.Since(p.started).Round(10*time.Millisecond), w1, o.State, got)
	}
	if len(starts) != 2 || starts[1].Sub(p.started) > w1/2 {
		t.Fatalf("preparation: program starts %d; the refresh did not start before W1", len(starts))
	}
	if status != 200 || o.State != "connected" || fmt.Sprint(got) != "[B]" {
		t.Errorf("K-REFRESH-BEFORE-W1: read %d, state %q (%s), server %v; want 200, connected and B: the refresh run ends after the attempt's W1 instant", status, o.State, str(o.Message), got)
	}
}

// TestK_ExecRefreshAfterW1 (E4): a refresh that starts more than 10 s after the attempt's
// start takes 3 s and succeeds.
func TestK_ExecRefreshAfterW1(t *testing.T) {
	srv := newCredServer(t)
	tokA, tokB := "tok-a-"+randomHex(8), "tok-b-"+randomHex(8)
	p, prog := refreshSetup(t, srv, "token="+tokA+";expiry=11", "sleep=3s;token="+tokB)
	before := srv.count()
	untilAfterStart(p, w1+3*time.Second)
	status := p.readOnce(30 * time.Second)
	starts := prog.startTimes()
	o, _ := p.cluster()
	got := srv.received(before, map[string]string{tokA: "A", tokB: "B"})
	if len(starts) == 2 {
		t.Logf("K-REFRESH-AFTER-W1: refresh run started %s after the attempt start (W1 %s), read answered %d; state %s; server received %v",
			starts[1].Sub(p.started).Round(10*time.Millisecond), w1, status, o.State, got)
	}
	if len(starts) != 2 || starts[1].Sub(p.started) < w1 {
		t.Fatalf("preparation: program starts %d; the refresh did not start after W1", len(starts))
	}
	if status != 200 || o.State != "connected" || fmt.Sprint(got) != "[B]" {
		t.Errorf("K-REFRESH-AFTER-W1: read %d, state %q (%s), server %v; want 200, connected and B", status, o.State, str(o.Message), got)
	}
}

// TestK_ExecRefreshTimeout (E4): a refresh run that hangs, with a descendant, gives K-EP
// "no answer within 10 s" 10 s after that run started (bound + 1 s), and its PIDs are
// gone 2 s after that.
func TestK_ExecRefreshTimeout(t *testing.T) {
	srv := newCredServer(t)
	p, prog := refreshSetup(t, srv, "token=tok-"+randomHex(8)+";expiry=3", "child=detached;hang")
	untilAfterStart(p, 4*time.Second)
	done := make(chan int, 1)
	go func() { done <- p.readOnce(30 * time.Second) }()
	if !prog.waitStarts(2, 5*time.Second) || !prog.waitDescendants(1, 5*time.Second) {
		t.Fatalf("preparation: the refresh run did not start with its descendant")
	}
	runStart := prog.startTimes()[1]
	var decided time.Duration
	want := fmt.Sprintf(textEP, prog.path, "exec-ctx", "no answer within 10 s")
	for deadline := time.Now().Add(w1 + 5*time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if o, _ := p.cluster(); o.State != "connected" {
			decided = time.Since(runStart)
			break
		}
	}
	<-done
	o, _ := p.cluster()
	pids := prog.pids(2)
	alive, cleaned := gone(pids, runStart, w1+2*time.Second)
	n := waitCount(p.stderr, want, 1)
	t.Logf("K-REFRESH-TIMEOUT: state %s decided %s after the refresh run started (want W1, bound W1 + 1 s); K-EP lines %d; PIDs %v alive %v at %s after the run start (bound W1 + 2 s)\n  expected %q\n  actual   %q",
		o.State, decided.Round(10*time.Millisecond), n, pids, alive, cleaned.Round(10*time.Millisecond), want, str(o.Message))
	if o.State != "credential-program-failed" || str(o.Message) != want || n != 1 {
		t.Errorf("K-REFRESH-TIMEOUT: state %q message %q, %d lines; want the exact K-EP once", o.State, str(o.Message), n)
	}
	if decided < w1-time.Second || decided > w1+time.Second {
		t.Errorf("K-REFRESH-TIMEOUT: decided %s after the run started, want W1 with 1 s tolerance", decided.Round(10*time.Millisecond))
	}
	if len(alive) != 0 {
		t.Errorf("K-REFRESH-TIMEOUT: PIDs %v alive, want none 2 s after W1", alive)
	}
}

// TestK_ExecSwitchDuringRefresh (acceptance 4a, E5): a connected exec context has a
// refresh run in progress (with a descendant); a switch to kind-ok. The refresh tree is
// gone within 2 s of the switch, and no line or state comes from it (3 s window).
func TestK_ExecSwitchDuringRefresh(t *testing.T) {
	srv := newCredServer(t)
	p, prog := refreshSetup(t, srv, "token=tok-"+randomHex(8)+";expiry=3", "child=detached;hang")
	untilAfterStart(p, 4*time.Second)
	go p.readOnce(30 * time.Second)
	if !prog.waitStarts(2, 5*time.Second) || !prog.waitDescendants(1, 5*time.Second) {
		t.Fatalf("preparation: the refresh run did not start with its descendant")
	}
	errBefore := len(p.stderr.list())
	switched := time.Now()
	if r := p.switchTo("kind-ok"); r.status != 202 {
		t.Fatalf("switch to kind-ok: status %d", r.status)
	}
	pids := prog.pids(2)
	alive, took := gone(pids, switched, 2*time.Second)
	final := p.mustSettle()
	newErr := window(p.stderr, errBefore, 3*time.Second)
	later, _ := p.cluster()
	t.Logf("K-SWITCH-REFRESH: refresh PIDs %v alive %v %s after the switch; final %s %s, state after 3 s %s; stderr since the switch %q",
		pids, alive, took.Round(10*time.Millisecond), final.State, str(final.Context), later.State, newErr)
	if len(alive) != 0 {
		t.Errorf("K-SWITCH-REFRESH: PIDs %v alive, want the refresh tree gone within 2 s of the switch", alive)
	}
	if final.State != "connected" || later.State != "connected" || str(later.Context) != "kind-ok" || len(newErr) != 0 {
		t.Errorf("K-SWITCH-REFRESH: state %q then %q (%s), stderr %q; want connected on kind-ok and no line", final.State, later.State, str(later.Context), newErr)
	}
}
