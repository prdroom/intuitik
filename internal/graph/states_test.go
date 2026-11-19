package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// Unit tests of the bridge states, the namespace states and the cluster answer (packet
// FA2-T7a sections 3 and 6, job unit). Each checked bridge prints one line:
//   FA2-T7a <criterion> input=<fixture> kind=<bridge kind> from=<kind/name> to=<ns/kind/name uid|null> state=<expected>/<actual> expected-text=<...> actual-text=<...> [expected-uid=<...>] result=<PASS|FAIL>
// and a case prints its counts:
//   FA2-T7a <criterion> counts connected=<n> waiting=<n> broken=<n> warning=<n> unknown=<n> optional=<n>
// The expected strings are written here independently of the product (section 3).

// wantState is the expected state and text of one bridge; toName "" is a target without a
// name (a selector). uid "-" is not checked, "" expects null.
type wantState struct {
	kind, from, ns, toKind, toName string
	state, text                    string
	uid                            string
}

// exactBridges are the bridges of kind from the node from to exactly ns/toKind/toName.
func exactBridges(a Answer, kind, from, ns, toKind, toName string) []Bridge {
	var out []Bridge
	for _, b := range a.Bridges {
		if b.Kind == kind && nodeName(a, b.From.UID) == from && b.To.Namespace == ns && b.To.Kind == toKind && b.To.Name == toName {
			out = append(out, b)
		}
	}
	return out
}

func checkStates(t *testing.T, criterion, input string, a Answer, ws ...wantState) {
	t.Helper()
	for _, w := range ws {
		got := exactBridges(a, w.kind, w.from, w.ns, w.toKind, w.toName)
		state, text, uid := "absent", "", "-"
		if len(got) > 1 {
			state = fmt.Sprintf("%d bridges", len(got))
		}
		if len(got) == 1 {
			state, text, uid = got[0].State, got[0].Text, uidText(got[0].To.UID)
		}
		wantUID := w.uid
		if wantUID == "" {
			wantUID = "null"
		}
		pass := len(got) == 1 && state == w.state && text == w.text && (w.uid == "-" || uid == wantUID)
		line := fmt.Sprintf("FA2-T7a %s input=%s kind=%s from=%s to=%s/%s/%s %s state=%s/%s expected-text=%s actual-text=%s",
			criterion, input, w.kind, w.from, w.ns, w.toKind, w.toName, uid, w.state, state, w.text, text)
		if w.uid != "-" {
			line += " expected-uid=" + wantUID
		}
		t.Logf("%s result=%s", line, passFail(pass))
		if !pass {
			t.Errorf("FA2-T7a %s %s %s -> %s/%s/%s: state %s text %q uid %s (%d bridges); want %s %q uid %s",
				criterion, w.kind, w.from, w.ns, w.toKind, w.toName, state, text, uid, len(got), w.state, w.text, wantUID)
		}
	}
}

// counts prints and returns the number of bridges per state.
func counts(t *testing.T, criterion, input string, a Answer) map[string]int {
	t.Helper()
	n := map[string]int{}
	for _, b := range a.Bridges {
		n[b.State]++
	}
	t.Logf("FA2-T7a %s input=%s counts connected=%d waiting=%d broken=%d warning=%d unknown=%d optional=%d",
		criterion, input, n[BridgeConnected], n[BridgeWaiting], n[BridgeBroken], n[BridgeWarning], n[BridgeUnknown], n[BridgeOptional])
	return n
}

// brokenZero checks that a case has no broken bridge.
func brokenZero(t *testing.T, criterion, input string, a Answer) {
	t.Helper()
	if n := counts(t, criterion, input, a); n[BridgeBroken] != 0 {
		t.Errorf("FA2-T7a %s %s: %d broken bridges, want 0", criterion, input, n[BridgeBroken])
	}
}

// clusterObject is an object with no namespace.
func clusterObject(apiVersion, kind, name, uid, js string) *unstructured.Unstructured {
	u := objJSON(apiVersion, kind, name, uid, js)
	u.SetNamespace("")
	return u
}

func buildCluster(t *testing.T, f *fakeAPI, contextNamespace string) (ClusterAnswer, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), contextLimit)
	defer cancel()
	return BuildCluster(ctx, f.open, contextNamespace)
}

func clusterFixture() *fakeAPI {
	return newFake().add(
		clusterObject("v1", "Namespace", "b-ns", "ns-b", `{}`),
		clusterObject("v1", "Namespace", "a-ns", "ns-a", `{}`),
		clusterObject("v1", "Node", "n1", "node-1", `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`),
		clusterObject("v1", "Node", "n2", "node-2", `{"status":{"conditions":[{"type":"Ready","status":"False"}]}}`),
		clusterObject("storage.k8s.io/v1", "StorageClass", "standard", "sc-standard", `{"metadata":{}}`),
		clusterObject("storage.k8s.io/v1", "StorageClass", "fast", "sc-fast", `{}`),
		clusterObject("v1", "PersistentVolume", "pv-1", "pv-1", `{}`),
	)
}

// markDefault puts the default-class annotation on StorageClass name.
func markDefault(f *fakeAPI, name, annotation string) {
	for _, u := range f.objects {
		if u.GetKind() == "StorageClass" && u.GetName() == name {
			u.SetAnnotations(map[string]string{annotation: "true"})
		}
	}
}

func clusterJSON(a ClusterAnswer) string {
	b, _ := json.Marshal(a)
	return string(b)
}

