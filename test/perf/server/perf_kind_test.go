//go:build kind

package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/test/perf/server/gen"
	"github.com/prdroom/intuitik/tools/perfjudge"
	"github.com/prdroom/intuitik/tools/perfjudge/sampler"
)

// size is what one scale runs (packet 6.2, 6.3): the number of runs, the product idle
// window, the noisy window and its interval, and the readiness limit.
type size struct {
	runs           int
	idle           time.Duration
	noisy, every   time.Duration
	readiness      time.Duration
	controlWindows bool
}

var sizes = map[string]size{
	"S":  {runs: 1, idle: 60 * time.Second, noisy: 60 * time.Second, every: 6 * time.Second, readiness: 5 * time.Minute, controlWindows: true},
	"L":  {runs: perfjudge.N5Runs, idle: 10 * time.Minute, noisy: 10 * time.Minute, every: 60 * time.Second, readiness: 15 * time.Minute, controlWindows: true},
	"XL": {runs: 1, readiness: 40 * time.Minute},
}

// kinds are the labelled kinds the harness reads back, with their list paths (all
// namespaces) and the fixture kind name.
var readKinds = []struct{ kind, path string }{
	{"Namespace", "/api/v1/namespaces"}, {"Node", "/api/v1/nodes"},
	{"Deployment", "/apis/apps/v1/deployments"}, {"ReplicaSet", "/apis/apps/v1/replicasets"}, {"Pod", "/api/v1/pods"},
	{"Service", "/api/v1/services"}, {"PersistentVolumeClaim", "/api/v1/persistentvolumeclaims"},
	{"ConfigMap", "/api/v1/configmaps"}, {"Secret", "/api/v1/secrets"}, {"Event", "/api/v1/events"},
}

var labelSelector = url.QueryEscape(gen.Label + "=" + gen.LabelValue)

// TestPerfServer runs the harness at the scale of INTUITIK_PERF_SCALE (S on pull_request,
// L and then XL on workflow_dispatch). Preparation failures stop the test (PERF
// verdict=ERROR); after the cluster is ready every harness check is an error that lets
// the others run (packet 6.4).
func TestPerfServer(t *testing.T) {
	scale := env(t, "INTUITIK_PERF_SCALE")
	sz, ok := sizes[scale]
	plan, err := gen.For(scale)
	if !ok || err != nil {
		t.Fatalf("PERF verdict=ERROR scale %q: %v", scale, err)
	}
	work, out := env(t, "INTUITIK_PERF_WORK"), env(t, "INTUITIK_PERF_OUT")
	say("PERF-START scale=%s runs=%d idle=%s noisy=%s every=%s readiness-limit=%s", scale, sz.runs, sz.idle, sz.noisy, sz.every, sz.readiness)
	for _, l := range plan.Table() {
		say("%s", l)
	}

	c := createCluster(t, work)
	panelToken, controlToken := c.rbac(t)
	startKwok(t, c, work)
	build(t, c, plan)
	if !ready(t, c, plan, sz.readiness) {
		if scale == "XL" {
			say("PERF scale=XL not-measured (readiness limit %s reached; information only, P-T12-4)", sz.readiness)
			return
		}
		t.Fatalf("PERF scale=%s not-measured verdict=ERROR: readiness not reached in %s (named stop, packet 8)", scale, sz.readiness)
	}
	readback, info := counts(t, c)
	for _, l := range info {
		say("%s", l)
	}
	k := &control{c: c, client: c.tokenClient(controlToken)}
	served := discover(t, c, k)

	in := perfjudge.Input{Scale: scale, Fixture: plan.Counts(), Readback: readback, Served: served,
		PanelUser: panelUser, ControlUser: controlUser, Measured: plan.Measured, MeasuredPods: plan.Pods(plan.Measured)}
	kc := c.tokenKubeconfig(t, "perf-panel", panelToken)
	var c3 []string
	for i := 1; i <= sz.runs; i++ {
		r, problems := measure(t, kc, i, sz.idle)
		in.Runs = append(in.Runs, r)
		c3 = append(c3, problems...)
		if i == 1 && scale != "XL" {
			in.Files = append(in.Files, saveFiles(t, out, scale, r)...)
		}
	}
	if sz.controlWindows {
		t0 := time.Now()
		in.Noisy = k.noisyWindow(t, sz.noisy, sz.every)
		in.Positive = k.window410(t, "410-positive", false)
		in.Mixed = k.window410(t, "410-mixed", true)
		phase("control-windows", t0)
	}
	in.Audit = readAudit(t)
	in.Records = k.records

	t0 := time.Now()
	rep := perfjudge.Judge(in)
	for _, l := range rep.Lines {
		say("%s", l)
	}
	phase("judge", t0)
	say("PERF judge exit=%d", rep.Exit)
	if scale == "XL" {
		say("PERF scale=XL information only (P-T12-4): judge exit %d is not a verdict", rep.Exit)
		return
	}
	harness(t, in, rep, c3)
}

