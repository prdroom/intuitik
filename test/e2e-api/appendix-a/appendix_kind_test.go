//go:build kind

package appendixa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/internal/graph"
	"github.com/prdroom/intuitik/tools/hk2judge"
)

// scenarios is the directory of the Appendix A manifests (packet 6.5).
var scenarios = filepath.Join("..", "..", "..", "scenarios", "appendix-a")

// finalLine is the only last line the test accepts (packet 6.6).
const finalLine = "HK2 total=6/6 negative=PASS a0=PASS verdict=PASS"

// manifest is the file of scenario id ("A-1") in its fault or healthy version.
func manifest(id string, healthy bool) string {
	name := "a" + strings.TrimPrefix(id, "A-")
	if healthy {
		name += "-healthy"
	}
	return filepath.Join(scenarios, name+".yaml")
}

// podStatus is the part of a Pod these waits and records read with kubectl.
type podStatus struct {
	Metadata struct {
		Name              string  `json:"name"`
		UID               string  `json:"uid"`
		DeletionTimestamp *string `json:"deletionTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Name string `json:"name"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string            `json:"phase"`
		ContainerStatuses []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type containerStatus struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int64  `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting"`
	} `json:"state"`
}

func pods(t *testing.T, ns string) []podStatus {
	t.Helper()
	var v struct {
		Items []podStatus `json:"items"`
	}
	if err := json.Unmarshal(kubectl(t, "get", "pods", "-n", ns, "-o", "json"), &v); err != nil {
		t.Fatalf("HK2 verdict=ERROR preparation: pods of %s: %v", ns, err)
	}
	return v.Items
}