// TestStates_Cluster (C1): every Namespace sorted, the Nodes and how many are Ready, the
// StorageClasses with the default one marked (GA and beta annotation), the PV count.
func TestStates_Cluster(t *testing.T) {
	for _, an := range []string{"storageclass.kubernetes.io/is-default-class", "storageclass.beta.kubernetes.io/is-default-class"} {
		f := clusterFixture()
		markDefault(f, "standard", an)
		a, err := buildCluster(t, f, "")
		want := `{"namespaces":{"listed":true,"items":["a-ns","b-ns","team-a"],"from":""},"band":{"nodes":{"total":2,"ready":1},` +
			`"storageClasses":[{"name":"fast","default":false},{"name":"standard","default":true}],"persistentVolumes":{"total":1}},"refused":[]}`
		got := clusterJSON(a)
		pass := err == nil && got == want
		t.Logf("FA2-T7a C1 input=cluster-%s expected=%s actual=%s error=%v result=%s", an, want, got, err, passFail(pass))
		if !pass {
			t.Errorf("C1 %s: %s, error %v; want %s", an, got, err, want)
		}
	}
}

// TestStates_NamespacesRefused (C2): a refused Namespaces LIST names the kubeconfig
// context namespace, or default, never an empty listed cluster.
func TestStates_NamespacesRefused(t *testing.T) {
	for _, c := range []struct{ contextNS, want string }{
		{"shop", `{"listed":false,"items":["shop"],"from":"kubeconfig"}`},
		{"", `{"listed":false,"items":["default"],"from":"default"}`},
	} {
		f := clusterFixture()
		f.lists["/namespaces"] = fail{status: 403}
		a, err := buildCluster(t, f, c.contextNS)
		got, _ := json.Marshal(a.Namespaces)
		var refused []string
		for _, r := range a.Refused {
			refused = append(refused, fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text))
		}
		wantRefused := []string{"Namespace 403 Couldn't load Namespaces (403)"}
		pass := err == nil && string(got) == c.want && reflect.DeepEqual(refused, wantRefused)
		t.Logf("FA2-T7a C2 input=namespaces-403 context-namespace=%q expected=%s actual=%s expected-refused=%q actual-refused=%q result=%s",
			c.contextNS, c.want, got, wantRefused, refused, passFail(pass))
		if !pass {
			t.Errorf("C2 %q: %s refused %q error %v; want %s and %q", c.contextNS, got, refused, err, c.want, wantRefused)
		}
	}
}

// TestStates_BandRefused (C3): a refused Nodes LIST leaves the nodes part out, never
// zero; a Nodes LIST answered 500 fails the call.
func TestStates_BandRefused(t *testing.T) {
	f := clusterFixture()
	f.lists["/nodes"] = fail{status: 403}
	a, err := buildCluster(t, f, "")
	var refused []string
	for _, r := range a.Refused {
		refused = append(refused, fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text))
	}
	band, _ := json.Marshal(a.Band)
	wantRefused := []string{"Node 403 Couldn't load Nodes (403)"}
	pass := err == nil && a.Band.Nodes == nil && !strings.Contains(string(band), `"nodes"`) && reflect.DeepEqual(refused, wantRefused)
	t.Logf("FA2-T7a C3 input=nodes-403 expected=no-nodes-part refused=%q actual=band %s refused=%q result=%s", wantRefused, band, refused, passFail(pass))
	if !pass {
		t.Errorf("C3 403: band %s refused %q error %v", band, refused, err)
	}
	f = clusterFixture()
	f.lists["/nodes"] = fail{status: 500}
	_, err = buildCluster(t, f, "")
	var le *LoadError
	got := ""
	if errors.As(err, &le) {
		got = le.Error()
	}
	want := "Couldn't load Nodes (500)"
	t.Logf("FA2-T7a C3 input=nodes-500 expected-text=%s actual-text=%s result=%s", want, got, passFail(got == want))
	if got != want {
		t.Errorf("C3 500: error %v, want %q", err, want)
	}
}

// nsState is the namespace state of a as "<state> existenceChecked=<bool>".
func nsState(a Answer) string {
	return fmt.Sprintf("%s existenceChecked=%v", a.NamespaceState.State, a.NamespaceState.ExistenceChecked)
}

func checkNamespace(t *testing.T, criterion, input string, a Answer, err error, want string) {
	t.Helper()
	got := nsState(a)
	pass := err == nil && got == want
	t.Logf("FA2-T7a %s input=%s expected=%s actual=%s error=%v result=%s", criterion, input, want, got, err, passFail(pass))
	if !pass {
		t.Errorf("FA2-T7a %s %s: %s error %v, want %s", criterion, input, got, err, want)
	}
}

// TestStates_EmptyNamespace (C4): a namespace whose only object is kube-root-ca.crt is
// empty; with one more ConfigMap it is ok.
func TestStates_EmptyNamespace(t *testing.T) {
	f := newFake().add(obj("v1", "ConfigMap", "kube-root-ca.crt", "cm-root"))
	a, err := f.build(t)
	checkNamespace(t, "C4", "only-kube-root-ca", a, err, "empty existenceChecked=true")
	f = newFake().add(obj("v1", "ConfigMap", "kube-root-ca.crt", "cm-root"), obj("v1", "ConfigMap", "x", "cm-x"))
	a, err = f.build(t)
	checkNamespace(t, "C4", "configmap-x", a, err, "ok existenceChecked=true")
}

// everyNamespacedList are the fake keys of every namespaced LIST of a call with no group
// outside list B.
var everyNamespacedList = []string{"/pods", "apps/replicasets", "apps/deployments", "apps/statefulsets", "apps/daemonsets", "batch/jobs", "batch/cronjobs",
	"/services", "/persistentvolumeclaims", "networking.k8s.io/ingresses", "autoscaling/horizontalpodautoscalers", "/configmaps", "/secrets"}

