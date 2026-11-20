// Package hk2judge computes the H-K2 (1) verdict of Appendix A from the graph API answers
// of three stages (packet FA2-T7b 3.3, 3.4, 6.4): the six faults, the negative stage with
// A-3 in its healthy version, and the healthy baseline A-0. It is a pure function of its
// input: no network, no clock, no environment. The kind test of job appendix-a records the
// answers and calls Judge once.
package hk2judge

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/prdroom/intuitik/internal/graph"
)

// Call is one graph API response as received: its HTTP status and body.
type Call struct {
	Status int
	Body   []byte
}

// Pod is one Pod a stage expects in a scenario namespace, as read with kubectl.
type Pod struct {
	UID, Name string
}

// Scenario is one of A-1 to A-6 in one stage: the containment answer of its namespace and
// the Pods the test read in that stage. ReadBefore and ReadAfter are A-1's crashing
// container read with kubectl just before and just after the call (6.4 r5).
type Scenario struct {
	ID         string
	Namespace  string
	Call       Call
	Pods       []Pod
	ReadBefore ContainerRead
	ReadAfter  ContainerRead
}

// ContainerRead is one kubectl read of a container (6.4 r5): the Pod UID, the container
// name, its current state ("waiting", "terminated", "running", or "" when the read found
// no such container or no state), the reason of that state, the exitCode when terminated
// and restartCount. lastState is not part of a read.
type ContainerRead struct {
	PodUID    string
	Container string
	State     string
	Reason    string
	ExitCode  int64
	Restarts  int64
}