// claimPhase is the phase of PVC name in ns, or "" when it cannot be read.
func claimPhase(ns, name string) string {
	out, err := kubectlErr("get", "pvc", name, "-n", ns, "-o", "jsonpath={.status.phase}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// onePod is the single Pod of ns that is not terminating, if there is exactly one Pod.
func onePod(t *testing.T, ns string) (podStatus, bool) {
	ps := pods(t, ns)
	if len(ps) != 1 || ps[0].Metadata.DeletionTimestamp != nil {
		return podStatus{}, false
	}
	return ps[0], true
}

func waitingReason(p podStatus) string {
	for _, c := range p.Status.ContainerStatuses {
		if c.State.Waiting != nil {
			return c.State.Waiting.Reason
		}
	}
	return ""
}

func runningReady(p podStatus) bool {
	if p.Status.Phase != "Running" || len(p.Status.ContainerStatuses) != len(p.Spec.Containers) {
		return false
	}
	for _, c := range p.Status.ContainerStatuses {
		if !c.Ready {
			return false
		}
	}
	return true
}

// condition is one wait of a stage: what it waits for and when it holds.
type condition struct {
	what string
	ok   func() bool
}

// waitAll polls every condition against one deadline and prints each result; a wait that
// ends without its condition is printed and left to the judge (FAIL, not ERROR).
func waitAll(t *testing.T, stage string, limit time.Duration, conds ...condition) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(limit)
	done := make([]bool, len(conds))
	for {
		left := 0
		for i, c := range conds {
			if !done[i] && c.ok() {
				done[i] = true
				t.Logf("appendix-a wait stage=%s %s reached after %s", stage, c.what, time.Since(start).Round(time.Second))
			}
			if !done[i] {
				left++
			}
		}
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			for i, c := range conds {
				if !done[i] {
					t.Logf("appendix-a wait stage=%s %s not reached within %s", stage, c.what, limit)
				}
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func crashing(t *testing.T) condition {
	return condition{"hk2-a1 CrashLoopBackOff with restartCount >= 1", func() bool {
		p, ok := onePod(t, "hk2-a1")
		if !ok {
			return false
		}
		for _, c := range p.Status.ContainerStatuses {
			if c.State.Waiting != nil && c.State.Waiting.Reason == "CrashLoopBackOff" && c.RestartCount >= 1 {
				return true
			}
		}
		return false
	}}
}

func waiting(t *testing.T, ns string, reasons ...string) condition {
	return condition{ns + " waiting " + strings.Join(reasons, "|"), func() bool {
		p, ok := onePod(t, ns)
		if !ok {
			return false
		}
		r := waitingReason(p)
		for _, want := range reasons {
			if r == want {
				return true
			}
		}
		return false
	}}
}

func ready(t *testing.T, ns string) condition {
	return condition{ns + " one Pod Running and ready", func() bool {
		p, ok := onePod(t, ns)
		return ok && runningReady(p)
	}}
}

func claim(t *testing.T, ns, name, phase string, podPending bool) condition {
	what := fmt.Sprintf("%s PVC %s %s", ns, name, phase)
	if podPending {
		what += " and its Pod Pending"
	}
	return condition{what, func() bool {
		if claimPhase(ns, name) != phase {
			return false
		}
		if !podPending {
			return true
		}
		p, ok := onePod(t, ns)
		return ok && p.Status.Phase == "Pending"
	}}
}

// apply applies the manifests; a manifest kubectl rejects is a preparation failure.
func apply(t *testing.T, files ...string) {
	t.Helper()
	for _, f := range files {
		kubectl(t, "apply", "-f", f)
		t.Logf("appendix-a applied %s", filepath.ToSlash(f))
	}
}

// removeNamespaces deletes the namespaces and waits until a GET finds none of them
// (packet 6.5); a namespace that does not go away is a preparation failure.
func removeNamespaces(t *testing.T, names ...string) {
	t.Helper()
	kubectl(t, append(append([]string{"delete", "namespace"}, names...), "--wait", "--timeout=120s")...)
	for _, ns := range names {
		out := kubectl(t, "get", "namespace", ns, "--ignore-not-found", "-o", "name")
		if len(bytes.TrimSpace(out)) != 0 {
			t.Fatalf("HK2 verdict=ERROR preparation: namespace %s still exists after delete --wait", ns)
		}
	}
	t.Logf("appendix-a removed namespaces %s", strings.Join(names, ","))
}

// expected records the Pods of ns that are not terminating, as the stage expects them.
func expected(t *testing.T, ns string) []hk2judge.Pod {
	var out []hk2judge.Pod
	for _, p := range pods(t, ns) {
		if p.Metadata.DeletionTimestamp == nil {
			out = append(out, hk2judge.Pod{UID: p.Metadata.UID, Name: p.Metadata.Name})
		}
	}
	return out
}

// restarts is the restartCount of container crash of the A-1 Pod, or -1 when it cannot be
// read.
func restarts(t *testing.T) int64 {
	p, ok := onePod(t, "hk2-a1")
	if !ok {
		return -1
	}
	for _, c := range p.Status.ContainerStatuses {
		if c.Name == "crash" {
			return c.RestartCount
		}
	}
	return -1
}

// readStage calls the containment answer of the six scenario namespaces of one stage,
// with A-1's restartCount read with kubectl just before and just after its call.
func readStage(t *testing.T, p *panel, stage string) []hk2judge.Scenario {
	t.Helper()
	var out []hk2judge.Scenario
	for _, id := range hk2judge.IDs {
		ns := hk2judge.ScenarioNamespaces[id]
		s := hk2judge.Scenario{ID: id, Namespace: ns, Pods: expected(t, ns)}
		if id == "A-1" {
			s.RestartsBefore = restarts(t)
		}
		s.Call.Status, s.Call.Body = p.request("GET", "/api/graph/containment?namespace="+ns, nil, true)
		if id == "A-1" {
			s.RestartsAfter = restarts(t)
			t.Logf("appendix-a stage=%s A-1 restartCount before=%d after=%d (kubectl, container crash)", stage, s.RestartsBefore, s.RestartsAfter)
		}
		var names []string
		for _, pod := range s.Pods {
			names = append(names, pod.Name+"="+pod.UID)
		}
		t.Logf("appendix-a call stage=%s %s namespace=%s status=%d bytes=%d expected-pods=%s", stage, id, ns, s.Call.Status, len(s.Call.Body), strings.Join(names, ","))
		out = append(out, s)
	}
	return out
}

// readBaseline calls the cluster answer and the containment answer of every namespace it
// lists.
func readBaseline(t *testing.T, p *panel) hk2judge.Baseline {
	t.Helper()
	b := hk2judge.Baseline{Namespaces: map[string]hk2judge.Call{}}
	b.Cluster.Status, b.Cluster.Body = p.request("GET", "/api/graph/cluster", nil, true)
	var cluster graph.ClusterAnswer
	json.Unmarshal(b.Cluster.Body, &cluster)
	t.Logf("appendix-a call stage=baseline cluster status=%d namespaces=%s listed=%v", b.Cluster.Status, strings.Join(cluster.Namespaces.Items, ","), cluster.Namespaces.Listed)
	for _, ns := range cluster.Namespaces.Items {
		var c hk2judge.Call
		c.Status, c.Body = p.request("GET", "/api/graph/containment?namespace="+ns, nil, true)
		t.Logf("appendix-a call stage=baseline namespace=%s status=%d bytes=%d", ns, c.Status, len(c.Body))
		b.Namespaces[ns] = c
	}
	return b
}

// defaultClass checks the section 8 stop condition: kind's default StorageClass is named
// standard and binds WaitForFirstConsumer (A-0 idle and A-6 healthy rest on it).
func defaultClass(t *testing.T) {
	t.Helper()
	var v struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			VolumeBindingMode string `json:"volumeBindingMode"`
		} `json:"items"`
	}
	if err := json.Unmarshal(kubectl(t, "get", "storageclass", "-o", "json"), &v); err != nil {
		t.Fatalf("HK2 verdict=ERROR preparation: storage classes: %v", err)
	}
	var defaults []string
	for _, c := range v.Items {
		if c.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			defaults = append(defaults, c.Metadata.Name+"/"+c.VolumeBindingMode)
		}
	}
	sort.Strings(defaults)
	t.Logf("appendix-a default StorageClass expected=standard/WaitForFirstConsumer actual=%s", strings.Join(defaults, ","))
	if strings.Join(defaults, ",") != "standard/WaitForFirstConsumer" {
		t.Fatalf("HK2 verdict=ERROR preparation: default StorageClass is %q, not standard/WaitForFirstConsumer", strings.Join(defaults, ","))
	}
}

// TestA_AppendixA (FA2-T7b C2 to C10): stage faults, stage negative (A-3 healthy), stage
// baseline (every scenario healthy), then one judge call over the three stages with every
// HK2 line printed. It passes only on exit 0 with the final line PASS.
func TestA_AppendixA(t *testing.T) {
	defaultClass(t)
	p := adminPanel(t)

	// Stage 1, faults: hk2-a0 and the six faults (6.6, at most 180 s).
	files := []string{filepath.Join(scenarios, "a0.yaml")}
	for _, id := range hk2judge.IDs {
		files = append(files, manifest(id, false))
	}
	apply(t, files...)
	waitAll(t, hk2judge.StageFaults, 180*time.Second,
		crashing(t),
		waiting(t, "hk2-a2", "ErrImagePull", "ImagePullBackOff"),
		ready(t, "hk2-a3"),
		ready(t, "hk2-a4"),
		waiting(t, "hk2-a5", "CreateContainerConfigError"),
		claim(t, "hk2-a6", "data", "Pending", true),
		claim(t, "hk2-a0", "idle", "Pending", false))
	faults := readStage(t, p, hk2judge.StageFaults)

	// Stage 2, negative: A-3 switched to its healthy version (120 s + 120 s).
	removeNamespaces(t, "hk2-a3")
	apply(t, manifest("A-3", true))
	waitAll(t, hk2judge.StageNegative, 120*time.Second, ready(t, "hk2-a3"))
	negative := readStage(t, p, hk2judge.StageNegative)

	// Stage 3, baseline: the five other scenarios switched to healthy (120 s + 180 s).
	rest := []string{"A-1", "A-2", "A-4", "A-5", "A-6"}
	var namespaces []string
	for _, id := range rest {
		namespaces = append(namespaces, hk2judge.ScenarioNamespaces[id])
	}
	removeNamespaces(t, namespaces...)
	files = nil
	for _, id := range rest {
		files = append(files, manifest(id, true))
	}
	apply(t, files...)
	var conds []condition
	for _, id := range hk2judge.IDs {
		conds = append(conds, ready(t, hk2judge.ScenarioNamespaces[id]))
	}
	conds = append(conds, claim(t, "hk2-a6", "data", "Bound", false), claim(t, "hk2-a0", "idle", "Pending", false))
	waitAll(t, hk2judge.StageBaseline, 180*time.Second, conds...)
	base := readBaseline(t, p)

	// One judge call over the three stages; every line goes to the job log as printed.
	var out bytes.Buffer
	code := hk2judge.Judge(hk2judge.Input{Faults: faults, Negative: negative, Baseline: base}, io.MultiWriter(os.Stdout, &out))
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	last := got[len(got)-1]
	t.Logf("appendix-a judge exit=%d last=%q", code, last)
	if code != 0 || last != finalLine {
		t.Errorf("appendix-a: judge exit %d, last line %q; want 0 and %q", code, last, finalLine)
	}
}
