package hk2judge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/prdroom/intuitik/internal/graph"
)

// The recorded answers of these tests are Go values written here as the graph API returns
// them for the 3.2 scenarios (C8 for the faults, C9 for the baseline); each test changes
// one value and checks the HK2 lines and the exit value.

func call(v any) Call {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return Call{Status: 200, Body: raw}
}

func uid(s string) *string { return &s }

func node(apiVersion, kind, name, id string) graph.Node {
	return graph.Node{UID: id, APIVersion: apiVersion, Kind: kind, Name: name, Owner: graph.Owner{State: graph.StateNone}}
}

func podNode(name, id, state, text string) graph.Node {
	n := node("v1", "Pod", name, id)
	n.NodeState = &graph.NodeState{State: state, Text: text}
	return n
}

func bridge(kind, from, toGroup, toKind, toName string, toUID *string, state, text string) graph.Bridge {
	return graph.Bridge{Kind: kind, From: graph.From{UID: from}, To: graph.Target{Group: toGroup, Kind: toKind, Name: toName, UID: toUID}, State: state, Settings: []string{}, Text: text}
}

func answer(ns string, nodes []graph.Node, bridges ...graph.Bridge) graph.Answer {
	if bridges == nil {
		bridges = []graph.Bridge{}
	}
	return graph.Answer{Namespace: ns, NamespaceState: graph.NamespaceState{State: graph.NamespaceOK, ExistenceChecked: true},
		Nodes: nodes, Refused: []graph.Refusal{}, Bridges: bridges, Unsupported: []graph.Unsupported{}}
}

// owner is the Deployment dep (uid "d"+n) and its ReplicaSet (uid "r"+n) as the answer
// holds them; ownedPod is a Pod node of that ReplicaSet.
func owner(dep, n string) []graph.Node {
	d := node("apps/v1", "Deployment", dep, "d"+n)
	rs := node("apps/v1", "ReplicaSet", dep+"-1", "r"+n)
	rs.Parent, rs.Owner.State = uid("d"+n), graph.StateContained
	return []graph.Node{d, rs}
}

func ownedPod(name, id, state, text, n string) graph.Node {
	p := podNode(name, id, state, text)
	p.Parent, p.Owner.State = uid("r"+n), graph.StateContained
	return p
}

// nodes is the Pod node first, then the other nodes, then the owner nodes of n.
func nodes(pod graph.Node, dep, n string, other ...graph.Node) []graph.Node {
	return append(append([]graph.Node{pod}, other...), owner(dep, n)...)
}

// faultAnswers are the C8 answers of A-1 to A-6, one per scenario; every scenario Pod node
// sits in a ReplicaSet of the scenario's Deployment.
func faultAnswers() map[string]graph.Answer {
	return map[string]graph.Answer{
		"A-1": answer("hk2-a1", nodes(ownedPod("crash-1", "p1", graph.NodeFailing, "CrashLoopBackOff"+dot+"3 restarts", "1"), "crash", "1")),
		"A-2": answer("hk2-a2", nodes(ownedPod("pull-1", "p2", graph.NodeFailing, "ImagePullBackOff"+dot+missingImage, "2"), "pull", "2")),
		"A-3": answer("hk2-a3", nodes(ownedPod("web-1", "p3", graph.NodeHealthy, "", "3"), "web", "3", node("v1", "Service", "web", "s3")),
			bridge(graph.BridgeServicePod, "s3", "", "Pod", "", nil, graph.BridgeBroken, graph.SelectorNoPodsText("app=web"))),
		"A-4": answer("hk2-a4", nodes(ownedPod("web-1", "p4", graph.NodeHealthy, "", "4"), "web", "4", node("v1", "Service", "web", "s4")),
			bridge(graph.BridgeServicePod, "s4", "", "Pod", "web-1", uid("p4"), graph.BridgeBroken, graph.NamedPortText("http"))),
		"A-5": answer("hk2-a5", nodes(ownedPod("app-1", "p5", graph.NodeFailing, "CreateContainerConfigError", "5"), "app", "5"),
			bridge(graph.BridgePodConfigMap, "p5", "", "ConfigMap", "app-config", nil, graph.BridgeBroken, graph.NotFoundText("ConfigMap", "app-config"))),
		"A-6": answer("hk2-a6", nodes(ownedPod("db-1", "p6", graph.NodeProgressing, "Pending"+dot+"scheduling", "6"), "db", "6", node("v1", "PersistentVolumeClaim", "data", "c6")),
			bridge(graph.BridgePVCPV, "c6", "", "PersistentVolume", "", nil, graph.BridgeBroken, graph.ClassUnboundText("missing-class")),
			bridge(graph.BridgePVCStorageClass, "c6", "storage.k8s.io", "StorageClass", "missing-class", nil, graph.BridgeBroken, graph.ClassUnboundText("missing-class")),
			bridge(graph.BridgePodPVC, "p6", "", "PersistentVolumeClaim", "data", uid("c6"), graph.BridgeConnected, "")),
	}
}