// build creates the synthetic cluster of the plan.
func build(t *testing.T, c *cluster, plan gen.Plan) {
	t.Helper()
	var nodes, nss, rest []object
	for _, o := range plan.NodeObjects() {
		nodes = append(nodes, object{o.Path, o.Body})
	}
	for _, o := range plan.NamespaceObjects() {
		nss = append(nss, object{o.Path, o.Body})
	}
	for _, ns := range plan.Namespaces {
		for _, o := range ns.Objects() {
			rest = append(rest, object{o.Path, o.Body})
		}
	}
	c.mustCreate(t, "nodes", nodes)
	c.mustCreate(t, "namespaces", nss)
	// The default ServiceAccount of a new namespace comes from a controller; Pods of a
	// namespace are admitted only after it exists.
	t0 := time.Now()
	for _, ns := range plan.Namespaces {
		for {
			status, _, _, err := c.do(t.Context(), c.admin, "GET", "/api/v1/namespaces/"+ns.Name+"/serviceaccounts/default", nil, "")
			if err == nil && status == 200 {
				break
			}
			if time.Since(t0) > 2*time.Minute {
				t.Fatalf("PERF verdict=ERROR preparation: no default ServiceAccount in %s after 2 minutes", ns.Name)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	c.mustCreate(t, "objects", rest)
}

// ready waits until every labelled Pod is Running and ready and the ReplicaSets and Pods
// of every namespace equal the plan, printing progress, the elapsed time and the runner's
// memory; false when the limit is reached.
func ready(t *testing.T, c *cluster, plan gen.Plan, limit time.Duration) bool {
	t.Helper()
	t0 := time.Now()
	wantPods, wantRS := map[string]int{}, map[string]int{}
	total := 0
	for _, ns := range plan.Namespaces {
		wantRS[ns.Name] = len(ns.Deployments)
		wantPods[ns.Name] = plan.Pods(ns.Name)
		total += wantPods[ns.Name]
	}
	for {
		pods := c.list(t, "/api/v1/pods", labelSelector, false)
		rss := c.list(t, "/apis/apps/v1/replicasets", labelSelector, true)
		readyPods, perNS, rsNS := 0, map[string]int{}, map[string]int{}
		for _, p := range pods {
			ok := p.Status.Phase == "Running"
			cond := false
			for _, cd := range p.Status.Conditions {
				if cd.Type == "Ready" && cd.Status == "True" {
					cond = true
				}
			}
			if ok && cond {
				readyPods++
				perNS[p.Metadata.Namespace]++
			}
		}
		for _, r := range rss {
			rsNS[r.Metadata.Namespace]++
		}
		equal := len(pods) == total
		for ns := range wantPods {
			equal = equal && perNS[ns] == wantPods[ns] && rsNS[ns] == wantRS[ns]
		}
		say("PERF-READY elapsed_s=%.0f pods=%d ready=%d want=%d replicasets=%d", time.Since(t0).Seconds(), len(pods), readyPods, total, len(rss))
		if equal {
			phase("readiness", t0)
			freeM()
			return true
		}
		if time.Since(t0) > limit {
			phase("readiness-limit", t0)
			freeM()
			return false
		}
		time.Sleep(10 * time.Second)
	}
}

// freeM prints the runner's memory (packet 7, 8: readiness and memory are named stops).
func freeM() {
	out, err := exec.Command("free", "-m").Output()
	say("PERF-MEMORY free -m (%v):\n%s", err, strings.TrimRight(string(out), "\n"))
}

// counts reads back the labelled objects per kind and namespace, and prints the
// unlabelled objects (kind's own, the harness's namespace) per kind as information.
func counts(t *testing.T, c *cluster) ([]perfjudge.Count, []string) {
	t.Helper()
	var out []perfjudge.Count
	var info []string
	for _, k := range readKinds {
		per := map[string]int{}
		for _, it := range c.list(t, k.path, labelSelector, true) {
			per[it.Metadata.Namespace]++
		}
		var nss []string
		for ns := range per {
			nss = append(nss, ns)
		}
		sort.Strings(nss)
		for _, ns := range nss {
			out = append(out, perfjudge.Count{Namespace: ns, Kind: k.kind, N: per[ns]})
		}
		other := c.list(t, k.path, url.QueryEscape("!"+gen.Label), true)
		info = append(info, fmt.Sprintf("PERF unlabelled kind=%s count=%d (information)", k.kind, len(other)))
	}
	return out, info
}

// discover reads the served resources with the control identity (packet 6.3 C5) and
// prints each with group, version, resource and scope.
func discover(t *testing.T, c *cluster, k *control) []perfjudge.Resource {
	t.Helper()
	get := func(path string, v any) {
		status, b, id, err := c.do(t.Context(), k.client, "GET", path, nil, "application/json")
		if err != nil || status != 200 || json.Unmarshal(b, v) != nil {
			t.Fatalf("PERF verdict=ERROR discovery %s: status %d %v", path, status, err)
		}
		k.record(perfjudge.Record{AuditID: id, Verb: "get", Path: path})
	}
	type resourceList struct {
		Resources []struct {
			Name       string   `json:"name"`
			Namespaced bool     `json:"namespaced"`
			Verbs      []string `json:"verbs"`
		} `json:"resources"`
	}
	var out []perfjudge.Resource
	add := func(group, version string, rl resourceList) {
		for _, r := range rl.Resources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			out = append(out, perfjudge.Resource{Group: group, Version: version, Resource: r.Name, Namespaced: r.Namespaced, Verbs: r.Verbs})
		}
	}
	var core resourceList
	get("/api/v1", &core)
	add("", "v1", core)
	var groups struct {
		Groups []struct {
			Name     string `json:"name"`
			Versions []struct {
				Version string `json:"version"`
			} `json:"versions"`
		} `json:"groups"`
	}
	get("/apis", &groups)
	for _, g := range groups.Groups {
		for _, v := range g.Versions {
			var rl resourceList
			get("/apis/"+g.Name+"/"+v.Version, &rl)
			add(g.Name, v.Version, rl)
		}
	}
	for _, r := range out {
		scope := "cluster"
		if r.Namespaced {
			scope = "namespaced"
		}
		say("PERF discovery group=%q version=%s resource=%s scope=%s verbs=%s", r.Group, r.Version, r.Resource, scope, strings.Join(r.Verbs, ","))
	}
	return out
}

// measure is one run (packet 3.2, 6.3): start the binary, connect, call the cluster graph
// and then the graph of every namespace the cluster answer lists, in order, stay idle for
// the product window, read VmHWM and stop. RSS is sampled every 1 s through the whole run.
// The returned problems are C3 band failures of the run.
func measure(t *testing.T, kubeconfig string, index int, idle time.Duration) (perfjudge.Run, []string) {
	t.Helper()
	t0 := time.Now()
	r := perfjudge.Run{Index: index, Interval: time.Second}
	p, err := startPanel(t, kubeconfig)
	if err != nil {
		t.Fatalf("PERF verdict=ERROR run %d: %v", index, err)
	}
	pid := p.cmd.Process.Pid
	s := sampler.Start(pid, r.Interval)
	call := func(name, path string) perfjudge.Call {
		c := perfjudge.Call{Name: name, Span: perfjudge.Span{Start: time.Now()}}
		status, body, err := p.request("GET", path, nil, true)
		c.Span.End = time.Now()
		if err != nil {
			say("PERF-CALL run=%d name=%s error %v", index, name, err)
		}
		c.Status, c.Body = status, body
		say("PERF-CALL run=%d name=%s start=%s end=%s status=%d bytes=%d", index, name, stamp(c.Span.Start), stamp(c.Span.End), status, len(body))
		return c
	}
	cl := call("cluster", "/api/graph/cluster")
	r.Calls = append(r.Calls, cl)
	var answer struct {
		Namespaces struct {
			Items []string `json:"items"`
		} `json:"namespaces"`
	}
	if err := json.Unmarshal(cl.Body, &answer); err != nil || len(answer.Namespaces.Items) == 0 {
		t.Errorf("PERF-HARNESS run %d: the cluster answer lists no namespace (%v)", index, err)
	}
	for _, ns := range answer.Namespaces.Items {
		r.Calls = append(r.Calls, call(ns, "/api/graph/containment?namespace="+url.QueryEscape(ns)))
	}
	if idle > 0 {
		r.Idle.Start = r.Calls[len(r.Calls)-1].Span.End
		r.Idle.End = r.Idle.Start.Add(idle)
		time.Sleep(time.Until(r.Idle.End))
		say("PERF-WINDOW product run=%d start=%s end=%s", index, stamp(r.Idle.Start), stamp(r.Idle.End))
	}
	st, err := sampler.ReadStatus(pid)
	samples, errs := s.Stop()
	p.stop()
	r.Samples, r.HWM = samples, st.HWM
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("run %d VmHWM read: %v", index, err))
	}
	for _, e := range errs {
		problems = append(problems, fmt.Sprintf("run %d sample read: %s", index, e))
	}
	// C3 band of the live run: samples were taken, each is a resident size above zero and
	// none exceeds the VmHWM read after the window, and VmHWM is at least 5,000,000 bytes.
	if len(samples) == 0 {
		problems = append(problems, fmt.Sprintf("run %d: no sample", index))
	}
	for i, v := range samples {
		if v <= 0 || v > st.HWM {
			problems = append(problems, fmt.Sprintf("run %d sample %d = %d bytes outside (0, VmHWM %d]", index, i+1, v, st.HWM))
			break
		}
	}
	if st.HWM < 5_000_000 {
		problems = append(problems, fmt.Sprintf("run %d VmHWM %d bytes below 5000000", index, st.HWM))
	}
	phase(fmt.Sprintf("run-%d", index), t0)
	freeM()
	return r, problems
}