// ReadContainer is the read of container name in one Pod object as kubectl returns it
// (JSON). It decodes metadata.uid and, of that container's status, the current state and
// restartCount only; a Pod that does not decode or has no such container gives a read
// with State "".
func ReadContainer(pod []byte, name string) ContainerRead {
	var v struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
		Status struct {
			ContainerStatuses []struct {
				Name         string `json:"name"`
				RestartCount int64  `json:"restartCount"`
				State        struct {
					Waiting *struct {
						Reason string `json:"reason"`
					} `json:"waiting"`
					Running *struct {
					} `json:"running"`
					Terminated *struct {
						Reason   string `json:"reason"`
						ExitCode int64  `json:"exitCode"`
					} `json:"terminated"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	}
	r := ContainerRead{Container: name}
	if json.Unmarshal(pod, &v) != nil {
		return r
	}
	r.PodUID = v.Metadata.UID
	for _, c := range v.Status.ContainerStatuses {
		if c.Name != name {
			continue
		}
		r.Restarts = c.RestartCount
		switch {
		case c.State.Waiting != nil:
			r.State, r.Reason = "waiting", c.State.Waiting.Reason
		case c.State.Terminated != nil:
			r.State, r.Reason, r.ExitCode = "terminated", c.State.Terminated.Reason, c.State.Terminated.ExitCode
		case c.State.Running != nil:
			r.State = "running"
		}
		break
	}
	return r
}

// Candidate is the A-1 text the read's current state gives (3.2 r5), and whether the read
// is a candidate at all: waiting CrashLoopBackOff or terminated, restartCount >= 1.
func (r ContainerRead) Candidate() (string, bool) {
	if r.Restarts < 1 {
		return "", false
	}
	switch {
	case r.State == "waiting" && r.Reason == "CrashLoopBackOff":
		return "CrashLoopBackOff" + dot + restartsText(r.Restarts), true
	case r.State == "terminated" && r.Reason != "":
		return r.Reason + dot + restartsText(r.Restarts), true
	case r.State == "terminated":
		return "exit " + strconv.FormatInt(r.ExitCode, 10) + dot + restartsText(r.Restarts), true
	}
	return "", false
}

// String is the read as printed: never as the API's state or text.
func (r ContainerRead) String() string {
	exit := "-"
	if r.State == "terminated" {
		exit = strconv.FormatInt(r.ExitCode, 10)
	}
	state := r.State
	if state == "" {
		state = "none"
	}
	return fmt.Sprintf("uid=%s container=%s state=%s reason=%q exitCode=%s restartCount=%d", r.PodUID, r.Container, state, r.Reason, exit, r.Restarts)
}

func restartsText(n int64) string {
	if n == 1 {
		return "1 restart"
	}
	return strconv.FormatInt(n, 10) + " restarts"
}

// Baseline is stage 3 (A-0): the cluster answer and one containment answer per namespace,
// keyed by namespace name.
type Baseline struct {
	Cluster    Call
	Namespaces map[string]Call
}

// Input is the three labelled stages of one judge call.
type Input struct {
	Faults   []Scenario
	Negative []Scenario
	Baseline Baseline
}

// The stage names of the output lines.
const (
	StageFaults   = "faults"
	StageNegative = "negative"
	StageBaseline = "baseline"
)

// IDs are the six scenarios in order; ScenarioNamespaces are their namespaces.
var (
	IDs                = []string{"A-1", "A-2", "A-3", "A-4", "A-5", "A-6"}
	ScenarioNamespaces = map[string]string{"A-1": "hk2-a1", "A-2": "hk2-a2", "A-3": "hk2-a3", "A-4": "hk2-a4", "A-5": "hk2-a5", "A-6": "hk2-a6"}
)

// CrashContainer is the container of A-1's Deployment crash (3.2, 6.5).
const CrashContainer = "crash"

// The scenario values of 3.2.
const (
	missingImage = "registry.k8s.io/pause:intuitik-a2-missing"
	idleClaim    = "idle"
	baselineNS   = "hk2-a0"
)

// dot is U+00B7 with one space on each side (3.1).
var dot = " " + string(rune(0x00b7)) + " "

// Judge prints the HK2 lines of 3.4 for in to w and returns the exit value: 0 for verdict
// PASS, 1 for verdict FAIL, 2 when an answer cannot be judged (a status other than 200 or
// a body that does not decode), in which case only "HK2 verdict=ERROR <reason>" is
// printed.
func Judge(in Input, w io.Writer) int {
	faults, err := decodeStage(StageFaults, in.Faults)
	if err == nil {
		var negative []decoded
		if negative, err = decodeStage(StageNegative, in.Negative); err == nil {
			var base baseAnswers
			if base, err = decodeBaseline(in.Baseline); err == nil {
				return judge(w, faults, negative, base)
			}
		}
	}
	fmt.Fprintf(w, "HK2 verdict=ERROR %s\n", err)
	return 2
}

// decoded is one scenario with its answer decoded.
type decoded struct {
	Scenario
	ans graph.Answer
}

type baseAnswers struct {
	cluster    graph.ClusterAnswer
	namespaces map[string]graph.Answer
}

func decodeCall(what string, c Call, v any) error {
	if c.Status != 200 {
		return fmt.Errorf("%s: status %d", what, c.Status)
	}
	if err := json.Unmarshal(c.Body, v); err != nil {
		return fmt.Errorf("%s: answer does not decode: %v", what, err)
	}
	return nil
}

// decodeStage decodes the answers of a scenario stage; the stage must hold A-1 to A-6
// once each, in order.
func decodeStage(stage string, in []Scenario) ([]decoded, error) {
	var ids []string
	for _, s := range in {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != strings.Join(IDs, ",") {
		return nil, fmt.Errorf("stage=%s holds scenarios [%s], not [%s]", stage, strings.Join(ids, ","), strings.Join(IDs, ","))
	}
	out := make([]decoded, len(in))
	for i, s := range in {
		out[i].Scenario = s
		if err := decodeCall(fmt.Sprintf("stage=%s %s namespace=%s", stage, s.ID, s.Namespace), s.Call, &out[i].ans); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func decodeBaseline(in Baseline) (baseAnswers, error) {
	out := baseAnswers{namespaces: map[string]graph.Answer{}}
	if err := decodeCall("stage=baseline cluster", in.Cluster, &out.cluster); err != nil {
		return out, err
	}
	var names []string
	for ns := range in.Namespaces {
		names = append(names, ns)
	}
	sort.Strings(names)
	for _, ns := range names {
		var a graph.Answer
		if err := decodeCall("stage=baseline namespace="+ns, in.Namespaces[ns], &a); err != nil {
			return out, err
		}
		out.namespaces[ns] = a
	}
	return out, nil
}

func judge(w io.Writer, faults, negative []decoded, base baseAnswers) int {
	faultResults := judgeStage(w, StageFaults, faults)
	faultTotal := passed(faultResults)
	fmt.Fprintf(w, "HK2 stage=%s total=%d/6\n", StageFaults, faultTotal)

	negResults := judgeStage(w, StageNegative, negative)
	negTotal := passed(negResults)
	negPass := negTotal == 5 && !negResults["A-3"]
	fmt.Fprintf(w, "HK2 stage=%s total=%d/6 expected=5/6 result=%s\n", StageNegative, negTotal, passFail(negPass))

	a0 := judgeBaseline(w, base)

	verdict := faultTotal == 6 && negPass && a0
	fmt.Fprintf(w, "HK2 total=%d/6 negative=%s a0=%s verdict=%s\n", faultTotal, passFail(negPass), passFail(a0), passFail(verdict))
	if verdict {
		return 0
	}
	return 1
}

func passed(results map[string]bool) int {
	n := 0
	for _, ok := range results {
		if ok {
			n++
		}
	}
	return n
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// printable writes U+00B7 as its escape, so every HK2 line is ASCII.
func printable(s string) string {
	return strings.ReplaceAll(s, string(rune(0x00b7)), "\\u00b7")
}

// scenarioCheck prints the expectation lines of one scenario.
type scenarioCheck struct {
	w     io.Writer
	stage string
	id    string
	ok    bool
}

func (c *scenarioCheck) line(name, expected, actual string, pass bool) {
	fmt.Fprintf(c.w, "HK2 stage=%s %s check=%s expected=%s actual=%s result=%s\n", c.stage, c.id, name, printable(expected), printable(actual), passFail(pass))
	c.ok = c.ok && pass
}

func judgeStage(w io.Writer, stage string, scenarios []decoded) map[string]bool {
	results := map[string]bool{}
	for _, s := range scenarios {
		c := &scenarioCheck{w: w, stage: stage, id: s.ID, ok: true}
		judgeScenario(c, s)
		fmt.Fprintf(w, "HK2 stage=%s %s result=%s\n", stage, s.ID, passFail(c.ok))
		results[s.ID] = c.ok
	}
	return results
}

// index is one answer looked up by node uid.
type index struct {
	ans   graph.Answer
	byUID map[string]graph.Node
}

func newIndex(a graph.Answer) index {
	x := index{ans: a, byUID: map[string]graph.Node{}}
	for _, n := range a.Nodes {
		x.byUID[n.UID] = n
	}
	return x
}

// node is the node of kind and name with apiVersion, if the answer holds one.
func (x index) node(apiVersion, kind, name string) (graph.Node, bool) {
	for _, n := range x.ans.Nodes {
		if n.APIVersion == apiVersion && n.Kind == kind && n.Name == name {
			return n, true
		}
	}
	return graph.Node{}, false
}

// bridgesFrom is every bridge of kind leaving the node uid.
func (x index) bridgesFrom(kind, uid string) []graph.Bridge {
	var out []graph.Bridge
	for _, b := range x.ans.Bridges {
		if b.Kind == kind && b.From.UID == uid {
			out = append(out, b)
		}
	}
	return out
}

// describe is one bridge as a check value: "<to kind>/<to name> <state> <text>".
func describe(b graph.Bridge) string {
	s := b.To.Kind + "/" + b.To.Name + " " + b.State
	if b.Text != "" {
		s += " " + b.Text
	}
	return s
}

func describeAll(bs []graph.Bridge) string {
	if len(bs) == 0 {
		return "none"
	}
	var parts []string
	for _, b := range bs {
		parts = append(parts, describe(b))
	}
	return strings.Join(parts, "; ")
}

func judgeScenario(c *scenarioCheck, s decoded) {
	x := newIndex(s.ans)
	if len(s.ans.Refused) > 0 {
		c.line("refused", "none", "refused "+s.ans.Refused[0].Text, false)
	}
	pods := checkPods(c, x, s.Pods)
	switch s.ID {
	case "A-1":
		fmt.Fprintf(c.w, "HK2 stage=%s %s read=before %s\n", c.stage, c.id, s.ReadBefore)
		fmt.Fprintf(c.w, "HK2 stage=%s %s read=after %s\n", c.stage, c.id, s.ReadAfter)
		for _, p := range pods {
			checkNodeState(c, p, graph.NodeFailing)
			text := nodeText(p)
			var candidates []string
			for _, r := range []ContainerRead{s.ReadBefore, s.ReadAfter} {
				if r.PodUID != p.UID || r.Container != CrashContainer {
					continue
				}
				if want, ok := r.Candidate(); ok && (len(candidates) == 0 || candidates[0] != want) {
					candidates = append(candidates, want)
				}
			}
			expected := strings.Join(candidates, " | ")
			if expected == "" {
				expected = "a candidate read of uid=" + p.UID + " container=" + CrashContainer + " (none)"
			}
			pass := false
			for _, want := range candidates {
				pass = pass || text == want
			}
			c.line("node-text pod="+p.Name, expected, text, pass)
		}
	case "A-2":
		for _, p := range pods {
			checkNodeState(c, p, graph.NodeFailing)
			text := nodeText(p)
			a, b := "ImagePullBackOff"+dot+missingImage, "ErrImagePull"+dot+missingImage
			c.line("node-text pod="+p.Name, a+" | "+b, text, text == a || text == b)
		}
	case "A-3":
		svc, ok := x.node("v1", "Service", "web")
		if !ok {
			c.line("service-pod from=Service/web", "Pod/ broken "+graph.SelectorNoPodsText("app=web"), "no Service web", false)
			break
		}
		bs := x.bridgesFrom(graph.BridgeServicePod, svc.UID)
		want := "Pod/ broken " + graph.SelectorNoPodsText("app=web")
		c.line("service-pod from=Service/web", want, describeAll(bs), len(bs) == 1 && describe(bs[0]) == want && bs[0].To.Group == "" && bs[0].To.UID == nil)
	case "A-4":
		svc, ok := x.node("v1", "Service", "web")
		for _, p := range pods {
			want := "Pod/" + p.Name + " broken " + graph.NamedPortText("http")
			if !ok {
				c.line("service-pod from=Service/web", want, "no Service web", false)
				continue
			}
			bs := toUID(x.bridgesFrom(graph.BridgeServicePod, svc.UID), p.UID)
			c.line("service-pod from=Service/web", want, describeAll(bs), len(bs) == 1 && describe(bs[0]) == want)
		}
	case "A-5":
		for _, p := range pods {
			bs := toName(x.bridgesFrom(graph.BridgePodConfigMap, p.UID), "ConfigMap", "app-config")
			want := "ConfigMap/app-config broken " + graph.NotFoundText("ConfigMap", "app-config")
			c.line("pod-configmap pod="+p.Name, want, describeAll(bs), len(bs) == 1 && describe(bs[0]) == want)
			checkNodeState(c, p, graph.NodeFailing)
			text := nodeText(p)
			c.line("node-text pod="+p.Name, "CreateContainerConfigError", text, text == "CreateContainerConfigError")
		}
	case "A-6":
		pvc, ok := x.node("v1", "PersistentVolumeClaim", "data")
		unbound := graph.ClassUnboundText("missing-class")
		if !ok {
			c.line("pvc-storageclass from=PersistentVolumeClaim/data", "StorageClass/missing-class broken "+unbound, "no PersistentVolumeClaim data", false)
		} else {
			bs := x.bridgesFrom(graph.BridgePVCStorageClass, pvc.UID)
			want := "StorageClass/missing-class broken " + unbound
			c.line("pvc-storageclass from=PersistentVolumeClaim/data", want, describeAll(bs), len(bs) == 1 && describe(bs[0]) == want)
			bs = x.bridgesFrom(graph.BridgePVCPV, pvc.UID)
			want = "PersistentVolume/ broken " + unbound
			c.line("pvc-pv from=PersistentVolumeClaim/data", want, describeAll(bs), len(bs) == 1 && describe(bs[0]) == want)
		}
		for _, p := range pods {
			bs := toName(x.bridgesFrom(graph.BridgePodPVC, p.UID), "PersistentVolumeClaim", "data")
			want := "PersistentVolumeClaim/data connected"
			pass := len(bs) == 1 && describe(bs[0]) == want && ok && bs[0].To.UID != nil && *bs[0].To.UID == pvc.UID
			c.line("pod-pvc pod="+p.Name, want, describeAll(bs), pass)
		}
	}
}

func toUID(bs []graph.Bridge, uid string) []graph.Bridge {
	var out []graph.Bridge
	for _, b := range bs {
		if b.To.UID != nil && *b.To.UID == uid {
			out = append(out, b)
		}
	}
	return out
}

func toName(bs []graph.Bridge, kind, name string) []graph.Bridge {
	var out []graph.Bridge
	for _, b := range bs {
		if b.To.Group == "" && b.To.Kind == kind && b.To.Name == name {
			out = append(out, b)
		}
	}
	return out
}

// checkPods compares the core Pod nodes of the answer with the Pods the stage expects
// (3.2 stale-pod rule) and returns the expected Pod nodes the answer holds.
func checkPods(c *scenarioCheck, x index, want []Pod) []graph.Node {
	expected := map[string]string{}
	var names []string
	for _, p := range want {
		expected[p.UID] = p.Name
		names = append(names, p.Name)
	}
	sort.Strings(names)
	var found []graph.Node
	var problems []string
	seen := map[string]bool{}
	for _, n := range x.ans.Nodes {
		if n.APIVersion != "v1" || n.Kind != "Pod" {
			continue
		}
		if _, ok := expected[n.UID]; !ok {
			problems = append(problems, "stale pod "+n.Name)
			continue
		}
		seen[n.UID] = true
		found = append(found, n)
	}
	for _, p := range want {
		if !seen[p.UID] {
			problems = append(problems, "missing pod "+p.Name)
		}
	}
	sort.Strings(problems)
	actual := "none"
	if len(problems) > 0 {
		actual = strings.Join(problems, ", ")
	} else if len(found) > 0 {
		var got []string
		for _, n := range found {
			got = append(got, n.Name)
		}
		sort.Strings(got)
		actual = strings.Join(got, ",")
	}
	exp := strings.Join(names, ",")
	if exp == "" {
		exp = "at least one pod"
	}
	c.line("pods", exp, actual, len(want) > 0 && len(problems) == 0)
	return found
}

func nodeText(n graph.Node) string {
	if n.NodeState == nil {
		return "no nodeState"
	}
	return n.NodeState.Text
}

func checkNodeState(c *scenarioCheck, n graph.Node, want string) {
	actual := "no nodeState"
	if n.NodeState != nil {
		actual = n.NodeState.State
	}
	c.line("node-state pod="+n.Name, want, actual, actual == want)
}

// judgeBaseline prints the A-0 lines (3.3) and returns whether A-0 passed.
func judgeBaseline(w io.Writer, base baseAnswers) bool {
	ok := true
	fail := func(check, expected, actual string) {
		fmt.Fprintf(w, "HK2 stage=%s A-0 check=%s expected=%s actual=%s result=FAIL\n", StageBaseline, check, printable(expected), printable(actual))
		ok = false
	}
	if !base.cluster.Namespaces.Listed {
		fail("namespaces-listed", "true", "false from="+base.cluster.Namespaces.From)
	}
	for _, r := range base.cluster.Refused {
		fail("refused namespace=cluster", "none", "refused "+r.Text)
	}
	for _, ns := range base.cluster.Namespaces.Items {
		if _, found := base.namespaces[ns]; !found {
			fail("answer namespace="+ns, "answer", "missing")
		}
	}
	scenario := map[string]bool{}
	for _, ns := range ScenarioNamespaces {
		scenario[ns] = true
	}
	var counts struct{ broken, warning, failing, waiting, optional, unknown int }
	var names []string
	for ns := range base.namespaces {
		names = append(names, ns)
	}
	sort.Strings(names)
	idle := "no answer for " + baselineNS
	for _, ns := range names {
		a := base.namespaces[ns]
		x := newIndex(a)
		for _, r := range a.Refused {
			fail("refused namespace="+ns, "none", "refused "+r.Text)
		}
		for _, b := range a.Bridges {
			switch b.State {
			case graph.BridgeBroken:
				counts.broken++
			case graph.BridgeWarning:
				if scenario[ns] {
					counts.warning++
				}
			case graph.BridgeWaiting:
				counts.waiting++
			case graph.BridgeOptional:
				counts.optional++
			case graph.BridgeUnknown:
				counts.unknown++
			default:
				continue
			}
			from := "uid:" + b.From.UID
			if n, found := x.byUID[b.From.UID]; found {
				from = n.Kind + "/" + n.Name
			}
			fmt.Fprintf(w, "HK2 stage=%s A-0 item=%s namespace=%s bridge=%s from=%s to=%s/%s text=%s\n", StageBaseline, b.State, ns, b.Kind, from, b.To.Kind, b.To.Name, printable(b.Text))
		}
		for _, n := range a.Nodes {
			if n.APIVersion == "v1" && n.Kind == "Pod" && n.NodeState != nil && n.NodeState.State == graph.NodeFailing {
				if scenario[ns] {
					counts.failing++
				}
				fmt.Fprintf(w, "HK2 stage=%s A-0 item=failing namespace=%s node=Pod/%s text=%s\n", StageBaseline, ns, n.Name, printable(n.NodeState.Text))
			}
		}
		if ns == baselineNS {
			idle = "no PersistentVolumeClaim " + idleClaim
			if pvc, found := x.node("v1", "PersistentVolumeClaim", idleClaim); found {
				idle = describeAll(x.bridgesFrom(graph.BridgePVCPV, pvc.UID))
			}
		}
	}
	want := "PersistentVolume/ waiting " + graph.ConsumerWaitText(idleClaim)
	idlePass := idle == want
	fmt.Fprintf(w, "HK2 stage=%s A-0 check=idle-waiting expected=%s actual=%s result=%s\n", StageBaseline, want, printable(idle), passFail(idlePass))
	pass := ok && idlePass && counts.broken == 0 && counts.warning == 0 && counts.failing == 0
	fmt.Fprintf(w, "HK2 stage=%s A-0 broken=%d warning=%d failing=%d waiting=%d optional=%d unknown=%d result=%s\n",
		StageBaseline, counts.broken, counts.warning, counts.failing, counts.waiting, counts.optional, counts.unknown, passFail(pass))
	return pass
}