// TestStates_NamespaceMissingAndLocked (C5): a Namespace that does not exist is not-found,
// one whose every LIST is refused is no-permission, never empty; a refused Namespace GET
// with the LISTs read leaves the state to the LISTs with existenceChecked false.
func TestStates_NamespaceMissingAndLocked(t *testing.T) {
	f := newFake()
	f.gets["Namespace/"+testNS] = fail{status: 404}
	a, err := f.build(t)
	checkNamespace(t, "C5", "namespace-404", a, err, "not-found existenceChecked=true")
	f = newFake()
	for _, k := range everyNamespacedList {
		f.lists[k] = fail{status: 403}
	}
	f.gets["Namespace/"+testNS] = fail{status: 403}
	a, err = f.build(t)
	checkNamespace(t, "C5", "every-list-403", a, err, "no-permission existenceChecked=false")
	f = newFake().add(obj("v1", "ConfigMap", "kube-root-ca.crt", "cm-root"))
	f.gets["Namespace/"+testNS] = fail{status: 403}
	a, err = f.build(t)
	checkNamespace(t, "C5", "namespace-get-403", a, err, "empty existenceChecked=false")
}

// storageFixtureC6 is the waiting claims of C6 and C13.
func waitingFixture() *fakeAPI {
	f := newFake()
	f.add(
		clusterObject("storage.k8s.io/v1", "StorageClass", "wffc", "sc-wffc", `{"volumeBindingMode":"WaitForFirstConsumer"}`),
		clusterObject("storage.k8s.io/v1", "StorageClass", "imm", "sc-imm", `{}`),
		clusterObject("v1", "PersistentVolume", "pv-b", "pv-b", `{"spec":{"storageClassName":"wffc"}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-w", "pvc-w", `{"spec":{"storageClassName":"wffc"},"status":{"phase":"Pending"}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-n", "pvc-n", `{"spec":{"storageClassName":"wffc"},"status":{"phase":"Pending"}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-i", "pvc-i", `{"spec":{"storageClassName":"imm"},"status":{"phase":"Pending"}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-x", "pvc-x", `{"spec":{},"status":{"phase":"Pending"}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-b", "pvc-b", `{"spec":{"storageClassName":"wffc","volumeName":"pv-b"},"status":{"phase":"Bound","capacity":{"storage":"1Gi"}}}`),
		objJSON("v1", "Pod", "w", "p-w", `{"spec":{"containers":[{"name":"c","image":"pause","volumeMounts":[{"name":"d","mountPath":"/d"}]}],
			"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"data-w"}}]}}`),
	)
	return f
}

// TestStates_WaitingClaim (C6, V-1, V-3 to V-6): a claim without a volume waits, with the
// text of its case; a bound claim is connected to its PV node; the Pod of the claim is
// connected to it; nothing is broken.
func TestStates_WaitingClaim(t *testing.T) {
	a := mustBuild(t, waitingFixture())
	checkStates(t, "C6", "waiting-claims", a,
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-w", "", "PersistentVolume", "", BridgeWaiting, "data-w waits for consumer Pod w", ""},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-n", "", "PersistentVolume", "", BridgeWaiting, "data-n waits for a consumer Pod", ""},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-i", "", "PersistentVolume", "", BridgeWaiting, "data-i waits for a volume to be provisioned", ""},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-x", "", "PersistentVolume", "", BridgeWaiting, "data-x waits for a matching PersistentVolume", ""},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-b", "", "PersistentVolume", "pv-b", BridgeConnected, "", "pv-b"},
		wantState{BridgePodPVC, "Pod/w", testNS, "PersistentVolumeClaim", "data-w", BridgeConnected, "", "pvc-w"},
		wantState{BridgePVCStorageClass, "PersistentVolumeClaim/data-w", "", "StorageClass", "wffc", BridgeConnected, "", "sc-wffc"},
		wantState{BridgePVStorageClass, "PersistentVolume/pv-b", "", "StorageClass", "wffc", BridgeConnected, "", "sc-wffc"})
	brokenZero(t, "C6", "waiting-claims", a)
}

// TestStates_NumericTargetPort (C7, S-1, S-4): a numeric targetPort that no container of the
// Pod declares is a warning; one that a container declares is connected.
func TestStates_NumericTargetPort(t *testing.T) {
	f := newFake().add(
		objJSON("v1", "Service", "num", "svc-num", `{"spec":{"selector":{"app":"num"},"ports":[{"port":9000,"targetPort":9000}]}}`),
		objJSON("v1", "Pod", "num-1", "p-num", `{"labels":{"app":"num"},"spec":{"containers":[{"name":"c","image":"pause","ports":[{"containerPort":8080}]}]}}`),
		objJSON("v1", "Service", "num-ok", "svc-ok", `{"spec":{"selector":{"app":"ok"},"ports":[{"port":80,"targetPort":8080}]}}`),
		objJSON("v1", "Pod", "ok-1", "p-ok", `{"labels":{"app":"ok"},"spec":{"containers":[{"name":"c","image":"pause","ports":[{"containerPort":8080}]}]}}`),
	)
	a := mustBuild(t, f)
	checkStates(t, "C7", "numeric-target-port", a,
		wantState{BridgeServicePod, "Service/num", testNS, "Pod", "num-1", BridgeWarning, "targetPort 9000 is not declared by the container (traffic not verifiable)", "p-num"},
		wantState{BridgeServicePod, "Service/num-ok", testNS, "Pod", "ok-1", BridgeConnected, "", "p-ok"})
	brokenZero(t, "C7", "numeric-target-port", a)
}

// TestStates_Optional (C8, C-2, C-3): a missing target named only by optional references
// is optional; the same target named once optional and once not is broken.
func TestStates_Optional(t *testing.T) {
	f := newFake().add(objJSON("v1", "Pod", "opt", "p-opt", `{"spec":{"containers":[{"name":"c","image":"pause",
		"env":[{"name":"FF","valueFrom":{"configMapKeyRef":{"name":"feature-flags","key":"on","optional":true}}}],
		"volumeMounts":[{"name":"m","mountPath":"/m"}]}],
		"volumes":[{"name":"m","secret":{"secretName":"maybe","optional":true}}]}}`))
	a := mustBuild(t, f)
	checkStates(t, "C8", "optional-missing", a,
		wantState{BridgePodConfigMap, "Pod/opt", testNS, "ConfigMap", "feature-flags", BridgeOptional, "ConfigMap feature-flags not found", ""},
		wantState{BridgePodSecret, "Pod/opt", testNS, "Secret", "maybe", BridgeOptional, "Secret maybe not found", ""})
	brokenZero(t, "C8", "optional-missing", a)
	f = newFake().add(objJSON("v1", "Pod", "mixed", "p-mixed", `{"spec":{"containers":[{"name":"c","image":"pause",
		"env":[{"name":"A","valueFrom":{"configMapKeyRef":{"name":"shared","key":"a","optional":true}}},
			{"name":"B","valueFrom":{"configMapKeyRef":{"name":"shared","key":"b"}}}]}]}}`))
	checkStates(t, "C8", "optional-and-required", mustBuild(t, f),
		wantState{BridgePodConfigMap, "Pod/mixed", testNS, "ConfigMap", "shared", BridgeBroken, "ConfigMap shared not found", ""})
}

// TestStates_ViewRole (C9, C-4): with the Secrets LIST refused, both Secret bridges are
// unknown with no uid, the ConfigMap bridge is connected to its node, and nothing says not
// found.
func TestStates_ViewRole(t *testing.T) {
	f := newFake()
	configFixture(f)
	f.lists["/secrets"] = fail{status: 403}
	a := mustBuild(t, f)
	checkStates(t, "C9", "secrets-list-403", a,
		wantState{BridgePodSecret, "Pod/api", testNS, "Secret", "db", BridgeUnknown, "no permission to read Secrets", ""},
		wantState{BridgePodSecret, "Pod/api", testNS, "Secret", "tls", BridgeUnknown, "no permission to read Secrets", ""},
		wantState{BridgePodConfigMap, "Pod/api", testNS, "ConfigMap", "app", BridgeConnected, "", "cm-app"})
	var refused []string
	for _, r := range a.Refused {
		refused = append(refused, fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text))
	}
	notFound := 0
	for _, b := range a.Bridges {
		if strings.Contains(b.Text, "not found") {
			notFound++
		}
	}
	want := []string{"Secret 403 Couldn't load Secrets (403)"}
	pass := reflect.DeepEqual(refused, want) && notFound == 0
	t.Logf("FA2-T7a C9 input=secrets-list-403 expected-refused=%q actual-refused=%q not-found-texts=%d result=%s", want, refused, notFound, passFail(pass))
	if !pass {
		t.Errorf("C9: refused %q, not found %d; want %q and 0", refused, notFound, want)
	}
	brokenZero(t, "C9", "secrets-list-403", a)
}

