package perfjudge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

// event is one recorded audit line (Metadata level, the fields packet 6.4 names).
func event(id, stage, verb, user, group, resource, ns, uri string, code int, received, stageAt time.Time) []byte {
	e := map[string]any{"kind": "Event", "apiVersion": "audit.k8s.io/v1", "level": "Metadata", "auditID": id, "stage": stage,
		"verb": verb, "requestURI": uri, "user": map[string]any{"username": user},
		"requestReceivedTimestamp": received.Format(time.RFC3339Nano), "stageTimestamp": stageAt.Format(time.RFC3339Nano)}
	if resource != "" {
		e["objectRef"] = map[string]any{"apiGroup": group, "resource": resource, "namespace": ns}
	}
	if stage == "ResponseComplete" {
		e["responseStatus"] = map[string]any{"code": code}
	}
	b, _ := json.Marshal(e)
	return b
}

const (
	panelU   = "system:serviceaccount:intuitik-perf:panel"
	controlU = "system:serviceaccount:intuitik-perf:control"
	perfC    = "intuitik-perf"     // the measurement cluster
	reproC   = "intuitik-perf-410" // the 410 reproduction cluster
)

// done is one complete list or get of user.
func done(id, verb, user, group, resource, ns, uri string, ms int) []byte {
	return event(id, "ResponseComplete", verb, user, group, resource, ns, uri, 200, at(ms), at(ms+5))
}

// path is the collection path of a resource in namespace ns ("" for cluster scope).
func path(r Resource, ns string) string {
	p := "/api/" + r.Version
	if r.Group != "" {
		p = "/apis/" + r.Group + "/" + r.Version
	}
	if ns != "" && r.Resource != "persistentvolumes" && r.Resource != "storageclasses" && r.Resource != "namespaces" && r.Resource != "nodes" {
		p += "/namespaces/" + ns
	}
	return p + "/" + r.Resource
}

// served is every resource of the source tables as discovery serves it.
func served() []Resource {
	var out []Resource
	for _, r := range append(append(append([]Resource{}, clusterKinds...), namespaceKinds...), volumeKinds...) {
		r.Verbs = []string{"get", "list", "watch"}
		r.Namespaced = r.Resource != "namespaces" && r.Resource != "nodes" && r.Resource != "persistentvolumes" && r.Resource != "storageclasses"
		out = append(out, r)
	}
	return out
}

func TestScale(t *testing.T) {
	full := func(scale string) []Count {
		var cs []Count
		for _, k := range Kinds {
			want, _ := Table(scale)
			ns := "perf-001"
			if k == "Namespace" || k == "Node" {
				ns = ""
			}
			cs = append(cs, Count{Namespace: ns, Kind: k, N: want[k]})
		}
		return cs
	}
	ok := JudgeScale("L", full("L"), full("L"))
	if !ok.OK || len(ok.Errors) != 0 || last(ok.Lines) != "PERF scale=L ok" {
		t.Errorf("C1 L as built: OK=%v errors=%q last=%q, want ok", ok.OK, ok.Errors, last(ok.Lines))
	}
	// A recorded short count: Pods 4,000 of 5,000.
	short := full("L")
	for i := range short {
		if short[i].Kind == "Pod" {
			short[i].N = 4000
		}
	}
	r := JudgeScale("L", full("L"), short)
	if r.OK || last(r.Lines) != "PERF scale=L short Pod=4000/5000" {
		t.Errorf("C1 L short: OK=%v last=%q, want not ok and `PERF scale=L short Pod=4000/5000`", r.OK, last(r.Lines))
	}
	// Totals equal, one Pod in another namespace: not equal to the fixture (ERROR).
	moved := full("L")
	for i := range moved {
		if moved[i].Kind == "Pod" {
			moved[i].N--
		}
	}
	moved = append(moved, Count{Namespace: "perf-002", Kind: "Pod", N: 1})
	r = JudgeScale("L", full("L"), moved)
	if r.OK || len(r.Errors) != 2 {
		t.Errorf("C1 L moved Pod: OK=%v errors=%q, want 2 errors", r.OK, r.Errors)
	}
	// A fixture short of the table.
	r = JudgeScale("S", short, short)
	if r.OK || len(r.Errors) == 0 {
		t.Errorf("C1 S with an L fixture: OK=%v errors=%q, want errors", r.OK, r.Errors)
	}
	for _, l := range ok.Lines {
		t.Log(l)
	}
	// Every L verdict of a short scale is not-measured.
	rep := Judge(Input{Cluster: perfC, Scale: "L", Fixture: full("L"), Readback: short, Runs: []Run{{Index: 1, Samples: []int64{1}, HWM: 1}}})
	text := strings.Join(rep.Lines, "\n")
	if rep.ScaleOK || !strings.Contains(text, "PERF N-5 not-measured") || rep.Exit != 2 {
		t.Errorf("C1 short L run: ScaleOK=%v exit=%d, want not-measured N-5 and exit 2:\n%s", rep.ScaleOK, rep.Exit, text)
	}
}

