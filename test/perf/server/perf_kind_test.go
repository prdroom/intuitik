//go:build kind

package server

import (
	"bytes"
	"context"
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
// window, the noisy window and its interval, the readiness limit, and whether the 410
// reproduction cluster follows the measurement. The job runs S on pull_request, and L and
// then XL as two invocations on workflow_dispatch, so the reproduction cluster follows S
// and XL: on dispatch it comes after the 5 L runs, their windows and XL (packet r5.1 6.2).
type size struct {
	runs         int
	idle         time.Duration
	noisy, every time.Duration
	readiness    time.Duration
	reproduction bool
}

var sizes = map[string]size{
	"S":  {runs: 1, idle: 60 * time.Second, noisy: 60 * time.Second, every: 6 * time.Second, readiness: 5 * time.Minute, reproduction: true},
	"L":  {runs: perfjudge.N5Runs, idle: 10 * time.Minute, noisy: 10 * time.Minute, every: 60 * time.Second, readiness: 15 * time.Minute},
	"XL": {runs: 1, readiness: 40 * time.Minute, reproduction: true},
}

// The applied options each cluster must show (packet r5.1 6.2). The measurement cluster
// keeps the kube-apiserver defaults of etcd compaction and the watch cache.
var (
	measurementOptions = options{
		apiserver:         []string{"--audit-policy-file=/etc/kubernetes/intuitik-audit/audit-policy.yaml", "--audit-log-path=" + auditLog, "--audit-log-mode=blocking"},
		controllerManager: []string{"--kube-api-qps=200", "--kube-api-burst=400"},
		absent:            []string{"--etcd-compaction-interval", "--watch-cache"},
	}
	reproductionOptions = options{
		apiserver: []string{"--audit-policy-file=/etc/kubernetes/intuitik-audit/audit-policy.yaml", "--audit-log-path=" + auditLog, "--audit-log-mode=blocking",
			"--etcd-compaction-interval=30s", "--watch-cache-sizes=cronjobs.batch#0"},
	}
)

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
// L and then XL on workflow_dispatch) in the measurement cluster, and then, at S and XL,
// C6 windows 3a and 3b in the 410 reproduction cluster. Preparation failures stop the test
// (PERF verdict=ERROR); after a cluster is ready every harness check is an error that lets
// the others run (packet 6.4).
func TestPerfServer(t *testing.T) {
	scale := env(t, "INTUITIK_PERF_SCALE")
	sz, ok := sizes[scale]
	plan, err := gen.For(scale)
	if !ok || err != nil {
		fatalf(t, "PERF verdict=ERROR scale %q: %v", scale, err)
	}
	work, out := env(t, "INTUITIK_PERF_WORK"), env(t, "INTUITIK_PERF_OUT")
	say("PERF-START scale=%s runs=%d idle=%s noisy=%s every=%s readiness-limit=%s reproduction=%v", scale, sz.runs, sz.idle, sz.noisy, sz.every, sz.readiness, sz.reproduction)
	for _, l := range plan.Table() {
		say("%s", l)
	}

	measurement(t, scale, sz, plan, work, out)
	if sz.reproduction {
		reproduction(t, work)
	}
}

// measurement is the part of the test in the measurement cluster intuitik-perf: build,
// readiness, the runs, the noisy window, the judge and the harness checks. It deletes the
// cluster when it is done, so the reproduction cluster never shares the runner with it.
func measurement(t *testing.T, scale string, sz size, plan gen.Plan, work, out string) {
	t.Helper()
	c := createCluster(t, work, clusterName, "kind-config.yaml", measurementOptions)
	panelToken, controlToken := c.rbac(t)
	stopKwok := startKwok(t, c, work)
	defer c.delete()
	defer stopKwok()
	build(t, c, plan)
	if !ready(t, c, plan, sz.readiness) {
		if scale == "XL" {
			say("PERF scale=XL not-measured (readiness limit %s reached; information only, P-T12-4)", sz.readiness)
			return
		}
		fatalf(t, "PERF scale=%s not-measured verdict=ERROR: readiness not reached in %s (named stop, packet 8)", scale, sz.readiness)
	}
	readback, info := counts(t, c)
	for _, l := range info {
		say("%s", l)
	}
	k := &control{c: c, client: c.tokenClient(controlToken)}
	served, discoveryIDs := discover(t, c, k)
	auditReady(t, c, discoveryIDs)

	in := perfjudge.Input{Cluster: c.name, Scale: scale, Fixture: plan.Counts(), Readback: readback, Served: served,
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
	if sz.noisy > 0 {
		t0 := time.Now()
		in.Noisy = k.noisyWindow(t, sz.noisy, sz.every)
		phase("noisy-window", t0)
	}
	in.Audit = c.readAudit(t)
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

// reproduction is the part of the test in the 410 reproduction cluster intuitik-perf-410
// (packet r5.1 6.2, 6.3): its own RBAC and tokens, namespaces perf-001 and perf-002 and no
// workload; discovery and audit readiness; the setup probe; windows 3a and 3b on batch/v1
// cronjobs; the record of row a's watches; the judge and the C6 410 harness checks.
func reproduction(t *testing.T, work string) {
	t.Helper()
	c := createCluster(t, work, clusterName410, "kind-config-410.yaml", reproductionOptions)
	defer c.delete()
	_, controlToken := c.rbac(t)
	for _, ns := range []string{"perf-001", "perf-002"} {
		if err := c.create("/api/v1/namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}); err != nil {
			fatalf(t, "PERF verdict=ERROR preparation: %v", err)
		}
	}
	k := &control{c: c, client: c.tokenClient(controlToken)}
	_, discoveryIDs := discover(t, c, k)
	auditReady(t, c, discoveryIDs)
	in := perfjudge.ReproInput{Cluster: c.name, PanelUser: panelUser, ControlUser: controlUser}
	t0 := time.Now()
	k.compacted(t)
	in.Positive = k.window410(t, "410-positive", false)
	in.Mixed = k.window410(t, "410-mixed", true)
	phase("410-windows", t0)
	in.Audit = c.readAudit(t)
	in.Records = k.records
	k.rowAEvidence(t, in.Audit)

	t0 = time.Now()
	rep := perfjudge.JudgeRepro(in)
	for _, l := range rep.Lines {
		say("%s", l)
	}
	phase("judge", t0)
	say("PERF judge exit=%d", rep.Exit)
	check := checker(t)
	check("judge-exit", rep.Exit != 2, fmt.Sprintf("exit=%d (0 or 1 expected; 2 is ERROR) errors=%q", rep.Exit, rep.Errors))
	check("C6-410-positive", rep.Positive == perfjudge.Counts{Relist410: 1}, fmt.Sprintf("expected relist-410=1 unverified=0 periodic=0 got %s", rep.Positive))
	check("C6-410-mixed", rep.Mixed == perfjudge.Counts{Relist410: 1, Unverified: 2, Periodic: 1}, fmt.Sprintf("expected relist-410=1 unverified=2 periodic=1 got %s", rep.Mixed))
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
				fatalf(t, "PERF verdict=ERROR preparation: no default ServiceAccount in %s after 2 minutes", ns.Name)
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
			freeM(c.name, "readiness")
			return true
		}
		if time.Since(t0) > limit {
			phase("readiness-limit", t0)
			freeM(c.name, "readiness-limit")
			return false
		}
		time.Sleep(10 * time.Second)
	}
}

// freeM prints the runner's memory at a named point (packet 7, 8: readiness and memory are
// named stops; r5.1 6.2: before and after a cluster is deleted).
func freeM(cluster, when string) {
	out, err := exec.Command("free", "-m").Output()
	say("PERF-MEMORY cluster=%s %s free -m (%v):\n%s", cluster, when, err, strings.TrimRight(string(out), "\n"))
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
// prints each with group, version, resource and scope. It also returns the Audit-Id of
// each discovery request.
func discover(t *testing.T, c *cluster, k *control) ([]perfjudge.Resource, []string) {
	t.Helper()
	var ids []string
	get := func(path string, v any) {
		status, b, id, err := c.do(t.Context(), k.client, "GET", path, nil, "application/json")
		if err != nil || status != 200 || json.Unmarshal(b, v) != nil {
			fatalf(t, "PERF verdict=ERROR discovery %s: status %d %v", path, status, err)
		}
		k.record(perfjudge.Record{Cluster: c.name, AuditID: id, Verb: "get", Path: path})
		ids = append(ids, id)
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
	return out, ids
}

// auditReady is the audit readiness check (DECISION delegation T AUDIT-READY-T12): within
// 10 s of the end of the discovery reads, on one deadline, the audit log must hold a
// well-formed JSON line of the control user whose auditID is the Audit-Id of one of those
// requests, at stage ResponseComplete with a 2xx code. Every read of the log gets the
// remaining time as its limit. A timeout or any read failure is the packet 8 named stop
// "audit stage lines missing", never a pass. It reads the log with its own JSON decoding,
// not with the judge; the judge's audit checks stay as they are.
func auditReady(t *testing.T, c *cluster, ids []string) {
	t.Helper()
	t0 := time.Now()
	deadline := t0.Add(10 * time.Second)
	want := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			want[id] = true
		}
	}
	state := fmt.Sprintf("%d discovery Audit-Ids", len(want))
	for len(want) > 0 {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), left)
		out, err := exec.CommandContext(ctx, "docker", "exec", c.node, "cat", auditLog).Output()
		cancel()
		if err != nil {
			state = fmt.Sprintf("audit log read failed: %v", err)
			break
		}
		n := 0
		for _, raw := range bytes.Split(out, []byte("\n")) {
			var l struct {
				AuditID string `json:"auditID"`
				Stage   string `json:"stage"`
				User    struct {
					Username string `json:"username"`
				} `json:"user"`
				ResponseStatus *struct {
					Code int `json:"code"`
				} `json:"responseStatus"`
			}
			if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &l) != nil {
				continue
			}
			n++
			if l.User.Username != controlUser || !want[l.AuditID] || l.Stage != "ResponseComplete" {
				continue
			}
			if l.ResponseStatus == nil || l.ResponseStatus.Code < 200 || l.ResponseStatus.Code > 299 {
				continue
			}
			say("PERF-AUDIT ready user=%s auditID=%s stage=%s elapsed_ms=%d", l.User.Username, l.AuditID, l.Stage, time.Since(t0).Milliseconds())
			return
		}
		state = fmt.Sprintf("%d well-formed lines, none of the control user bound to a discovery Audit-Id at ResponseComplete with a 2xx code", n)
		time.Sleep(200 * time.Millisecond)
	}
	fatalf(t, "PERF verdict=ERROR STOP-4 named stop (packet 8): audit stage lines missing: after %d ms, last state: %s", time.Since(t0).Milliseconds(), state)
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
		fatalf(t, "PERF verdict=ERROR run %d: %v", index, err)
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
		errorf(t, "PERF-HARNESS run %d: the cluster answer lists no namespace (%v)", index, err)
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
	// C3 band of the live run, judged by the same function as TestRSSBand.
	problems := perfjudge.RSSBand(index, samples, st.HWM, err, errs)
	phase(fmt.Sprintf("run-%d", index), t0)
	freeM(clusterName, fmt.Sprintf("run-%d", index))
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
		fatalf(t, "PERF verdict=ERROR packing %s: %v %s", tarball, err, b)
	}
	data, err := os.ReadFile(tarball)
	if err != nil {
		t.Fatal(err)
	}
	return append(files, perfjudge.File{Name: "l-graph.tar.gz", Data: data})
}

// checker returns the harness check of packet 6.4: it prints the check and its result, and
// a failing check is an error that lets every other check run.
func checker(t *testing.T) func(name string, ok bool, detail string) {
	return func(name string, ok bool, detail string) {
		t.Helper()
		say("PERF-HARNESS check=%s %s result=%s", name, detail, map[bool]string{true: "PASS", false: "FAIL"}[ok])
		if !ok {
			errorf(t, "PERF-HARNESS check=%s FAIL: %s", name, detail)
		}
	}
}

// harness runs the harness checks of packet 6.4 on the measurement judge's report.
func harness(t *testing.T, in perfjudge.Input, rep perfjudge.Report, c3 []string) {
	t.Helper()
	check := checker(t)
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
}