// TestStates_SelectorNoPods (C10, S-2): a selector that matches no Pod gives one bridge to
// a Pod without a name, broken.
func TestStates_SelectorNoPods(t *testing.T) {
	f := newFake().add(objJSON("v1", "Service", "web", "svc-web", `{"spec":{"selector":{"app":"web"},"ports":[{"port":80}]}}`))
	checkStates(t, "C10", "selector-no-pods", mustBuild(t, f),
		wantState{BridgeServicePod, "Service/web", testNS, "Pod", "", BridgeBroken, "selector app=web matches 0 Pods", ""})
}

// TestStates_NamedTargetPort (C11, S-3): a named targetPort needs a container port of
// that name.
func TestStates_NamedTargetPort(t *testing.T) {
	f := newFake().add(
		objJSON("v1", "Service", "api2", "svc-api2", `{"spec":{"selector":{"app":"api2"},"ports":[{"port":80,"targetPort":"http"}]}}`),
		objJSON("v1", "Pod", "api2-1", "p-api2", `{"labels":{"app":"api2"},"spec":{"containers":[{"name":"c","image":"pause","ports":[{"name":"web","containerPort":8080}]}]}}`),
		objJSON("v1", "Service", "api3", "svc-api3", `{"spec":{"selector":{"app":"api3"},"ports":[{"port":80,"targetPort":"http"}]}}`),
		objJSON("v1", "Pod", "api3-1", "p-api3", `{"labels":{"app":"api3"},"spec":{"containers":[{"name":"c","image":"pause","ports":[{"name":"http","containerPort":8080}]}]}}`),
	)
	checkStates(t, "C11", "named-target-port", mustBuild(t, f),
		wantState{BridgeServicePod, "Service/api2", testNS, "Pod", "api2-1", BridgeBroken, `targetPort "http": no container port with this name`, "p-api2"},
		wantState{BridgeServicePod, "Service/api3", testNS, "Pod", "api3-1", BridgeConnected, "", "p-api3"})
}

func paymentsFixture(f *fakeAPI) {
	f.add(objJSON("v1", "Pod", "pay", "p-pay", `{"spec":{"containers":[{"name":"c","image":"pause",
		"env":[{"name":"K","valueFrom":{"configMapKeyRef":{"name":"payments-config","key":"k"}}},
			{"name":"S","valueFrom":{"secretKeyRef":{"name":"payments-key","key":"s"}}}]}]}}`))
}

// TestStates_ConfigMissing (C12, C-2): a ConfigMap and a Secret that do not exist and are
// not optional are broken.
func TestStates_ConfigMissing(t *testing.T) {
	f := newFake()
	paymentsFixture(f)
	checkStates(t, "C12", "payments-missing", mustBuild(t, f),
		wantState{BridgePodConfigMap, "Pod/pay", testNS, "ConfigMap", "payments-config", BridgeBroken, "ConfigMap payments-config not found", ""},
		wantState{BridgePodSecret, "Pod/pay", testNS, "Secret", "payments-key", BridgeBroken, "Secret payments-key not found", ""})
}