func TestN5Judge(t *testing.T) {
	mb := func(ms ...int64) []int64 {
		var out []int64
		for _, m := range ms {
			out = append(out, m*1_000_000)
		}
		return out
	}
	r := JudgeN5(mb(512, 540, 601, 530, 525))
	if want := "PERF N-5 runs=512MB,540MB,601MB,530MB,525MB max=601MB limit=600MB result=FAIL"; r.Pass || r.Line("L") != want {
		t.Errorf("C2 601MB: got %q pass=%v, want %q", r.Line("L"), r.Pass, want)
	}
	if r := JudgeN5(mb(600, 600, 600, 600, 600)); !r.Pass || r.Max != 600_000_000 {
		t.Errorf("C2 600,000,000 bytes: pass=%v max=%d, want PASS", r.Pass, r.Max)
	}
	r = JudgeN5([]int64{512_000_000, 600_000_001, 1, 2, 3})
	if want := "max=600.000001MB limit=600MB result=FAIL"; r.Pass || !strings.Contains(r.Line("L"), want) {
		t.Errorf("C2 600,000,001 bytes: got %q, want %q", r.Line("L"), want)
	}
	if r := JudgeN5(nil); r.Pass {
		t.Error("C2 no peaks: PASS, want FAIL")
	}
	if p := RunPeak([]int64{10, 30, 20}, 25); p != 30 {
		t.Errorf("RunPeak samples above VmHWM: %d, want 30", p)
	}
	if p := RunPeak([]int64{10, 20}, 25); p != 25 {
		t.Errorf("RunPeak VmHWM above samples: %d, want 25", p)
	}
}

// TestRSSBand is the C3 band of a live run (decision C3-BAND-T12): the three sample and
// VmHWM pairs recorded in dispatch 44748355995 (perf-server log L1341) are a PASS, since
// packet 6.3 takes the run peak as max(VmHWM, samples); no sample, a sample of 0, VmHWM
// below 5,000,000 bytes and read errors are FAIL.
func TestRSSBand(t *testing.T) {
	readErr := errors.New("open /proc/1/status: no such file or directory")
	cases := []struct {
		name       string
		samples    []int64
		hwm        int64
		hwmErr     error
		sampleErrs []string
		pass       bool
		want       string
	}{
		{"dispatch-L1341-run1", []int64{25772032, 116572160, 116154368}, 116154368, nil, nil, true, ""},
		{"dispatch-L1341-run3", []int64{25772032, 109920256, 109760512}, 109760512, nil, nil, true, ""},
		{"dispatch-L1341-run5", []int64{25829376, 115216384, 114868224}, 114868224, nil, nil, true, ""},
		{"samples-below-hwm", []int64{25772032, 116000000}, 116154368, nil, nil, true, ""},
		{"no-sample", nil, 116154368, nil, nil, false, "run 1: no sample"},
		{"zero-sample", []int64{25772032, 0}, 116154368, nil, nil, false, "run 1 sample 2 = 0 bytes"},
		{"hwm-below-5000000", []int64{4000000}, 4999999, nil, nil, false, "run 1 VmHWM 4999999 bytes below 5000000"},
		{"hwm-read-error", []int64{25772032}, 0, readErr, nil, false, "run 1 VmHWM read: open /proc/1/status"},
		{"sample-read-error", []int64{25772032}, 116154368, nil, []string{"open /proc/1/status: no such process"}, false, "run 1 sample read: open /proc/1/status"},
	}
	for _, c := range cases {
		problems := RSSBand(1, c.samples, c.hwm, c.hwmErr, c.sampleErrs)
		pass := len(problems) == 0
		t.Logf("C3 band case=%s samples=%v hwm=%d problems=%q pass=%v want-pass=%v", c.name, c.samples, c.hwm, problems, pass, c.pass)
		if pass != c.pass {
			t.Errorf("C3 band case=%s: pass=%v problems=%q, want pass=%v", c.name, pass, problems, c.pass)
			continue
		}
		if !c.pass && !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("C3 band case=%s: problems=%q, want one containing %q", c.name, problems, c.want)
		}
	}
}