var expectedPods = map[string]Pod{"A-1": {"p1", "crash-1"}, "A-2": {"p2", "pull-1"}, "A-3": {"p3", "web-1"}, "A-4": {"p4", "web-1"}, "A-5": {"p5", "app-1"}, "A-6": {"p6", "db-1"}}

func stage(answers map[string]graph.Answer) []Scenario {
	var out []Scenario
	for _, id := range IDs {
		n := strings.TrimPrefix(id, "A-")
		s := Scenario{ID: id, Namespace: ScenarioNamespaces[id], Call: call(answers[id]), Deployment: Deployment{ScenarioDeployments[id], "d" + n}, Pods: []Pod{expectedPods[id]}}
		if id == "A-1" {
			s.ReadBefore = ContainerRead{PodUID: "p1", Container: CrashContainer, State: "waiting", Reason: "CrashLoopBackOff", Restarts: 3}
			s.ReadAfter = ContainerRead{PodUID: "p1", Container: CrashContainer, State: "running", Restarts: 4}
		}
		out = append(out, s)
	}
	return out
}

// negativeAnswers are the C8 answers with A-3 in its healthy version (C10).
func negativeAnswers() map[string]graph.Answer {
	m := faultAnswers()
	m["A-3"] = answer("hk2-a3", nodes(ownedPod("web-1", "p3", graph.NodeHealthy, "", "3"), "web", "3", node("v1", "Service", "web", "s3")),
		bridge(graph.BridgeServicePod, "s3", "", "Pod", "web-1", uid("p3"), graph.BridgeConnected, ""))
	return m
}

// baselineAnswers are the C9 answers: every namespace the cluster answer lists; the Pod of
// each scenario namespace sits in a ReplicaSet of the scenario's Deployment.
func baselineAnswers() (graph.ClusterAnswer, map[string]graph.Answer) {
	ns := map[string]graph.Answer{
		"hk2-a0": answer("hk2-a0", []graph.Node{node("v1", "PersistentVolumeClaim", "idle", "c0")},
			bridge(graph.BridgePVCPV, "c0", "", "PersistentVolume", "", nil, graph.BridgeWaiting, graph.ConsumerWaitText("idle")),
			bridge(graph.BridgePVCStorageClass, "c0", "storage.k8s.io", "StorageClass", "standard", nil, graph.BridgeConnected, "")),
		"kube-system": answer("kube-system", []graph.Node{node("v1", "Service", "kube-dns", "ks"), podNode("coredns-1", "kp", graph.NodeHealthy, "")},
			bridge(graph.BridgeServicePod, "ks", "", "Pod", "coredns-1", uid("kp"), graph.BridgeConnected, "")),
	}
	for _, id := range IDs {
		n := ScenarioNamespaces[id]
		ns[n] = answer(n, nodes(ownedPod("ok-1", "h"+n, graph.NodeHealthy, "", n), ScenarioDeployments[id], n))
	}
	var items []string
	for n := range ns {
		items = append(items, n)
	}
	cluster := graph.ClusterAnswer{Namespaces: graph.NamespaceList{Listed: true, Items: items, From: "list"}, Refused: []graph.Refusal{}}
	return cluster, ns
}

// baselineExpected is what the baseline stage expects in each scenario namespace.
func baselineExpected() map[string]Expected {
	out := map[string]Expected{}
	for _, id := range IDs {
		n := ScenarioNamespaces[id]
		out[n] = Expected{Deployment: Deployment{ScenarioDeployments[id], "d" + n}, Pods: []Pod{{"h" + n, "ok-1"}}}
	}
	return out
}

func baseline(cluster graph.ClusterAnswer, ns map[string]graph.Answer) Baseline {
	b := Baseline{Cluster: call(cluster), Namespaces: map[string]Call{}, Expected: baselineExpected()}
	for n, a := range ns {
		b.Namespaces[n] = call(a)
	}
	return b
}

func recorded() Input {
	cluster, ns := baselineAnswers()
	return Input{Faults: stage(faultAnswers()), Negative: stage(negativeAnswers()), Baseline: baseline(cluster, ns)}
}

// run judges in and returns the output lines and the exit value.
func run(t *testing.T, in Input) ([]string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Judge(in, &out)
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	for _, l := range lines {
		t.Log(l)
	}
	return lines, code
}