// TestStates_StorageClassMissing (C13, K-2, K-3, V-7): a missing class breaks a Pending
// claim on both of its bridges; a bound claim and its PV only warn.
func TestStates_StorageClassMissing(t *testing.T) {
	f := newFake().add(objJSON("v1", "PersistentVolumeClaim", "data-f", "pvc-f", `{"spec":{"storageClassName":"fast"},"status":{"phase":"Pending"}}`))
	text := `StorageClass "fast" not found (claim cannot bind)`
	checkStates(t, "C13", "class-fast-missing", mustBuild(t, f),
		wantState{BridgePVCStorageClass, "PersistentVolumeClaim/data-f", "", "StorageClass", "fast", BridgeBroken, text, ""},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-f", "", "PersistentVolume", "", BridgeBroken, text, ""})
	f = newFake().add(
		clusterObject("v1", "PersistentVolume", "pv-g", "pv-g", `{"spec":{"storageClassName":"gone"}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-g", "pvc-g", `{"spec":{"storageClassName":"gone","volumeName":"pv-g"},"status":{"phase":"Bound"}}`),
	)
	a := mustBuild(t, f)
	bound := `StorageClass "gone" not found (volume already bound)`
	checkStates(t, "C13", "bound-class-gone", a,
		wantState{BridgePVCStorageClass, "PersistentVolumeClaim/data-g", "", "StorageClass", "gone", BridgeWarning, bound, ""},
		wantState{BridgePVStorageClass, "PersistentVolume/pv-g", "", "StorageClass", "gone", BridgeWarning, bound, ""},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-g", "", "PersistentVolume", "pv-g", BridgeConnected, "", "pv-g"})
	brokenZero(t, "C13", "bound-class-gone", a)
}

// TestStates_TrafficTargets (C14 as decided by T7a-C14-PORT, I-2, I-3, I-4, H-2, H-3, H-4):
// the I-3 and I-4 Ingress references are on two Ingress objects.
func TestStates_TrafficTargets(t *testing.T) {
	f := newFake()
	gateway(f, "v1")
	f.add(
		objJSON("v1", "Service", "api", "svc-api", `{"spec":{"ports":[{"name":"http","port":80}]}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "to-gone", "ing-gone", `{"spec":{"defaultBackend":{"service":{"name":"gone","port":{"number":80}}}}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "to-81", "ing-81", `{"spec":{"defaultBackend":{"service":{"name":"api","port":{"number":81}}}}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "to-metrics", "ing-metrics", `{"spec":{"defaultBackend":{"service":{"name":"api","port":{"name":"metrics"}}}}}`),
		route("v1", "r81", "rt-81", `{"spec":{"rules":[{"backendRefs":[{"name":"api","port":81}]}]}}`),
		objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "nope", "hpa-nope", `{"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"nope"},"maxReplicas":2}}`),
		objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "unserved", "hpa-unserved", `{"spec":{"scaleTargetRef":{"apiVersion":"foo.example/v1","kind":"Widget","name":"x"},"maxReplicas":2}}`),
		objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "rc", "hpa-rc", `{"spec":{"scaleTargetRef":{"apiVersion":"v1","kind":"ReplicationController","name":"r"},"maxReplicas":2}}`),
	)
	checkStates(t, "C14", "traffic-targets", mustBuild(t, f),
		wantState{BridgeIngressService, "Ingress/to-gone", testNS, "Service", "gone", BridgeBroken, "Service gone not found", ""},
		wantState{BridgeIngressService, "Ingress/to-81", testNS, "Service", "api", BridgeBroken, "Service api has no port 81", "svc-api"},
		wantState{BridgeIngressService, "Ingress/to-metrics", testNS, "Service", "api", BridgeBroken, `Service api has no port named "metrics"`, "svc-api"},
		wantState{BridgeHTTPRouteService, "HTTPRoute/r81", testNS, "Service", "api", BridgeBroken, "Service api has no port 81", "svc-api"},
		wantState{BridgeHPATarget, "HorizontalPodAutoscaler/nope", testNS, "Deployment", "nope", BridgeBroken, "Deployment nope not found", ""},
		wantState{BridgeHPATarget, "HorizontalPodAutoscaler/unserved", testNS, "Widget", "x", BridgeUnknown, "foo.example Widget is not served by this cluster", ""},
		wantState{BridgeHPATarget, "HorizontalPodAutoscaler/rc", testNS, "ReplicationController", "r", BridgeUnknown, "ReplicationController r is not read by intuitik", ""})
}

// refusalCase is one LIST of C15 with the objects that reference it and the bridge that
// must be unknown.
type refusalCase struct {
	key, kind string
	objects   []*unstructured.Unstructured
	want      wantState
}

