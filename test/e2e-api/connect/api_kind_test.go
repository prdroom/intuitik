//go:build kind

package connect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestK_SwitchUnknownContext (packet section 6.1): a switch to a name that is not in the
// list is refused with 400 and K-UC; the state and the active context do not change.
func TestK_SwitchUnknownContext(t *testing.T) {
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "kind-ok"}).withKind(t).bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	before := p.mustSettle()
	r := p.switchTo("no-such-context")
	var body map[string]any
	json.Unmarshal(r.body, &body)
	want := map[string]any{"text_id": "K-UC", "message": fmt.Sprintf(textUC, "no-such-context")}
	t.Logf("K-UC: expected 400 %v, actual %d %s", want, r.status, r.body)
	if r.status != 400 || fmt.Sprint(body) != fmt.Sprint(want) {
		t.Errorf("K-UC: answer %d %s, want 400 %v", r.status, r.body, want)
	}
	time.Sleep(500 * time.Millisecond)
	after, _ := p.cluster()
	t.Logf("K-UC: state %s -> %s, active %q -> %q", before.State, after.State, before.flagged(false), after.flagged(false))
	if after.State != before.State || str(after.Context) != str(before.Context) || after.flagged(false) != before.flagged(false) {
		t.Errorf("K-UC: the refused switch changed the state or the active context: before %s %s, after %s %s", before.State, str(before.Context), after.State, str(after.Context))
	}
}

// TestK_SwitchCancelsRunningAttempt (packet section 6.1): a switch while an attempt runs
// cancels it. The test switches to a context whose server never completes the TLS
// handshake, waits until that server reports the connection, and switches back before
// W1: the final state is connected and no K-UR line is printed for the canceled attempt.
func TestK_SwitchCancelsRunningAttempt(t *testing.T) {
	held := newHeldServer(t)
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "kind-ok"}).withKind(t).
		cluster("held-c", held.url(), kindCluster(t).caData).context("held", "held-c", "kind-admin").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	if o := p.mustSettle(); o.State != "connected" {
		t.Fatalf("start: state %q, want connected (%s)", o.State, str(o.Message))
	}
	if r := p.switchTo("held"); r.status != 202 {
		t.Fatalf("switch to held: status %d", r.status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for held.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("K-CANCEL held server connections after the switch: %d", held.count())
	if held.count() == 0 {
		t.Fatal("the attempt for held never reached the held server")
	}
	switched := time.Now()
	if r := p.switchTo("kind-ok"); r.status != 202 {
		t.Fatalf("switch back to kind-ok: status %d", r.status)
	}
	final := p.mustSettle()
	// Wait past W1 of the canceled attempt, so that a late result would show.
	time.Sleep(w1 + 2*time.Second - time.Since(switched))
	later, _ := p.cluster()
	urLine := fmt.Sprintf("Cannot reach the API server %s of context held", held.url())
	urLines := 0
	for _, l := range p.stderr.list() {
		if strings.HasPrefix(l, urLine) {
			urLines++
		}
	}
	t.Logf("K-CANCEL final state %s, state %s after W1, context %s, K-UR lines for held: %d", final.State, later.State, str(later.Context), urLines)
	if final.State != "connected" || later.State != "connected" || str(later.Context) != "kind-ok" {
		t.Errorf("K-CANCEL: state %q then %q (context %s), want connected on kind-ok", final.State, later.State, str(later.Context))
	}
	if urLines != 0 {
		t.Errorf("K-CANCEL: %d K-UR lines were printed for the canceled attempt: %q", urLines, p.stderr.list())
	}
}

// TestK_NoSessionNoClusterData (packet sections 3.4 and 6.1, T2 S2): every T3 route
// answers 401 without a session, and the body names no context, cluster or server.
func TestK_NoSessionNoClusterData(t *testing.T) {
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "kind-ok"}).withKind(t).bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	p.mustSettle()
	names := []string{"kind-ok", "kind-c", kindCluster(t).server, strings.TrimPrefix(kindCluster(t).server, "https://")}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/cluster", ""},
		{"POST", "/api/cluster/context", `{"name":"kind-ok"}`},
		{"POST", "/api/cluster/retry", "{}"},
	} {
		r := p.request(c.method, c.path, []byte(c.body), false)
		found := 0
		for _, n := range names {
			found += bytes.Count(r.raw, []byte(n))
		}
		t.Logf("K-NOSESSION %s %s: status %d, names found in the answer %d", c.method, c.path, r.status, found)
		if r.status != 401 || found != 0 {
			t.Errorf("K-NOSESSION %s %s: status %d with %d names in %q, want 401 with none", c.method, c.path, r.status, found, firstLines(r.raw, 12))
		}
	}
}

// TestK_ClusterRouteGate (packet section 6.2, D1): the e2etest build registers one probe
// route through clusterRoute. In every failure state of 3.2 the probe answers 503 with
// the state and text of /api/cluster, and its handler is not called: after the panel
// reaches the kind context, the first successful answer counts 1 call.
func TestK_ClusterRouteGate(t *testing.T) {
	for _, f := range stateFixtures() {
		if f.state == "connected" {
			continue
		}
		t.Run(f.id, func(t *testing.T) {
			run := f.setup(t, t.TempDir())
			p := startPanel(t, panelOpts{kubeconfig: run.kubeconfig, test: true, fixture: "cluster-probe-route"})
			o := p.mustSettle()
			if o.State != f.state {
				t.Errorf("K-GATE %s: state %q, want %q", f.name, o.State, f.state)
			}
			for i := 0; i < 3; i++ {
				r := p.request("GET", "/api/fixture-cluster-probe", nil, true)
				var g clusterObj
				json.Unmarshal(r.body, &g)
				t.Logf("K-GATE %s: probe answer %d state=%s text_id=%s", f.name, r.status, g.State, str(g.TextID))
				if r.status != 503 || g.State != o.State || str(g.TextID) != str(o.TextID) || str(g.Message) != str(o.Message) {
					t.Errorf("K-GATE %s: probe answered %d %s, want 503 with state %s text %s", f.name, r.status, r.body, o.State, str(o.TextID))
				}
			}
			if r := p.request("GET", "/api/fixture-cluster-probe", nil, false); r.status != 401 {
				t.Errorf("K-GATE %s: probe without a session answered %d, want 401", f.name, r.status)
			}
			if run.recover == nil {
				return
			}
			run.recover(p)
			if after := p.mustSettle(); after.State != "connected" {
				t.Fatalf("K-GATE %s: the kind context did not connect: %s", f.name, str(after.Message))
			}
			r := p.request("GET", "/api/fixture-cluster-probe", nil, true)
			var calls struct{ Calls int }
			json.Unmarshal(r.body, &calls)
			t.Logf("K-GATE %s: connected probe answer %d, handler calls %d (want 200 and 1: the 503 answers made 0 calls)", f.name, r.status, calls.Calls)
			if r.status != 200 || calls.Calls != 1 {
				t.Errorf("K-GATE %s: connected probe %d %s, want 200 with calls 1", f.name, r.status, r.body)
			}
		})
	}
}