func has(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func hasPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// expect checks that every wanted line is printed exactly, every unwanted prefix is absent
// and the exit value, and prints one FA2-T7b line for the criterion.
func expect(t *testing.T, criterion, input string, lines []string, code, wantCode int, want []string, absent ...string) {
	t.Helper()
	ok := code == wantCode
	var missing []string
	for _, w := range want {
		if !has(lines, w) {
			ok = false
			missing = append(missing, w)
		}
	}
	for _, a := range absent {
		if hasPrefix(lines, a) {
			ok = false
			missing = append(missing, "unexpected "+a)
		}
	}
	t.Logf("FA2-T7b %s input=%s expected=exit %d with %d lines actual=exit %d missing=%q result=%s", criterion, input, wantCode, len(want), code, missing, passFail(ok))
	if !ok {
		t.Errorf("%s %s: exit %d (want %d), missing %q", criterion, input, code, wantCode, missing)
	}
}

// TestJudge_Recorded (C8, C9, C10 on recorded answers): the unchanged answers give 6/6,
// the negative stage 5/6 with A-3 FAIL only, A-0 PASS and verdict PASS.
func TestJudge_Recorded(t *testing.T) {
	lines, code := run(t, recorded())
	expect(t, "recorded", "c8-c9-c10-answers", lines, code, 0, []string{
		`HK2 stage=faults A-1 read=before uid=p1 container=crash state=waiting reason="CrashLoopBackOff" exitCode=- restartCount=3`,
		`HK2 stage=faults A-1 read=after uid=p1 container=crash state=running reason="" exitCode=- restartCount=4`,
		"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=CrashLoopBackOff \\u00b7 3 restarts actual=CrashLoopBackOff \\u00b7 3 restarts result=PASS",
		"HK2 stage=faults A-3 check=service-pod from=Service/web expected=Pod/ broken selector app=web matches 0 Pods actual=Pod/ broken selector app=web matches 0 Pods result=PASS",
		"HK2 stage=faults total=6/6",
		"HK2 stage=negative A-3 result=FAIL",
		"HK2 stage=negative total=5/6 expected=5/6 result=PASS",
		"HK2 stage=baseline A-0 check=idle-waiting expected=PersistentVolume/ waiting idle waits for a consumer Pod actual=PersistentVolume/ waiting idle waits for a consumer Pod result=PASS",
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=1 optional=0 unknown=0 result=PASS",
		"HK2 total=6/6 negative=PASS a0=PASS verdict=PASS",
	})
	if last := lines[len(lines)-1]; last != "HK2 total=6/6 negative=PASS a0=PASS verdict=PASS" {
		t.Errorf("last line %q", last)
	}
}

// TestJudge_NodeValueDecides (C11): with the bridges unchanged, a wrong Pod node value
// fails its scenario; total 5/6, exit 1.
func TestJudge_NodeValueDecides(t *testing.T) {
	cases := []struct {
		input, id string
		change    func(m map[string]graph.Answer)
		line      string
	}{
		{"a1-running-healthy", "A-1", func(m map[string]graph.Answer) {
			m["A-1"].Nodes[0].NodeState = &graph.NodeState{State: graph.NodeHealthy, Text: "Running"}
		}, "HK2 stage=faults A-1 check=node-state pod=crash-1 expected=failing actual=healthy result=FAIL"},
		{"a2-image-one-character", "A-2", func(m map[string]graph.Answer) {
			m["A-2"].Nodes[0].NodeState.Text = "ImagePullBackOff" + dot + "registry.k8s.io/pause:intuitik-a2-missinG"
		}, "HK2 stage=faults A-2 check=node-text pod=pull-1 expected=ImagePullBackOff \\u00b7 registry.k8s.io/pause:intuitik-a2-missing | ErrImagePull \\u00b7 registry.k8s.io/pause:intuitik-a2-missing actual=ImagePullBackOff \\u00b7 registry.k8s.io/pause:intuitik-a2-missinG result=FAIL"},
		{"a5-bridge-right-node-healthy", "A-5", func(m map[string]graph.Answer) {
			m["A-5"].Nodes[0].NodeState = &graph.NodeState{State: graph.NodeHealthy}
		}, "HK2 stage=faults A-5 check=node-state pod=app-1 expected=failing actual=healthy result=FAIL"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			m := faultAnswers()
			c.change(m)
			in := recorded()
			in.Faults = stage(m)
			lines, code := run(t, in)
			expect(t, "C11", c.input, lines, code, 1, []string{
				c.line,
				"HK2 stage=faults " + c.id + " result=FAIL",
				"HK2 stage=faults total=5/6",
				"HK2 total=5/6 negative=PASS a0=PASS verdict=FAIL",
			})
		})
	}
	// A-1's restart count other than the candidate read's, and the singular form for n > 1.
	for _, text := range []string{"CrashLoopBackOff" + dot + "4 restarts", "CrashLoopBackOff" + dot + "5 restarts", "CrashLoopBackOff" + dot + "3 restart", "CrashLoopBackOff" + dot + "03 restarts"} {
		m := faultAnswers()
		m["A-1"].Nodes[0].NodeState.Text = text
		in := recorded()
		in.Faults = stage(m)
		lines, code := run(t, in)
		expect(t, "C11", "a1-text "+printable(text), lines, code, 1, []string{"HK2 stage=faults A-1 result=FAIL", "HK2 stage=faults total=5/6"})
	}
}