func refusalCases() []refusalCase {
	return []refusalCase{
		{"/pods", "Pod", []*unstructured.Unstructured{objJSON("v1", "Service", "web", "svc-web", `{"spec":{"selector":{"app":"web"},"ports":[{"port":80}]}}`)},
			wantState{BridgeServicePod, "Service/web", testNS, "Pod", "", BridgeUnknown, "no permission to read Pods", ""}},
		{"/services", "Service", []*unstructured.Unstructured{objJSON("networking.k8s.io/v1", "Ingress", "i", "ing-i", `{"spec":{"defaultBackend":{"service":{"name":"api","port":{"number":80}}}}}`),
			objJSON("v1", "Service", "api", "svc-api", `{"spec":{"ports":[{"port":80}]}}`)},
			wantState{BridgeIngressService, "Ingress/i", testNS, "Service", "api", BridgeUnknown, "no permission to read Services", ""}},
		{"/persistentvolumeclaims", "PersistentVolumeClaim", []*unstructured.Unstructured{objJSON("v1", "Pod", "p", "p-p", `{"spec":{"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"data"}}]}}`)},
			wantState{BridgePodPVC, "Pod/p", testNS, "PersistentVolumeClaim", "data", BridgeUnknown, "no permission to read PersistentVolumeClaims", ""}},
		{"/persistentvolumes", "PersistentVolume", []*unstructured.Unstructured{objJSON("v1", "PersistentVolumeClaim", "data", "pvc-d", `{"spec":{"volumeName":"pv-d"},"status":{"phase":"Bound"}}`)},
			wantState{BridgePVCPV, "PersistentVolumeClaim/data", "", "PersistentVolume", "pv-d", BridgeUnknown, "no permission to read PersistentVolumes", ""}},
		{"storage.k8s.io/storageclasses", "StorageClass", []*unstructured.Unstructured{objJSON("v1", "PersistentVolumeClaim", "data", "pvc-d", `{"spec":{"storageClassName":"fast"},"status":{"phase":"Pending"}}`)},
			wantState{BridgePVCStorageClass, "PersistentVolumeClaim/data", "", "StorageClass", "fast", BridgeUnknown, "no permission to read StorageClasses", ""}},
		{"/configmaps", "ConfigMap", []*unstructured.Unstructured{objJSON("v1", "Pod", "p", "p-p", `{"spec":{"containers":[{"name":"c","envFrom":[{"configMapRef":{"name":"app"}}]}]}}`),
			obj("v1", "ConfigMap", "app", "cm-app")},
			wantState{BridgePodConfigMap, "Pod/p", testNS, "ConfigMap", "app", BridgeUnknown, "no permission to read ConfigMaps", ""}},
		{"/secrets", "Secret", []*unstructured.Unstructured{objJSON("v1", "Pod", "p", "p-p", `{"spec":{"containers":[{"name":"c","envFrom":[{"secretRef":{"name":"db"}}]}]}}`),
			obj("v1", "Secret", "db", "sec-db")},
			wantState{BridgePodSecret, "Pod/p", testNS, "Secret", "db", BridgeUnknown, "no permission to read Secrets", ""}},
		{"apps/deployments", "Deployment", []*unstructured.Unstructured{objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "h", "hpa-h", `{"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"api"},"maxReplicas":2}}`),
			obj("apps/v1", "Deployment", "api", "d-api")},
			wantState{BridgeHPATarget, "HorizontalPodAutoscaler/h", testNS, "Deployment", "api", BridgeUnknown, "no permission to read Deployments", ""}},
	}
}

// TestStates_RefusalNeverMissing (C15, B3): each LIST refused gives unknown with its text,
// never not found, and its refused entry; each LIST answered 500 or not at all fails the
// call with the 3.5 text.
func TestStates_RefusalNeverMissing(t *testing.T) {
	for _, c := range refusalCases() {
		t.Run(c.kind, func(t *testing.T) {
			f := newFake().add(c.objects...)
			f.lists[c.key] = fail{status: 403}
			a := mustBuild(t, f)
			checkStates(t, "C15", c.kind+"-list-403", a, c.want)
			notFound, refused := 0, ""
			for _, b := range a.Bridges {
				if strings.Contains(b.Text, "not found") {
					notFound++
				}
			}
			for _, r := range a.Refused {
				if r.Kind == c.kind {
					refused = r.Text
				}
			}
			wantRefused := fmt.Sprintf("Couldn't load %s (403)", Plural(c.kind))
			pass := notFound == 0 && refused == wantRefused
			t.Logf("FA2-T7a C15 input=%s-list-403 expected-refused=%s actual-refused=%s not-found-texts=%d result=%s", c.kind, wantRefused, refused, notFound, passFail(pass))
			if !pass {
				t.Errorf("C15 %s: refused %q, not found %d", c.kind, refused, notFound)
			}
			for _, ans := range []struct {
				name   string
				answer fail
				reason string
			}{{"500", fail{status: 500}, "500"}, {"no-answer", fail{noAnswer: true}, NoAnswer}} {
				f := newFake().add(c.objects...)
				f.lists[c.key] = ans.answer
				a, err := f.build(t)
				var le *LoadError
				got := ""
				if errors.As(err, &le) {
					got = le.Error()
				}
				want := fmt.Sprintf("Couldn't load %s in %s (%s)", Plural(c.kind), testNS, ans.reason)
				pass := got == want && len(a.Nodes) == 0
				t.Logf("FA2-T7a C15 input=%s-list-%s expected=error-no-nodes actual=nodes=%d expected-text=%s actual-text=%s result=%s", c.kind, ans.name, len(a.Nodes), want, got, passFail(pass))
				if !pass {
					t.Errorf("C15 %s %s: error %v, %d nodes; want %q", c.kind, ans.name, err, len(a.Nodes), want)
				}
			}
		})
	}
}