// perfBig is the recorded audit log of one perf-big call: every expected resource listed
// once with limit=500, and the Pods in pods pages (all but the first with continue).
func perfBig(pods int, skipLimit string) [][]byte {
	var lines [][]byte
	ms := 0
	for i, r := range ExpectedSet(false, served(), true) {
		n := 1
		if r.Resource == "pods" {
			n = pods
		}
		for p := 0; p < n; p++ {
			uri := path(r, "perf-big") + "?limit=500"
			if r.Key() == skipLimit {
				uri = path(r, "perf-big")
			}
			if p > 0 {
				uri += "&continue=token" + fmt.Sprint(p)
			}
			ms += 10
			lines = append(lines, done(fmt.Sprintf("id-%d-%d", i, p), "list", panelU, r.Group, r.Resource, "perf-big", uri, ms))
		}
	}
	lines = append(lines, done("id-get", "get", panelU, "apps", "replicasets", "perf-big", "/apis/apps/v1/namespaces/perf-big/replicasets/x", ms+10))
	return lines
}

func TestPageJudge(t *testing.T) {
	span := Span{t0, at(60_000)}
	expected := ExpectedSet(false, served(), true)
	judge := func(lines [][]byte, nodes int) []PageLine {
		return JudgePages("perf-big", ParseAudit(perfC, lines).Requests, panelU, span, expected, true, 1200, nodes)
	}
	failed := func(ls []PageLine) []string {
		var out []string
		for _, l := range ls {
			t.Log(l.Line())
			if !l.Pass {
				out = append(out, l.Resource+": "+strings.Join(l.Shortfall, ";"))
			}
		}
		return out
	}
	ls := judge(perfBig(3, ""), 1200)
	if f := failed(ls); len(ls) != len(expected) || len(f) != 0 {
		t.Errorf("C4 3 pages of 500 and 1,200 Pod nodes: %d lines, failed %q; want %d lines, none failed", len(ls), f, len(expected))
	}
	for _, l := range ls {
		if l.Resource == "pods" && (l.Lists != 3 || l.Continue != 2 || strings.Join(l.Limits, ",") != "500") {
			t.Errorf("C4 pods line %q, want list=3 limit=500 continue=2", l.Line())
		}
		if l.Resource == "replicasets.apps" && l.Gets != 1 {
			t.Errorf("C4 replicasets line %q, want get=1", l.Line())
		}
	}
	for _, c := range []struct {
		name  string
		lines [][]byte
		nodes int
		want  string
	}{
		{"one list without limit", perfBig(3, "services"), 1200, "services: list 1 limit=none"},
		{"2 pages only", perfBig(2, ""), 1200, "pods: pages 2/3;continue 1/2"},
		{"3 pages and 1,000 Pod nodes", perfBig(3, ""), 1000, "pods: pod nodes 1000/1200"},
	} {
		if f := failed(judge(c.lines, c.nodes)); len(f) != 1 || f[0] != c.want {
			t.Errorf("C4 %s: failed %q, want [%q]", c.name, f, c.want)
		}
	}
	// The product today: every list without limit.
	for _, l := range judge(perfBig(1, "pods"), 1200) {
		if l.Resource == "pods" && (l.Pass || !strings.Contains(l.Line(), "limit=none") || !strings.Contains(l.Line(), "handled-in=T11")) {
			t.Errorf("C4 pods without limit: %q, want FAIL with limit=none and handled-in=T11", l.Line())
		}
	}
	body, _ := json.Marshal(map[string]any{"nodes": []any{
		map[string]any{"apiVersion": "v1", "kind": "Pod"}, map[string]any{"apiVersion": "v1", "kind": "Pod"},
		map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet"}, map[string]any{"apiVersion": "example.com/v1", "kind": "Pod"}}})
	if n, err := CountPodNodes(body); n != 2 || err != nil {
		t.Errorf("CountPodNodes: %d %v, want 2", n, err)
	}
}

func TestAuditUnit(t *testing.T) {
	lines := [][]byte{
		done("list-1", "list", panelU, "", "pods", "perf-001", "/api/v1/namespaces/perf-001/pods", 0),
		done("list-1", "list", panelU, "", "pods", "perf-001", "/api/v1/namespaces/perf-001/pods", 0), // duplicate stage line
		event("watch-1", "ResponseStarted", "watch", controlU, "", "pods", "perf-001", "/api/v1/namespaces/perf-001/pods?watch=1", 0, at(100), at(110)),
		event("watch-1", "ResponseComplete", "watch", controlU, "", "pods", "perf-001", "/api/v1/namespaces/perf-001/pods?watch=1", 200, at(100), at(900)),
	}
	a := ParseAudit(perfC, lines)
	if len(a.Requests) != 2 || len(a.Duplicates) != 1 || len(a.Problems) != 0 || a.Lines != 4 {
		t.Fatalf("duplicate stage line: requests=%d duplicates=%q problems=%q lines=%d, want 2, 1, none, 4", len(a.Requests), a.Duplicates, a.Problems, a.Lines)
	}
	w := a.Requests[1]
	if w.Verb != "watch" || !w.End.Equal(at(900)) || !w.Received.Equal(at(100)) || w.User != controlU || w.Namespace != "perf-001" || w.Cluster != perfC {
		t.Errorf("watch pair: %+v, want received at 100 ms and end at 900 ms", w)
	}
	for _, c := range []struct {
		name string
		line []byte
		want string
	}{
		{"orphan watch end", event("watch-2", "ResponseComplete", "watch", controlU, "", "pods", "perf-001", "/x", 200, at(0), at(5)), "watch watch-2 ends without its ResponseStarted line"},
		{"watch without end", event("watch-3", "ResponseStarted", "watch", controlU, "", "pods", "perf-001", "/x", 0, at(0), at(5)), "watch watch-3 has no ResponseComplete line"},
		{"list without ResponseComplete", event("list-2", "ResponseStarted", "list", panelU, "", "pods", "perf-001", "/x", 0, at(0), at(5)), "list list-2 has no ResponseComplete line"},
		{"line that does not decode", []byte(`{"auditID":`), "audit line 1 does not decode as an event with auditID and stage"},
	} {
		a := ParseAudit(perfC, [][]byte{c.line})
		if len(a.Problems) != 1 || a.Problems[0] != c.want || len(a.Requests) != 0 {
			t.Errorf("%s: problems %q requests %d, want [%q] and none counted", c.name, a.Problems, len(a.Requests), c.want)
		}
	}
	// An orphan line makes the judge exit 2.
	rep := Judge(Input{Cluster: perfC, Scale: "S", Audit: [][]byte{event("watch-2", "ResponseComplete", "watch", controlU, "", "pods", "perf-001", "/x", 200, at(0), at(5))}})
	if rep.Exit != 2 || !strings.Contains(strings.Join(rep.Lines, "\n"), "PERF ERROR audit watch watch-2 ends without its ResponseStarted line cluster=intuitik-perf") {
		t.Errorf("orphan watch end: judge exit %d, want 2 with the ERROR line", rep.Exit)
	}
	// Every line names the cluster it judged.
	for _, l := range rep.Lines {
		if !strings.HasPrefix(l, "PERF ") || !strings.HasSuffix(l, " cluster=intuitik-perf") {
			t.Errorf("judge line without its cluster: %q", l)
		}
	}
}

func TestIdentityFilter(t *testing.T) {
	span := Span{t0, at(60_000)}
	cluster, namespace := ExpectedSet(true, served(), true), ExpectedSet(false, served(), true)
	if len(cluster) != 4 || len(namespace) != 15 {
		t.Fatalf("expected sets: cluster %d, namespace %d; want 4 and 15", len(cluster), len(namespace))
	}
	extra := append(served(), Resource{Group: "example.com", Version: "v1", Resource: "widgets", Namespaced: true, Verbs: []string{"list"}},
		Resource{Group: "example.com", Version: "v1", Resource: "gadgets", Namespaced: false, Verbs: []string{"list"}})
	if got := ExpectedSet(false, extra, true); len(got) != 16 || got[15].Key() != "widgets.example.com" {
		t.Errorf("namespace set with a custom group: %d entries, want 16 ending in widgets.example.com", len(got))
	}
	var noHPA []Resource
	for _, r := range served() {
		if r.Resource != "horizontalpodautoscalers" {
			noHPA = append(noHPA, r)
		}
	}
	if got := ExpectedSet(false, noHPA, false); len(got) != 12 {
		t.Errorf("namespace set without HPA served and without claims: %d, want 12", len(got))
	}
	calls := func(user string, set []Resource, skip string) [][]byte {
		var out [][]byte
		for i, r := range set {
			if r.Key() == skip {
				continue
			}
			out = append(out, done(fmt.Sprint("c-", i), "list", user, r.Group, r.Resource, "perf-001", path(r, "perf-001"), 100*i))
		}
		return out
	}
	for _, c := range []struct {
		name    string
		lines   [][]byte
		set     []Resource
		pass    bool
		matched int
		missing string
	}{
		{"cluster call by the panel", calls(panelU, cluster, ""), cluster, true, 4, ""},
		{"namespace call by the panel", calls(panelU, namespace, ""), namespace, true, 15, ""},
		{"empty log", nil, cluster, false, 0, "namespaces,nodes,storageclasses.storage.k8s.io,persistentvolumes"},
		{"another user", calls(controlU, cluster, ""), cluster, false, 0, "namespaces,nodes,storageclasses.storage.k8s.io,persistentvolumes"},
		{"one resource not listed", calls(panelU, namespace, "secrets"), namespace, false, 14, "secrets"},
	} {
		s := JudgeIdentity("call", ParseAudit(perfC, c.lines).Requests, panelU, span, c.set)
		t.Log(s.Line())
		if s.Pass != c.pass || s.Matched != c.matched || strings.Join(s.Missing, ",") != c.missing || s.K != len(c.set) {
			t.Errorf("C5 %s: %q, want pass=%v matched=%d K=%d missing=%q", c.name, s.Line(), c.pass, c.matched, len(c.set), c.missing)
		}
	}
}

// rec is one cause record of the control client in cluster cl.
func rec(cl, id, verb, path string, ev *RecordEvent) []byte {
	b, _ := json.Marshal(Record{Cluster: cl, AuditID: id, Verb: verb, Path: path, Event: ev})
	return b
}

// cronjobs is the collection of the C6 410 rows (packet 6.3 r5: batch/v1 cronjobs).
const cronjobs = "/apis/batch/v1/namespaces/perf-001/cronjobs"

// rows are the C6 table rows of packet 6.3 as recorded audit lines and cause records of
// the reproduction cluster, all batch/v1 cronjobs, starting at ms: row a (a watch of
// perf-001 that ends with ERROR 410, then a list of perf-001), and with mixed rows b (a
// cancelled watch, then a list of perf-001 within 5 s), c (a list of perf-002) and d (a
// second list of perf-001). Every auditID starts with id.
func rows(id string, ms int, mixed bool) ([][]byte, [][]byte) {
	watch := func(wid string, start, end int) [][]byte {
		return [][]byte{
			event(wid, "ResponseStarted", "watch", controlU, "batch", "cronjobs", "perf-001", cronjobs+"?watch=1", 0, at(start), at(start+5)),
			event(wid, "ResponseComplete", "watch", controlU, "batch", "cronjobs", "perf-001", cronjobs+"?watch=1", 200, at(start), at(end)),
		}
	}
	audit := watch(id+"a-watch", ms, ms+100)
	audit = append(audit, done(id+"a-list", "list", controlU, "batch", "cronjobs", "perf-001", cronjobs, ms+600))
	records := [][]byte{rec(reproC, id+"a-watch", "watch", cronjobs, &RecordEvent{Type: "ERROR", Code: 410}), rec(reproC, id+"a-list", "list", cronjobs, nil)}
	if mixed {
		audit = append(audit, watch(id+"b-watch", ms+700, ms+1200)...)
		audit = append(audit,
			done(id+"b-list", "list", controlU, "batch", "cronjobs", "perf-001", cronjobs, ms+2200),
			done(id+"c-list", "list", controlU, "batch", "cronjobs", "perf-002", "/apis/batch/v1/namespaces/perf-002/cronjobs", ms+2300),
			done(id+"d-list", "list", controlU, "batch", "cronjobs", "perf-001", cronjobs, ms+2400))
		records = append(records, rec(reproC, id+"b-watch", "watch", cronjobs, nil), rec(reproC, id+"b-list", "list", cronjobs, nil),
			rec(reproC, id+"c-list", "list", "/apis/batch/v1/namespaces/perf-002/cronjobs", nil), rec(reproC, id+"d-list", "list", cronjobs, nil))
	}
	return audit, records
}

func TestIdleDetector(t *testing.T) {
	classify := func(cl string, audit, records [][]byte, span Span) Counts {
		recs, problems := ParseRecords(records)
		if len(problems) != 0 {
			t.Fatalf("records: %q", problems)
		}
		c, detail := Classify(ParseAudit(cl, audit).Requests, controlU, span, recs)
		for _, d := range detail {
			t.Log(d)
		}
		return c
	}
	// Noisy window: 10 lists at a fixed 6 s interval in 60 s.
	var noisy [][]byte
	for i := 0; i < 10; i++ {
		noisy = append(noisy, done(fmt.Sprint("n-", i), "list", controlU, "", "pods", "perf-001", "/api/v1/namespaces/perf-001/pods", 500+6000*i))
	}
	span := Span{t0, at(60_000)}
	if c, want := classify(perfC, noisy, nil, span), ExpectedPeriodic(60*time.Second, 6*time.Second); c.Periodic != 10 || want != 10 || c.Pass() {
		t.Errorf("C6 noisy: %s (expected %d), want periodic=10 and FAIL", c, want)
	}
	// Quiet window: no list.
	if c := classify(perfC, nil, nil, span); c != (Counts{}) || !c.Pass() {
		t.Errorf("C6 quiet: %s, want 0 and PASS", c)
	}
	// Window (3a): row a alone.
	a, ra := rows("", 1000, false)
	if c := classify(reproC, a, ra, span); c != (Counts{Relist410: 1}) || !c.Pass() {
		t.Errorf("C6 410 positive (row a): %s, want relist-410=1 unverified=0 periodic=0 and PASS", c)
	}
	// Window (3b): rows a to d.
	m, rm := rows("", 1000, true)
	if c := classify(reproC, m, rm, span); c != (Counts{Relist410: 1, Unverified: 2, Periodic: 1}) || c.Pass() {
		t.Errorf("C6 410 mixed (rows a to d): %s, want relist-410=1 unverified=2 periodic=1 and FAIL", c)
	}
	// Row a without its 410 record (a normal end): unverified, not relist-410.
	if c := classify(reproC, a, ra[1:], span); c != (Counts{Unverified: 1}) {
		t.Errorf("C6 watch end without a 410 record: %s, want unverified=1", c)
	}
	// A list 6 s after the watch end does not follow it.
	late := append(append([][]byte{}, a[:2]...), done("a-list", "list", controlU, "batch", "cronjobs", "perf-001", cronjobs, 1000+100+6000))
	if c := classify(reproC, late, ra, span); c != (Counts{Periodic: 1}) {
		t.Errorf("C6 list 6 s after a 410 watch end: %s, want periodic=1", c)
	}
	// r5: the 410 record of the batch/v1 cronjobs watch binds no list of another resource
	// or group in the same namespace: a list of core/v1 pods, or of group apps with the
	// resource name cronjobs, is periodic (no watch on that resource ended), not relist-410.
	for _, other := range []struct{ group, resource, path string }{
		{"", "pods", "/api/v1/namespaces/perf-001/pods"},
		{"apps", "cronjobs", "/apis/apps/v1/namespaces/perf-001/cronjobs"},
	} {
		wrong := append(append([][]byte{}, a[:2]...), done("a-list", "list", controlU, other.group, other.resource, "perf-001", other.path, 1600))
		recs := [][]byte{ra[0], rec(reproC, "a-list", "list", other.path, nil)}
		if c := classify(reproC, wrong, recs, span); c != (Counts{Periodic: 1}) {
			t.Errorf("C6 410 record of batch cronjobs and a list of %q %s: %s, want periodic=1", other.group, other.resource, c)
		}
	}
	// r5.1: the records of row a written in the measurement cluster bind nothing in the
	// reproduction cluster: the list follows the watch end without a record of its cluster.
	var elsewhere [][]byte
	for _, r := range [][2]string{{"a-watch", "watch"}, {"a-list", "list"}} {
		var ev *RecordEvent
		if r[1] == "watch" {
			ev = &RecordEvent{Type: "ERROR", Code: 410}
		}
		elsewhere = append(elsewhere, rec(perfC, r[0], r[1], cronjobs, ev))
	}
	if c := classify(reproC, a, elsewhere, span); c != (Counts{Unverified: 1}) {
		t.Errorf("C6 row a with the records of another cluster: %s, want unverified=1", c)
	}
	// The judge of the reproduction cluster: window (3a) holds row a, window (3b) rows a
	// to d; a record of the measurement cluster in its input is an ERROR.
	pa, pra := rows("3a-", 1000, false)
	ma, mra := rows("3b-", 61_000, true)
	in := ReproInput{Cluster: reproC, PanelUser: panelU, ControlUser: controlU,
		Positive: Window{Span: Span{t0, at(60_000)}}, Mixed: Window{Span: Span{at(60_000), at(120_000)}},
		Audit: append(pa, ma...), Records: append(pra, mra...)}
	rep := JudgeRepro(in)
	for _, l := range rep.Lines {
		t.Log(l)
		if !strings.HasSuffix(l, " cluster=intuitik-perf-410") {
			t.Errorf("reproduction judge line without its cluster: %q", l)
		}
	}
	if rep.Positive != (Counts{Relist410: 1}) || rep.Mixed != (Counts{Relist410: 1, Unverified: 2, Periodic: 1}) || rep.Exit != 0 {
		t.Errorf("JudgeRepro: 3a %s, 3b %s, exit %d; want relist-410=1 alone, then 1 2 1, exit 0", rep.Positive, rep.Mixed, rep.Exit)
	}
	in.Records = append(in.Records, rec(perfC, "x-list", "list", cronjobs, nil))
	if rep := JudgeRepro(in); rep.Exit != 2 {
		t.Errorf("JudgeRepro with a record of the measurement cluster: exit %d, want 2", rep.Exit)
	}
}

// last is the last line, "" when there is none.
func last(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}