// kubectlPod is a Pod object as kubectl returns it, with container crash in state (JSON)
// and lastState (JSON, "" for none).
func kubectlPod(uid, state, lastState string, restarts int) []byte {
	s := `{"name":"crash","ready":false,"restartCount":` + strconv.Itoa(restarts) + `,"state":` + state
	if lastState != "" {
		s += `,"lastState":` + lastState
	}
	return []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"crash-1","uid":"` + uid + `"},"status":{"phase":"Running","containerStatuses":[` + s + `}]}}`)
}

// TestJudge_A1Reads (C11 r5, 6.4): the A-1 text must equal the normal form of the current
// state of a kubectl read of the same Pod UID and container; lastState is never a
// candidate.
func TestJudge_A1Reads(t *testing.T) {
	errorText := "Error" + dot + "4 restarts"
	terminated := func(reason string, exit, rc int64) ContainerRead {
		return ContainerRead{PodUID: "p1", Container: CrashContainer, State: "terminated", Reason: reason, ExitCode: exit, Restarts: rc}
	}
	running := ContainerRead{PodUID: "p1", Container: CrashContainer, State: "running", Restarts: 4}
	cases := []struct {
		input         string
		api           string
		before, after ContainerRead
		pass          bool
		line          string
	}{
		{"both-reads-running-rc-4", errorText, running, running, false,
			"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=a candidate read of uid=p1 container=crash (none) actual=Error \\u00b7 4 restarts result=FAIL"},
		{"laststate-terminated-error-only", errorText,
			ReadContainer(kubectlPod("p1", `{"running":{"startedAt":"2026-11-20T00:00:00Z"}}`, `{"terminated":{"reason":"Error","exitCode":1}}`, 4), CrashContainer),
			ReadContainer(kubectlPod("p1", `{"running":{"startedAt":"2026-11-20T00:00:00Z"}}`, `{"terminated":{"reason":"Error","exitCode":1}}`, 4), CrashContainer), false,
			`HK2 stage=faults A-1 read=before uid=p1 container=crash state=running reason="" exitCode=- restartCount=4`},
		{"terminated-error-of-another-pod-uid", errorText, running,
			ContainerRead{PodUID: "p0", Container: CrashContainer, State: "terminated", Reason: "Error", ExitCode: 1, Restarts: 4}, false,
			"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=a candidate read of uid=p1 container=crash (none) actual=Error \\u00b7 4 restarts result=FAIL"},
		{"terminated-empty-reason-exit-1-rc-4", "exit 1" + dot + "4 restarts", running,
			ReadContainer(kubectlPod("p1", `{"terminated":{"exitCode":1}}`, "", 4), CrashContainer), true,
			"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=exit 1 \\u00b7 4 restarts actual=exit 1 \\u00b7 4 restarts result=PASS"},
		{"terminated-read-at-rc-5", errorText, running, terminated("Error", 1, 5), false,
			"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=Error \\u00b7 5 restarts actual=Error \\u00b7 4 restarts result=FAIL"},
		{"terminated-error-rc-4", errorText, terminated("Error", 1, 4), running, true,
			"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=Error \\u00b7 4 restarts actual=Error \\u00b7 4 restarts result=PASS"},
		{"crashloop-read-other-container", "CrashLoopBackOff" + dot + "4 restarts",
			ContainerRead{PodUID: "p1", Container: "other", State: "waiting", Reason: "CrashLoopBackOff", Restarts: 4}, running, false,
			"HK2 stage=faults A-1 check=node-text pod=crash-1 expected=a candidate read of uid=p1 container=crash (none) actual=CrashLoopBackOff \\u00b7 4 restarts result=FAIL"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			m := faultAnswers()
			m["A-1"].Nodes[0].NodeState.Text = c.api
			in := recorded()
			in.Faults = stage(m)
			in.Faults[0].ReadBefore, in.Faults[0].ReadAfter = c.before, c.after
			lines, code := run(t, in)
			if c.pass {
				expect(t, "C11", "a1-"+c.input, lines, code, 0, []string{c.line, "HK2 stage=faults A-1 result=PASS", "HK2 stage=faults total=6/6"})
				return
			}
			expect(t, "C11", "a1-"+c.input, lines, code, 1, []string{c.line, "HK2 stage=faults A-1 result=FAIL", "HK2 stage=faults total=5/6"})
		})
	}
}

// TestJudge_RefusalAndError (C12): (a) a refused Pods LIST fails A-4 with the refusal
// text; (b) a refused entry fails A-0 with broken 0; (c) a 401 gives ERROR, no total, exit 2.
func TestJudge_RefusalAndError(t *testing.T) {
	refusal := graph.Refusal{Kind: "Pod", Status: 403, Reason: "Forbidden", Text: graph.KindText("Pod", 403)}

	m := faultAnswers()
	a4 := answer("hk2-a4", []graph.Node{node("v1", "Service", "web", "s4")},
		bridge(graph.BridgeServicePod, "s4", "", "Pod", "", nil, graph.BridgeUnknown, graph.NoPermissionText("Pod")))
	a4.Refused = []graph.Refusal{refusal}
	m["A-4"] = a4
	in := recorded()
	in.Faults = stage(m)
	lines, code := run(t, in)
	expect(t, "C12", "a-a4-pods-refused", lines, code, 1, []string{
		"HK2 stage=faults A-4 check=refused expected=none actual=refused " + refusal.Text + " result=FAIL",
		"HK2 stage=faults A-4 result=FAIL",
		"HK2 stage=faults total=5/6",
		"HK2 total=5/6 negative=PASS a0=PASS verdict=FAIL",
	})

	cluster, ns := baselineAnswers()
	ks := ns["kube-system"]
	ks.Refused = []graph.Refusal{refusal}
	ns["kube-system"] = ks
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "C12", "b-a0-refused-broken-0", lines, code, 1, []string{
		"HK2 stage=baseline A-0 check=refused namespace=kube-system expected=none actual=refused " + refusal.Text + " result=FAIL",
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=1 optional=0 unknown=0 result=FAIL",
		"HK2 total=6/6 negative=PASS a0=FAIL verdict=FAIL",
	})

	cluster, ns = baselineAnswers()
	cluster.Refused = []graph.Refusal{{Kind: "Namespace", Status: 403, Reason: "Forbidden", Text: graph.KindText("Namespace", 403)}}
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "C12", "b-cluster-refused", lines, code, 1, []string{"HK2 total=6/6 negative=PASS a0=FAIL verdict=FAIL"})

	for _, c := range []struct {
		input  string
		change func(in *Input)
	}{
		{"c-401-scenario", func(in *Input) { in.Negative[2].Call = Call{Status: 401, Body: []byte(`{"error":"session"}`)} }},
		{"c-401-cluster", func(in *Input) { in.Baseline.Cluster = Call{Status: 401} }},
		{"c-body-not-json", func(in *Input) { in.Faults[0].Call.Body = []byte("<html>") }},
		{"c-stage-missing-scenario", func(in *Input) { in.Faults = in.Faults[:5] }},
	} {
		in := recorded()
		c.change(&in)
		lines, code := run(t, in)
		expect(t, "C12", c.input, lines, code, 2, nil, "HK2 stage=", "HK2 total=")
		if len(lines) != 1 || !strings.HasPrefix(lines[0], "HK2 verdict=ERROR ") {
			t.Errorf("C12 %s: output %q, want one ERROR line", c.input, lines)
		}
	}
}

// TestJudge_A0Counts (C13): one broken bridge in kube-system gives broken=1, A-0 FAIL and
// verdict FAIL; a warning outside the scenario namespaces is printed and not counted; a
// failing Pod or a warning in a scenario namespace fails A-0; a missing idle PVC fails A-0.
func TestJudge_A0Counts(t *testing.T) {
	cluster, ns := baselineAnswers()
	ns["kube-system"].Bridges[0].State = graph.BridgeBroken
	ns["kube-system"].Bridges[0].Text = graph.NamedPortText("dns")
	in := recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code := run(t, in)
	expect(t, "C13", "kube-system-one-broken", lines, code, 1, []string{
		"HK2 stage=baseline A-0 item=broken namespace=kube-system bridge=service-pod from=Service/kube-dns to=Pod/coredns-1 text=" + graph.NamedPortText("dns"),
		"HK2 stage=baseline A-0 broken=1 warning=0 failing=0 waiting=1 optional=0 unknown=0 result=FAIL",
		"HK2 total=6/6 negative=PASS a0=FAIL verdict=FAIL",
	})

	cluster, ns = baselineAnswers()
	ns["kube-system"].Bridges[0].State = graph.BridgeWarning
	ns["kube-system"].Bridges[0].Text = graph.NumericPortText(53)
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "C13", "kube-system-warning-not-counted", lines, code, 0, []string{
		"HK2 stage=baseline A-0 item=warning namespace=kube-system bridge=service-pod from=Service/kube-dns to=Pod/coredns-1 text=" + graph.NumericPortText(53),
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=1 optional=0 unknown=0 result=PASS",
	})

	cluster, ns = baselineAnswers()
	ns["hk2-a1"].Nodes[0].NodeState = &graph.NodeState{State: graph.NodeFailing, Text: "CrashLoopBackOff" + dot + "1 restart"}
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "C13", "scenario-pod-failing", lines, code, 1, []string{
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=1 waiting=1 optional=0 unknown=0 result=FAIL",
	})

	cluster, ns = baselineAnswers()
	a3 := ns["hk2-a3"]
	a3.Bridges = []graph.Bridge{bridge(graph.BridgeServicePod, "x", "", "Pod", "ok-1", uid("hhk2-a3"), graph.BridgeWarning, graph.NumericPortText(80))}
	ns["hk2-a3"] = a3
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "C13", "scenario-warning", lines, code, 1, []string{
		"HK2 stage=baseline A-0 broken=0 warning=1 failing=0 waiting=1 optional=0 unknown=0 result=FAIL",
	})

	cluster, ns = baselineAnswers()
	ns["hk2-a0"] = answer("hk2-a0", []graph.Node{})
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "C13", "idle-absent", lines, code, 1, []string{
		"HK2 stage=baseline A-0 check=idle-waiting expected=PersistentVolume/ waiting idle waits for a consumer Pod actual=no PersistentVolumeClaim idle result=FAIL",
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=0 optional=0 unknown=0 result=FAIL",
	})

	cluster, ns = baselineAnswers()
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	delete(in.Baseline.Namespaces, "kube-system")
	lines, code = run(t, in)
	expect(t, "C13", "namespace-answer-missing", lines, code, 1, []string{
		"HK2 stage=baseline A-0 check=answer namespace=kube-system expected=answer actual=missing result=FAIL",
	})
}

// TestJudge_StalePodAndNegative (3.2 stale-pod rule, C10 failure side): a Pod node the
// stage does not expect fails its scenario; a negative stage other than 5/6 with A-3 FAIL
// fails the verdict.
func TestJudge_StalePodAndNegative(t *testing.T) {
	m := faultAnswers()
	a4 := m["A-4"]
	a4.Nodes = append(a4.Nodes, podNode("web-0", "old", graph.NodeHealthy, ""))
	m["A-4"] = a4
	in := recorded()
	in.Faults = stage(m)
	lines, code := run(t, in)
	expect(t, "C10", "stale-pod", lines, code, 1, []string{
		"HK2 stage=faults A-4 check=pods expected=web-1 actual=stale pod web-0 result=FAIL",
		"HK2 stage=faults A-4 result=FAIL",
	})

	in = recorded()
	in.Negative = stage(faultAnswers())
	lines, code = run(t, in)
	expect(t, "C10", "negative-6-of-6", lines, code, 1, []string{
		"HK2 stage=negative total=6/6 expected=5/6 result=FAIL",
		"HK2 total=6/6 negative=FAIL a0=PASS verdict=FAIL",
	})

	m = negativeAnswers()
	m["A-6"].Bridges[2].State = graph.BridgeBroken
	in = recorded()
	in.Negative = stage(m)
	lines, code = run(t, in)
	expect(t, "C10", "negative-a6-also-fails", lines, code, 1, []string{
		"HK2 stage=negative A-6 result=FAIL",
		"HK2 stage=negative total=4/6 expected=5/6 result=FAIL",
	})
}

// kubectlList is a kubectl list of the given objects (JSON).
func kubectlList(items ...string) []byte {
	return []byte(`{"apiVersion":"v1","kind":"List","items":[` + strings.Join(items, ",") + `]}`)
}

// kubectlObject is one object of a kubectl list; owner is its ownerReferences (JSON) or "".
func kubectlObject(kind, name, uid, owner string, terminating bool) string {
	meta := `"name":"` + name + `","namespace":"hk2-a4","uid":"` + uid + `"`
	if terminating {
		meta += `,"deletionTimestamp":"2026-11-20T00:00:00Z"`
	}
	if owner != "" {
		meta += `,"ownerReferences":` + owner
	}
	return `{"kind":"` + kind + `","metadata":{` + meta + `}}`
}

func controllerRef(kind, name, uid string, controller bool) string {
	return `[{"apiVersion":"apps/v1","kind":"` + kind + `","name":"` + name + `","uid":"` + uid + `","controller":` + strconv.FormatBool(controller) + `}]`
}

// f1Lists are the kubectl lists of namespace hk2-a4 for F1: Deployment web (current uid
// dep-web), ReplicaSet web-new of it, ReplicaSet web-old of an earlier Deployment web
// (uid dep-web-old), ReplicaSet other-1 of Deployment other, and Pods of each, a
// terminating Pod, a Pod without owner and a Pod whose ReplicaSet reference is not the
// controller.
func f1Lists() (deployments, replicaSets, pods []byte) {
	deployments = kubectlList(
		kubectlObject("Deployment", "web", "dep-web", "", false),
		kubectlObject("Deployment", "other", "dep-other", "", false))
	replicaSets = kubectlList(
		kubectlObject("ReplicaSet", "web-new", "rs-new", controllerRef("Deployment", "web", "dep-web", true), false),
		kubectlObject("ReplicaSet", "web-old", "rs-old", controllerRef("Deployment", "web", "dep-web-old", true), false),
		kubectlObject("ReplicaSet", "other-1", "rs-other", controllerRef("Deployment", "other", "dep-other", true), false))
	pods = kubectlList(
		kubectlObject("Pod", "web-new-a", "pod-new", controllerRef("ReplicaSet", "web-new", "rs-new", true), false),
		kubectlObject("Pod", "web-new-b", "pod-new-b", controllerRef("ReplicaSet", "web-new", "rs-new", true), true),
		kubectlObject("Pod", "web-old-a", "pod-old", controllerRef("ReplicaSet", "web-old", "rs-old", true), false),
		kubectlObject("Pod", "other-1-a", "pod-other", controllerRef("ReplicaSet", "other-1", "rs-other", true), false),
		kubectlObject("Pod", "bare", "pod-bare", "", false),
		kubectlObject("Pod", "adopted", "pod-adopted", controllerRef("ReplicaSet", "web-new", "rs-new", false), false))
	return
}

// TestSelectPods (F1 of review 5987053962, 3.2, 6.4): only the Pods of a ReplicaSet of the
// named Deployment's current UID are expected; a Pod of an earlier Deployment of the same
// name, of another Deployment, without controller owner or terminating is left out.
func TestSelectPods(t *testing.T) {
	d, r, p := f1Lists()
	dep, pods, excluded, err := SelectPods("web", d, r, p)
	var names []string
	for _, x := range pods {
		names = append(names, x.Name+"="+x.UID)
	}
	actual := fmt.Sprintf("deployment=%s/%s pods=%s err=%v", dep.Name, dep.UID, strings.Join(names, ","), err)
	expected := "deployment=web/dep-web pods=web-new-a=pod-new err=<nil>"
	t.Logf("FA2-T7b F1 select input=hk2-a4-six-pods expected=%s actual=%s result=%s", expected, actual, passFail(actual == expected))
	if actual != expected {
		t.Errorf("F1 select: got %s, want %s", actual, expected)
	}
	want := strings.Join([]string{
		"pod web-new-b: terminating",
		"pod web-old-a: controller ReplicaSet/web-old uid=rs-old is not a ReplicaSet of Deployment web uid=dep-web",
		"pod other-1-a: controller ReplicaSet/other-1 uid=rs-other is not a ReplicaSet of Deployment web uid=dep-web",
		"pod bare: no controller owner",
		"pod adopted: no controller owner",
	}, "; ")
	got := strings.Join(excluded, "; ")
	t.Logf("FA2-T7b F1 excluded expected=%s actual=%s result=%s", want, got, passFail(got == want))
	if got != want {
		t.Errorf("F1 excluded: got %q, want %q", got, want)
	}
	for _, c := range []struct {
		input, name string
		d           []byte
	}{
		{"deployment-absent", "pull", d},
		{"deployment-list-not-json", "web", []byte("<html>")},
	} {
		_, pods, _, err := SelectPods(c.name, c.d, r, p)
		t.Logf("FA2-T7b F1 select input=%s expected=error actual=pods=%d err=%v result=%s", c.input, len(pods), err, passFail(err != nil && len(pods) == 0))
		if err == nil || len(pods) != 0 {
			t.Errorf("F1 select %s: pods %d err %v, want an error and no Pod", c.input, len(pods), err)
		}
	}
}

func ownedPodOf(name, id, rs string) graph.Node {
	p := podNode(name, id, graph.NodeHealthy, "")
	p.Parent, p.Owner.State = uid(rs), graph.StateContained
	return p
}

func replicaSet(name, id, parent string) graph.Node {
	rs := node("apps/v1", "ReplicaSet", name, id)
	if parent != "" {
		rs.Parent, rs.Owner.State = uid(parent), graph.StateContained
	}
	return rs
}

// TestJudge_CollectedStalePod (F1): a Pod of the wrong owner that a collection sees is not
// expected, so when the API answer holds it the scenario fails with stale pod, also when
// such a Pod is the only Pod of the namespace.
func TestJudge_CollectedStalePod(t *testing.T) {
	d, r, p := f1Lists()
	dep, pods, _, err := SelectPods("web", d, r, p)
	if err != nil {
		t.Fatal(err)
	}
	a4 := answer("hk2-a4", []graph.Node{
		ownedPodOf("web-new-a", "pod-new", "rs-new"), ownedPodOf("web-old-a", "pod-old", "rs-old"),
		node("v1", "Service", "web", "s4"), node("apps/v1", "Deployment", "web", "dep-web"),
		replicaSet("web-new", "rs-new", "dep-web"), replicaSet("web-old", "rs-old", "")},
		bridge(graph.BridgeServicePod, "s4", "", "Pod", "web-new-a", uid("pod-new"), graph.BridgeBroken, graph.NamedPortText("http")))
	in := recorded()
	in.Faults[3].Call, in.Faults[3].Deployment, in.Faults[3].Pods = call(a4), dep, pods
	lines, code := run(t, in)
	expect(t, "F1", "old-owner-pod-in-answer", lines, code, 1, []string{
		"HK2 stage=faults A-4 check=pods expected=web-new-a actual=stale pod web-old-a result=FAIL",
		"HK2 stage=faults A-4 check=owner pod=web-new-a expected=ReplicaSet/<any> -> Deployment/web uid=dep-web actual=ReplicaSet/web-new -> Deployment/web uid=dep-web result=PASS",
		"HK2 stage=faults A-4 result=FAIL",
		"HK2 stage=faults total=5/6",
	})

	// The namespace holds only a Pod of Deployment other: nothing is expected.
	onlyOther := kubectlList(kubectlObject("Pod", "other-1-a", "pod-other", controllerRef("ReplicaSet", "other-1", "rs-other", true), false))
	dep, pods, _, err = SelectPods("web", d, r, onlyOther)
	if err != nil {
		t.Fatal(err)
	}
	a4 = answer("hk2-a4", []graph.Node{ownedPodOf("other-1-a", "pod-other", "rs-other"), node("v1", "Service", "web", "s4"),
		node("apps/v1", "Deployment", "other", "dep-other"), replicaSet("other-1", "rs-other", "dep-other")},
		bridge(graph.BridgeServicePod, "s4", "", "Pod", "other-1-a", uid("pod-other"), graph.BridgeBroken, graph.NamedPortText("http")))
	in = recorded()
	in.Faults[3].Call, in.Faults[3].Deployment, in.Faults[3].Pods = call(a4), dep, pods
	lines, code = run(t, in)
	expect(t, "F1", "only-wrong-owner-pod", lines, code, 1, []string{
		"HK2 stage=faults A-4 check=pods expected=at least one pod actual=stale pod other-1-a result=FAIL",
		"HK2 stage=faults A-4 result=FAIL",
	})
}

// TestJudge_OwnerChain (F1, 3.2): an expected Pod node must sit in a ReplicaSet of the
// stage's Deployment, by name and UID, in the API answer.
func TestJudge_OwnerChain(t *testing.T) {
	lines, code := run(t, recorded())
	expect(t, "F1", "owner-chain-recorded", lines, code, 0, []string{
		"HK2 stage=faults A-2 check=owner pod=pull-1 expected=ReplicaSet/<any> -> Deployment/pull uid=d2 actual=ReplicaSet/pull-1 -> Deployment/pull uid=d2 result=PASS",
		"HK2 stage=negative A-3 check=owner pod=web-1 expected=ReplicaSet/<any> -> Deployment/web uid=d3 actual=ReplicaSet/web-1 -> Deployment/web uid=d3 result=PASS",
	})
	cases := []struct {
		input  string
		change func(in *Input, m map[string]graph.Answer)
		line   string
	}{
		{"deployment-uid-of-earlier-stage", func(in *Input, m map[string]graph.Answer) { in.Faults[1].Deployment.UID = "d2-old" },
			"HK2 stage=faults A-2 check=owner pod=pull-1 expected=ReplicaSet/<any> -> Deployment/pull uid=d2-old actual=ReplicaSet/pull-1 -> Deployment/pull uid=d2 result=FAIL"},
		{"pod-without-parent", func(in *Input, m map[string]graph.Answer) {
			m["A-2"].Nodes[0].Parent = nil
			in.Faults[1].Call = call(m["A-2"])
		}, "HK2 stage=faults A-2 check=owner pod=pull-1 expected=ReplicaSet/<any> -> Deployment/pull uid=d2 actual=no parent result=FAIL"},
		{"replicaset-of-another-deployment", func(in *Input, m map[string]graph.Answer) {
			m["A-2"].Nodes[2].Parent = uid("dx")
			a := m["A-2"]
			a.Nodes = append(a.Nodes, node("apps/v1", "Deployment", "pull", "dx"))
			in.Faults[1].Call = call(a)
		}, "HK2 stage=faults A-2 check=owner pod=pull-1 expected=ReplicaSet/<any> -> Deployment/pull uid=d2 actual=ReplicaSet/pull-1 -> Deployment/pull uid=dx result=FAIL"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			m := faultAnswers()
			in := recorded()
			c.change(&in, m)
			lines, code := run(t, in)
			expect(t, "F1", c.input, lines, code, 1, []string{c.line, "HK2 stage=faults A-2 result=FAIL", "HK2 stage=faults total=5/6"})
		})
	}
}

// TestJudge_BaselinePods (F1, 3.2, 3.3): the baseline checks the expected Pods and owner of
// every scenario namespace; a Pod of an earlier stage or a namespace without expected Pods
// fails A-0.
func TestJudge_BaselinePods(t *testing.T) {
	lines, code := run(t, recorded())
	expect(t, "F1", "baseline-recorded", lines, code, 0, []string{
		"HK2 stage=baseline A-0 check=pods namespace=hk2-a1 expected=ok-1 actual=ok-1 result=PASS",
		"HK2 stage=baseline A-0 check=owner pod=ok-1 namespace=hk2-a6 expected=ReplicaSet/<any> -> Deployment/db uid=dhk2-a6 actual=ReplicaSet/db-1 -> Deployment/db uid=dhk2-a6 result=PASS",
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=1 optional=0 unknown=0 result=PASS",
	})

	cluster, ns := baselineAnswers()
	a3 := ns["hk2-a3"]
	a3.Nodes = append(a3.Nodes, ownedPodOf("web-old", "pod-old", "rs-old"))
	ns["hk2-a3"] = a3
	in := recorded()
	in.Baseline = baseline(cluster, ns)
	lines, code = run(t, in)
	expect(t, "F1", "baseline-earlier-stage-pod", lines, code, 1, []string{
		"HK2 stage=baseline A-0 check=pods namespace=hk2-a3 expected=ok-1 actual=stale pod web-old result=FAIL",
		"HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=1 optional=0 unknown=0 result=FAIL",
		"HK2 total=6/6 negative=PASS a0=FAIL verdict=FAIL",
	})

	cluster, ns = baselineAnswers()
	in = recorded()
	in.Baseline = baseline(cluster, ns)
	delete(in.Baseline.Expected, "hk2-a5")
	lines, code = run(t, in)
	expect(t, "F1", "baseline-no-expected-pods", lines, code, 1, []string{
		"HK2 stage=baseline A-0 check=pods namespace=hk2-a5 expected=expected pods and an answer actual=expected=false answer=true result=FAIL",
		"HK2 total=6/6 negative=PASS a0=FAIL verdict=FAIL",
	})
}