// TestStates_FA2T6Fixtures (C17): every FA2-T6 unit fixture bridge has the state and text
// that 3.2 gives it.
func TestStates_FA2T6Fixtures(t *testing.T) {
	f := newFake()
	trafficFixture(f)
	storageFixture(f)
	configFixture(f)
	hpaFixture(f)
	gateway(f, "v1")
	routeFixture(f, "v1")
	a := mustBuild(t, f)
	warn := "targetPort 8080 is not declared by the container (traffic not verifiable)"
	checkStates(t, "C17", "fa2-t6-fixtures", a,
		wantState{BridgeIngressService, "Ingress/shop", testNS, "Service", "api", BridgeBroken, `Service api has no port named "http"`, "svc-api"},
		wantState{BridgeIngressService, "Ingress/plain", testNS, "Service", "api", BridgeConnected, "", "svc-api"},
		wantState{BridgeServicePod, "Service/api", testNS, "Pod", "api-1", BridgeWarning, warn, "p-api-1"},
		wantState{BridgeServicePod, "Service/api", testNS, "Pod", "api-2", BridgeWarning, warn, "p-api-2"},
		wantState{BridgePodPVC, "Pod/db-0", testNS, "PersistentVolumeClaim", "data-db-0", BridgeConnected, "", "pvc-data"},
		wantState{BridgePVCPV, "PersistentVolumeClaim/data-db-0", "", "PersistentVolume", "pv-1", BridgeBroken, "PersistentVolume pv-1 not found", ""},
		wantState{BridgePodSecret, "Pod/api", testNS, "Secret", "db", BridgeConnected, "", "sec-db"},
		wantState{BridgePodConfigMap, "Pod/api", testNS, "ConfigMap", "app", BridgeConnected, "", "cm-app"},
		wantState{BridgePodSecret, "Pod/api", testNS, "Secret", "tls", BridgeConnected, "", "sec-tls"},
		wantState{BridgeHPATarget, "HorizontalPodAutoscaler/api", testNS, "Deployment", "api", BridgeConnected, "", "d-api"},
		wantState{BridgeHTTPRouteService, "HTTPRoute/shop", testNS, "Service", "checkout", BridgeConnected, "", "svc-checkout"},
		wantState{BridgeHTTPRouteService, "HTTPRoute/open", testNS, "Service", "checkout", BridgeConnected, "", "svc-checkout"})
	f = newFake()
	gateway(f, "v1")
	f.add(route("v1", "lost", "rt-lost", `{"spec":{"rules":[{"backendRefs":[{"name":"gone","port":8080}]}]}}`),
		route("v1", "cross", "rt-cross", `{"spec":{"rules":[{"backendRefs":[{"namespace":"other","name":"checkout","port":8080}]}]}}`))
	checkStates(t, "C17", "fa2-t6-route-cases", mustBuild(t, f),
		wantState{BridgeHTTPRouteService, "HTTPRoute/lost", testNS, "Service", "gone", BridgeBroken, "Service gone not found", ""},
		wantState{BridgeHTTPRouteService, "HTTPRoute/cross", "other", "Service", "checkout", BridgeUnknown, "Service other/checkout is in another namespace (not checked)", ""})
	f = newFake()
	gateway(f, "v1")
	routeFixture(f, "v1")
	f.lists["/services"] = fail{status: 403}
	checkStates(t, "C17", "fa2-t6-services-403", mustBuild(t, f),
		wantState{BridgeHTTPRouteService, "HTTPRoute/open", testNS, "Service", "checkout", BridgeUnknown, "no permission to read Services", ""})
}

// TestStates_ListFailureText (C18, 3.5): a failed LIST of each FA2-T5 kind names its kind;
// a failed owner GET keeps the owners text.
func TestStates_ListFailureText(t *testing.T) {
	for _, k := range builtInKinds {
		for _, ans := range []struct {
			name   string
			answer fail
			reason string
		}{{"500", fail{status: 500}, "500"}, {"no-answer", fail{noAnswer: true}, NoAnswer}} {
			f := newFake()
			f.lists[k.res.Group+"/"+k.res.Resource] = ans.answer
			_, err := f.build(t)
			var le *LoadError
			got := ""
			if errors.As(err, &le) {
				got = le.Error()
			}
			want := fmt.Sprintf("Couldn't load %s in %s (%s)", Plural(k.kind), testNS, ans.reason)
			t.Logf("FA2-T7a C18 input=%s-list-%s expected-text=%s actual-text=%s result=%s", k.kind, ans.name, want, got, passFail(got == want))
			if got != want {
				t.Errorf("C18 %s %s: %q, want %q", k.kind, ans.name, got, want)
			}
		}
	}
	f := newFake().add(obj("apps/v1", "ReplicaSet", "r", "rs-r"), obj("v1", "Pod", "p", "p-p", ctl("apps/v1", "ReplicaSet", "r", "rs-r")))
	f.gets["ReplicaSet/r"] = fail{status: 500}
	_, err := f.build(t)
	var le *LoadError
	got := ""
	if errors.As(err, &le) {
		got = le.Error()
	}
	want := "Couldn't load owners in " + testNS + " (500)"
	t.Logf("FA2-T7a C18 input=owner-get-500 expected-text=%s actual-text=%s result=%s", want, got, passFail(got == want))
	if got != want {
		t.Errorf("C18 owner GET: %q, want %q", got, want)
	}
}

// TestStates_MetadataOnly (C19 unit, B1, B2): ConfigMaps and Secrets are read only as
// metadata (LISTMETA), never by a full LIST or a GET of one object, and no value reaches
// the answer (positive control in the fixture).
func TestStates_MetadataOnly(t *testing.T) {
	f := newFake()
	canaries := configFixture(f)
	paymentsFixture(f)
	a := mustBuild(t, f)
	var reads []string
	for _, r := range f.requests {
		if strings.Contains(r, "/v1/configmaps") || strings.Contains(r, "/v1/secrets") {
			reads = append(reads, r)
		}
	}
	want := []string{"LISTMETA /v1/configmaps " + testNS, "LISTMETA /v1/secrets " + testNS}
	t.Logf("FA2-T7a C19 input=config-and-payments reads of ConfigMaps and Secrets expected=%q actual=%q result=%s", want, reads, passFail(reflect.DeepEqual(reads, want)))
	if !reflect.DeepEqual(reads, want) {
		t.Errorf("C19: reads %q, want %q", reads, want)
	}
	body, _ := json.Marshal(a)
	fixtureBody, _ := json.Marshal(f.objects)
	for i, c := range canaries {
		found, control := strings.Count(string(body), c), strings.Count(string(fixtureBody), c)
		t.Logf("FA2-T7a C19 input=config-and-payments canary=%d in the answer %d, positive control in the fixture %d (want 0, at least 1) result=%s", i+1, found, control, passFail(found == 0 && control > 0))
		if found != 0 || control == 0 {
			t.Errorf("C19 canary %d: answer %d, fixture %d", i+1, found, control)
		}
	}
}