// saveFiles writes the answers of run 1 (cluster.json byte for byte as served, and
// ns-<name>.json of every namespace call) and, at L, packs them as l-graph.tar.gz with a
// deterministic tar and gzip (packet 6.3); it returns the files the judge prints.
func saveFiles(t *testing.T, out, scale string, r perfjudge.Run) []perfjudge.File {
	t.Helper()
	dir := filepath.Join(out, "l-graph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var cluster []byte
	for _, c := range r.Calls {
		name := "ns-" + c.Name + ".json"
		if c.Name == "cluster" {
			name, cluster = "cluster.json", c.Body
		}
		if err := os.WriteFile(filepath.Join(dir, name), c.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []perfjudge.File{{Name: "cluster.json", Data: cluster}}
	if scale != "L" {
		return files
	}
	tarball := filepath.Join(out, "l-graph.tar.gz")
	cmd := exec.Command("sh", "-c", `cd "$1" && tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - cluster.json ns-*.json | gzip -n > "$2"`, "sh", dir, tarball)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PERF verdict=ERROR packing %s: %v %s", tarball, err, b)
	}
	data, err := os.ReadFile(tarball)
	if err != nil {
		t.Fatal(err)
	}
	return append(files, perfjudge.File{Name: "l-graph.tar.gz", Data: data})
}

// harness runs the harness checks of packet 6.4 on the judge's report. Each failing check
// is an error, and every check runs.
func harness(t *testing.T, in perfjudge.Input, rep perfjudge.Report, c3 []string) {
	t.Helper()
	check := func(name string, ok bool, detail string) {
		say("PERF-HARNESS check=%s %s result=%s", name, detail, map[bool]string{true: "PASS", false: "FAIL"}[ok])
		if !ok {
			t.Errorf("PERF-HARNESS check=%s FAIL: %s", name, detail)
		}
	}
	check("judge-exit", rep.Exit != 2, fmt.Sprintf("exit=%d (0 or 1 expected; 2 is ERROR) errors=%q", rep.Exit, rep.Errors))
	check("C1-exact-counts", rep.ScaleOK, "scale line ok")
	check("C3-band", len(c3) == 0, fmt.Sprintf("problems=%q peaks=%v", c3, rep.Peaks))
	for i, cluster := range []bool{true, false} {
		expected := perfjudge.ExpectedSet(cluster, in.Served, true)
		name := "cluster"
		if !cluster {
			name = in.Measured
		}
		var set perfjudge.SetResult
		if i < len(rep.C5) {
			set = rep.C5[i]
		}
		check("C5-set call="+name, set.Pass && set.Matched >= len(expected) && len(set.Missing) == 0 && len(expected) > 0,
			fmt.Sprintf("matched=%d K=%d missing=%q pass=%v", set.Matched, len(expected), set.Missing, set.Pass))
	}
	want := perfjudge.ExpectedPeriodic(in.Noisy.Span.End.Sub(in.Noisy.Span.Start), in.Noisy.Interval)
	d := rep.Noisy.Periodic - want
	check("C6-noisy", !rep.Noisy.Pass() && d >= -1 && d <= 1, fmt.Sprintf("expected periodic=%d+-1 got %s, detector FAIL expected", want, rep.Noisy))
	check("C6-410-positive", rep.Positive == perfjudge.Counts{Relist410: 1}, fmt.Sprintf("expected relist-410=1 unverified=0 periodic=0 got %s", rep.Positive))
	check("C6-410-mixed", rep.Mixed == perfjudge.Counts{Relist410: 1, Unverified: 2, Periodic: 1}, fmt.Sprintf("expected relist-410=1 unverified=2 periodic=1 got %s", rep.Mixed))
}