// statesFixture holds the objects of C6 to C14.
func statesFixture() *fakeAPI {
	f := waitingFixture()
	gateway(f, "v1")
	paymentsFixture(f)
	f.add(
		objJSON("v1", "Service", "num", "svc-num", `{"spec":{"selector":{"app":"num"},"ports":[{"port":9000,"targetPort":9000}]}}`),
		objJSON("v1", "Pod", "num-1", "p-num", `{"labels":{"app":"num"},"spec":{"containers":[{"name":"c","ports":[{"containerPort":8080}]}]}}`),
		objJSON("v1", "Service", "web", "svc-web", `{"spec":{"selector":{"app":"web"},"ports":[{"port":80}]}}`),
		objJSON("v1", "Service", "api", "svc-api", `{"spec":{"ports":[{"name":"http","port":80}]}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "to-81", "ing-81", `{"spec":{"defaultBackend":{"service":{"name":"api","port":{"number":81}}}}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "to-metrics", "ing-metrics", `{"spec":{"defaultBackend":{"service":{"name":"api","port":{"name":"metrics"}}}}}`),
		route("v1", "r81", "rt-81", `{"spec":{"rules":[{"backendRefs":[{"name":"api","port":81}]}]}}`),
		objJSON("v1", "PersistentVolumeClaim", "data-f", "pvc-f", `{"spec":{"storageClassName":"fast"},"status":{"phase":"Pending"}}`),
	)
	return f
}

// TestStates_Stable (C20): the same objects in two LIST orders give byte-identical JSON,
// for a namespace answer and for the cluster answer.
func TestStates_Stable(t *testing.T) {
	first := mustBuild(t, statesFixture())
	f := statesFixture()
	f.reversed = true
	second := mustBuild(t, f)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	t.Logf("FA2-T7a C20 input=c6-to-c14-two-orders answer bytes %d and %d, bridges %d, identical %v result=%s", len(a), len(b), len(first.Bridges), string(a) == string(b), passFail(string(a) == string(b)))
	if string(a) != string(b) {
		t.Errorf("C20: answers differ:\n%s\n%s", a, b)
	}
	c1, _ := buildCluster(t, clusterFixture(), "")
	fc := clusterFixture()
	fc.reversed = true
	c2, _ := buildCluster(t, fc, "")
	t.Logf("FA2-T7a C20 input=cluster-two-orders %s / %s identical %v result=%s", clusterJSON(c1), clusterJSON(c2), clusterJSON(c1) == clusterJSON(c2), passFail(clusterJSON(c1) == clusterJSON(c2)))
	if clusterJSON(c1) != clusterJSON(c2) {
		t.Errorf("C20 cluster: %s / %s", clusterJSON(c1), clusterJSON(c2))
	}
}

// endingReader makes the Namespace GET cancel the call context and return err.
type endingReader struct {
	Reader
	cancel func()
	err    error
}

func (r endingReader) Object(ctx context.Context, res readonly.Resource, namespace, name string) (*unstructured.Unstructured, error) {
	if res.Resource == "namespaces" {
		r.cancel()
		return nil, r.err
	}
	return r.Reader.Object(ctx, res, namespace, name)
}

// TestNamespaceGet_Ending (6.9): the Namespace GET answered 404 gives not-found; 403 and
// 500 leave existenceChecked false with the answer built; a stopped call, a cancelled and
// an expired call context end the call with no answer.
func TestNamespaceGet_Ending(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer fail
		want   string
	}{{"404", fail{status: 404}, "not-found existenceChecked=true"}, {"403", fail{status: 403}, "empty existenceChecked=false"}, {"500", fail{status: 500}, "empty existenceChecked=false"}} {
		f := newFake()
		f.gets["Namespace/"+testNS] = c.answer
		a, err := f.build(t)
		checkNamespace(t, "6.9", "namespace-get-"+c.name, a, err, c.want)
	}
	probe := newFake()
	mustBuild(t, probe)
	at := 0
	for i, r := range probe.requests {
		if r == "GET /v1/namespaces /"+testNS {
			at = i + 1
		}
	}
	f := newFake()
	f.stopAfter = at
	a, err := f.build(t)
	pass := at > 0 && errors.Is(err, ErrStopped) && len(a.Nodes) == 0
	t.Logf("FA2-T7a 6.9 input=stopped-at-namespace-get open=%d expected=ErrStopped no-answer actual=error=%v nodes=%d result=%s", at, err, len(a.Nodes), passFail(pass))
	if !pass {
		t.Errorf("stopped at the Namespace GET: open %d, error %v, nodes %d", at, err, len(a.Nodes))
	}
	for _, c := range []struct {
		name string
		err  error
	}{{"canceled", context.Canceled}, {"deadline", context.DeadlineExceeded}} {
		f := newFake()
		ctx, cancel := context.WithTimeout(context.Background(), contextLimit)
		open := func(octx context.Context) (Reader, error) {
			r, err := f.open(octx)
			if err != nil {
				return nil, err
			}
			return endingReader{Reader: r, cancel: cancel, err: c.err}, nil
		}
		a, err := Build(ctx, open, testNS)
		cancel()
		pass := err != nil && len(a.Nodes) == 0 && a.Namespace == ""
		t.Logf("FA2-T7a 6.9 input=%s-at-namespace-get expected=error no-answer actual=error=%v nodes=%d result=%s", c.name, err, len(a.Nodes), passFail(pass))
		if !pass {
			t.Errorf("%s at the Namespace GET: error %v, %d nodes; want an error and no answer", c.name, err, len(a.Nodes))
		}
	}
}
