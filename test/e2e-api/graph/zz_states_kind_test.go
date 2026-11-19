//go:build kind

package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// Kind tests of packet FA2-T7a (job e2e-graph): the state of every bridge, the namespace
// states and the cluster answer, on the release binary through the routes the tab uses.
// TestG_States runs after TestG_Bridges (file order, no parallel run), so the Gateway API
// CRDs are served. Each checked bridge prints one line:
//   FA2-T7a <criterion> input=<fixture> kind=<bridge kind> from=<kind/name> to=<ns/kind/name uid|null> state=<expected>/<actual> expected-text=<...> actual-text=<...> [expected-uid=<...>] result=<PASS|FAIL>
// and each case a count line:
//   FA2-T7a <criterion> input=<fixture> counts connected=<n> waiting=<n> broken=<n> warning=<n> unknown=<n> optional=<n>
// The expected strings are written here independently of the product (packet section 3).

const (
	nsStates = "graph-states"    // C7, C8, C10 to C14
	nsWait   = "graph-st-wait"   // C6, C16
	nsEmpty  = "graph-st-empty"  // C4
	nsLocked = "graph-st-locked" // C5
	nsView   = "graph-st-view"   // C9, C15 (Secrets), C19
	nsPV     = "graph-st-pv"     // C15 (PersistentVolumes)
)

// stateAnswer is the namespace answer with the fields of FA2-T7a appendix F.
type stateAnswer struct {
	Namespace      string `json:"namespace"`
	NamespaceState struct {
		State            string `json:"state"`
		ExistenceChecked bool   `json:"existenceChecked"`
	} `json:"namespaceState"`
	Nodes   []node `json:"nodes"`
	Refused []struct {
		Kind   string `json:"kind"`
		Status int    `json:"status"`
		Text   string `json:"text"`
	} `json:"refused"`
	Bridges []struct {
		Kind string `json:"kind"`
		From struct {
			UID string `json:"uid"`
		} `json:"from"`
		To struct {
			Group     string  `json:"group"`
			Namespace string  `json:"namespace"`
			Kind      string  `json:"kind"`
			Name      string  `json:"name"`
			UID       *string `json:"uid"`
		} `json:"to"`
		State string `json:"state"`
		Text  string `json:"text"`
	} `json:"bridges"`
}

func (a stateAnswer) nodeName(uid string) string {
	for _, n := range a.Nodes {
		if n.UID == uid {
			return n.Kind + "/" + n.Name
		}
	}
	return "uid:" + uid
}

func (a stateAnswer) refusedTexts() []string {
	var out []string
	for _, r := range a.Refused {
		out = append(out, r.Text)
	}
	return out
}

// stateCall is one namespace call of these tests.
type stateCall struct {
	status int
	ans    stateAnswer
	raw    []byte
	events []auditEvent
}

// statesCall sends one namespace call for ns and checks its audit window (G7).
func statesCall(t *testing.T, p *panel, label, ns string) stateCall {
	t.Helper()
	a := auditStart(t)
	start := time.Now()
	status, raw := p.request("GET", "/api/graph/containment?namespace="+ns, nil, true)
	end := time.Now()
	g7(t, label, p, a, start, end, ns, nil)
	c := stateCall{status: status, raw: raw, events: a.events(t, start, end)}
	json.Unmarshal(raw, &c.ans)
	t.Logf("FA2-T7a call input=%s namespace=%s time=%s status=%d namespaceState=%s existenceChecked=%v nodes=%d bridges=%d refused=%q",
		label, ns, end.Format(time.RFC3339Nano), status, c.ans.NamespaceState.State, c.ans.NamespaceState.ExistenceChecked, len(c.ans.Nodes), len(c.ans.Bridges), c.ans.refusedTexts())
	return c
}

// wantS is the expected state and text of one bridge; uid "-" is not checked, "" expects
// null.
type wantS struct {
	kind, from, ns, toKind, toName string
	state, text, uid               string
}

func checkS(t *testing.T, criterion, input string, a stateAnswer, ws ...wantS) {
	t.Helper()
	for _, w := range ws {
		var got []int
		for i, b := range a.Bridges {
			if b.Kind == w.kind && a.nodeName(b.From.UID) == w.from && b.To.Namespace == w.ns && b.To.Kind == w.toKind && b.To.Name == w.toName {
				got = append(got, i)
			}
		}
		state, text, uid := "absent", "", "-"
		if len(got) > 1 {
			state = fmt.Sprintf("%d bridges", len(got))
		}
		if len(got) == 1 {
			b := a.Bridges[got[0]]
			state, text, uid = b.State, b.Text, uidOrNull(b.To.UID)
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
		t.Logf("%s result=%s", line, result(pass))
		if !pass {
			t.Errorf("FA2-T7a %s %s %s -> %s/%s/%s: state %s text %q uid %s (%d bridges); want %s %q uid %s",
				criterion, w.kind, w.from, w.ns, w.toKind, w.toName, state, text, uid, len(got), w.state, w.text, wantUID)
		}
	}
}

// countLine prints the number of bridges per state and returns the broken count.
func countLine(t *testing.T, criterion, input string, a stateAnswer) int {
	t.Helper()
	n := map[string]int{}
	for _, b := range a.Bridges {
		n[b.State]++
	}
	t.Logf("FA2-T7a %s input=%s counts connected=%d waiting=%d broken=%d warning=%d unknown=%d optional=%d",
		criterion, input, n["connected"], n["waiting"], n["broken"], n["warning"], n["unknown"], n["optional"])
	return n["broken"]
}

func notFoundTexts(a stateAnswer) int {
	n := 0
	for _, b := range a.Bridges {
		if strings.Contains(b.Text, "not found") {
			n++
		}
	}
	return n
}

// clusterAnswerK is the cluster answer (FA2-T7a 6.5).
type clusterAnswerK struct {
	Namespaces struct {
		Listed bool     `json:"listed"`
		Items  []string `json:"items"`
		From   string   `json:"from"`
	} `json:"namespaces"`
	Band struct {
		Nodes *struct {
			Total int `json:"total"`
			Ready int `json:"ready"`
		} `json:"nodes"`
		StorageClasses *[]struct {
			Name    string `json:"name"`
			Default bool   `json:"default"`
		} `json:"storageClasses"`
		PersistentVolumes *struct {
			Total int `json:"total"`
		} `json:"persistentVolumes"`
	} `json:"band"`
	Refused []struct {
		Kind   string `json:"kind"`
		Status int    `json:"status"`
		Text   string `json:"text"`
	} `json:"refused"`
}

// clusterCall sends one cluster call and prints its audit window (verbs and resources).
func clusterCall(t *testing.T, p *panel, label string) (int, clusterAnswerK, []byte) {
	t.Helper()
	a := auditStart(t)
	start := time.Now()
	status, raw := p.request("GET", "/api/graph/cluster", nil, true)
	end := time.Now()
	time.Sleep(time.Second)
	per := map[string]int{}
	for _, e := range a.events(t, start, end) {
		res := "nonresource " + e.RequestURI
		if e.ObjectRef != nil {
			res = e.ObjectRef.APIGroup + "/" + e.ObjectRef.Resource
		}
		code := 0
		if e.ResponseStatus != nil {
			code = e.ResponseStatus.Code
		}
		per[fmt.Sprintf("%s %s %d", e.Verb, res, code)]++
	}
	var ans clusterAnswerK
	json.Unmarshal(raw, &ans)
	t.Logf("FA2-T7a cluster-call input=%s status=%d audit per-verb-resource=%v body=%s", label, status, per, raw)
	return status, ans, raw
}

// kubeconfigNS is kubeconfig with a context namespace.
func kubeconfigNS(t *testing.T, ctxName, server, caData, namespace string, user map[string]any) string {
	t.Helper()
	ctx := map[string]any{"cluster": ctxName + "-cluster", "user": ctxName + "-user"}
	if namespace != "" {
		ctx["namespace"] = namespace
	}
	doc := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": ctxName,
		"clusters": []any{map[string]any{"name": ctxName + "-cluster", "cluster": map[string]any{"server": server, "certificate-authority-data": caData}}},
		"users":    []any{map[string]any{"name": ctxName + "-user", "user": user}},
		"contexts": []any{map[string]any{"name": ctxName, "context": ctx}}}
	raw, _ := json.Marshal(doc)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// saPanelNS starts the release binary as ServiceAccount sa of saNS with context namespace
// contextNS ("" for none).
func saPanelNS(t *testing.T, saNS, sa, contextNS string) *panel {
	t.Helper()
	ki := kindCluster(t)
	token := strings.TrimSpace(string(kubectl(t, nil, "create", "token", sa, "-n", saNS, "--duration", "1h")))
	p := startPanel(t, kubeconfigNS(t, "sa-"+sa, ki.server, ki.caData, contextNS, map[string]any{"token": token}), "system:serviceaccount:"+saNS+":"+sa)
	if o := p.settle(); o.State != "connected" {
		t.Fatalf("preparation: the ServiceAccount %s did not connect: %s %q", sa, o.State, str(o.Message))
	}
	return p
}

// clusterRole binds sa of ns to a new ClusterRole with list on resources (core group and
// storage.k8s.io).
func clusterRole(t *testing.T, ns, sa string, resources ...string) {
	t.Helper()
	name := "intuitik-" + sa
	var core, storage []any
	for _, r := range resources {
		if r == "storageclasses" {
			storage = append(storage, r)
		} else {
			core = append(core, r)
		}
	}
	rules := []any{}
	if len(core) > 0 {
		rules = append(rules, map[string]any{"apiGroups": []any{""}, "resources": core, "verbs": []any{"get", "list"}})
	}
	if len(storage) > 0 {
		rules = append(rules, map[string]any{"apiGroups": []any{"storage.k8s.io"}, "resources": storage, "verbs": []any{"get", "list"}})
	}
	role, _ := json.Marshal(map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": name}, "rules": rules})
	kubectl(t, role, "create", "-f", "-")
	kubectl(t, nil, "create", "clusterrolebinding", name, "--clusterrole="+name, "--serviceaccount="+ns+":"+sa)
}

// TestG_States (C1 to C17 and C19 on kind).
func TestG_States(t *testing.T) {
	kindCluster(t)
	for _, ns := range []string{nsStates, nsWait, nsEmpty, nsLocked, nsView, nsPV} {
		kubectl(t, nil, "create", "namespace", ns)
	}
	admin := adminPanel(t)

	// Cluster answer (C1 to C3).
	kubectl(t, nil, "create", "serviceaccount", "cluster-reader", "-n", nsStates)
	clusterRole(t, nsStates, "cluster-reader", "namespaces", "nodes", "storageclasses", "persistentvolumes")
	kubectl(t, nil, "create", "serviceaccount", "no-ns-list", "-n", nsStates)
	clusterRole(t, nsStates, "no-ns-list", "nodes", "storageclasses", "persistentvolumes")
	kubectl(t, nil, "create", "serviceaccount", "no-nodes", "-n", nsStates)
	clusterRole(t, nsStates, "no-nodes", "namespaces", "storageclasses", "persistentvolumes")
	t.Run("C1", func(t *testing.T) {
		p := saPanelNS(t, nsStates, "cluster-reader", "")
		status, ans, _ := clusterCall(t, p, "cluster-reader")
		names := strings.Fields(string(kubectl(t, nil, "get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}")))
		sort.Strings(names)
		var nodes struct {
			Items []struct {
				Status struct {
					Conditions []struct{ Type, Status string } `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		json.Unmarshal(kubectl(t, nil, "get", "nodes", "-o", "json"), &nodes)
		ready := 0
		for _, n := range nodes.Items {
			for _, c := range n.Status.Conditions {
				if c.Type == "Ready" && c.Status == "True" {
					ready++
				}
			}
		}
		pvs := len(strings.Fields(string(kubectl(t, nil, "get", "pv", "-o", "jsonpath={.items[*].metadata.name}"))))
		standardDefault := "absent"
		if ans.Band.StorageClasses != nil {
			for _, sc := range *ans.Band.StorageClasses {
				if sc.Name == "standard" {
					standardDefault = fmt.Sprint(sc.Default)
				}
			}
		}
		gotNodes, gotPV := "absent", "absent"
		if ans.Band.Nodes != nil {
			gotNodes = fmt.Sprintf("%d/%d", ans.Band.Nodes.Total, ans.Band.Nodes.Ready)
		}
		if ans.Band.PersistentVolumes != nil {
			gotPV = fmt.Sprint(ans.Band.PersistentVolumes.Total)
		}
		wantNodes := fmt.Sprintf("%d/%d", len(nodes.Items), ready)
		pass := status == 200 && ans.Namespaces.Listed && reflect.DeepEqual(ans.Namespaces.Items, names) && gotNodes == wantNodes && standardDefault == "true" && gotPV == fmt.Sprint(pvs)
		t.Logf("FA2-T7a C1 input=cluster-reader expected=listed items=%v nodes=%s standard-default=true pv=%d actual=listed=%v items=%v nodes=%s standard-default=%s pv=%s result=%s",
			names, wantNodes, pvs, ans.Namespaces.Listed, ans.Namespaces.Items, gotNodes, standardDefault, gotPV, result(pass))
		if !pass {
			t.Errorf("C1: status %d, namespaces %v %v, nodes %s, standard default %s, pv %s", status, ans.Namespaces.Listed, ans.Namespaces.Items, gotNodes, standardDefault, gotPV)
		}
	})
	t.Run("C2", func(t *testing.T) {
		if canI(t, nsStates, "no-ns-list", "list", "namespaces") != "no" {
			t.Fatal("preparation: the ServiceAccount no-ns-list may list namespaces")
		}
		for _, c := range []struct{ contextNS, items, from string }{{"shop", "shop", "kubeconfig"}, {"", "default", "default"}} {
			p := saPanelNS(t, nsStates, "no-ns-list", c.contextNS)
			status, ans, _ := clusterCall(t, p, "no-ns-list context namespace "+c.contextNS)
			refused := ""
			for _, r := range ans.Refused {
				if r.Kind == "Namespace" {
					refused = fmt.Sprintf("%d %s", r.Status, r.Text)
				}
			}
			pass := status == 200 && !ans.Namespaces.Listed && strings.Join(ans.Namespaces.Items, ",") == c.items && ans.Namespaces.From == c.from && refused == "403 Couldn't load Namespaces (403)"
			t.Logf("FA2-T7a C2 input=namespaces-403 context-namespace=%q expected=listed=false items=[%s] from=%s refused=403 Couldn't load Namespaces (403) actual=listed=%v items=%v from=%s refused=%s result=%s",
				c.contextNS, c.items, c.from, ans.Namespaces.Listed, ans.Namespaces.Items, ans.Namespaces.From, refused, result(pass))
			if !pass {
				t.Errorf("C2 %q: %d %+v refused %q", c.contextNS, status, ans.Namespaces, refused)
			}
		}
	})
	t.Run("C3", func(t *testing.T) {
		if canI(t, nsStates, "no-nodes", "list", "nodes") != "no" {
			t.Fatal("preparation: the ServiceAccount no-nodes may list nodes")
		}
		p := saPanelNS(t, nsStates, "no-nodes", "")
		status, ans, raw := clusterCall(t, p, "no-nodes")
		var band map[string]any
		var v map[string]json.RawMessage
		json.Unmarshal(raw, &v)
		json.Unmarshal(v["band"], &band)
		_, hasNodes := band["nodes"]
		refused := ""
		for _, r := range ans.Refused {
			if r.Kind == "Node" {
				refused = fmt.Sprintf("%d %s", r.Status, r.Text)
			}
		}
		pass := status == 200 && !hasNodes && refused == "403 Couldn't load Nodes (403)"
		t.Logf("FA2-T7a C3 input=nodes-403 expected=no-nodes-part refused=403 Couldn't load Nodes (403) actual=nodes-present=%v refused=%s result=%s", hasNodes, refused, result(pass))
		if !pass {
			t.Errorf("C3: status %d, band %v, refused %q", status, band, refused)
		}
	})

	// Namespace states (C4, C5).
	t.Run("C4", func(t *testing.T) {
		waitFor(t, 60*time.Second, "ConfigMap kube-root-ca.crt in "+nsEmpty, func() bool {
			_, err := kubectlErr(nil, "get", "configmap", "kube-root-ca.crt", "-n", nsEmpty)
			return err == nil
		})
		c := statesCall(t, admin, "admin "+nsEmpty, nsEmpty)
		got := fmt.Sprintf("%s existenceChecked=%v", c.ans.NamespaceState.State, c.ans.NamespaceState.ExistenceChecked)
		t.Logf("FA2-T7a C4 input=empty-namespace expected=empty existenceChecked=true actual=%s result=%s", got, result(got == "empty existenceChecked=true"))
		if got != "empty existenceChecked=true" {
			t.Errorf("C4 empty: %s", got)
		}
		createJSON(t, nsEmpty, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x"}}`, 0)
		c = statesCall(t, admin, "admin "+nsEmpty+" with ConfigMap x", nsEmpty)
		got = fmt.Sprintf("%s existenceChecked=%v", c.ans.NamespaceState.State, c.ans.NamespaceState.ExistenceChecked)
		t.Logf("FA2-T7a C4 input=configmap-x expected=ok existenceChecked=true actual=%s result=%s", got, result(got == "ok existenceChecked=true"))
		if got != "ok existenceChecked=true" {
			t.Errorf("C4 with x: %s", got)
		}
	})
	t.Run("C5", func(t *testing.T) {
		c := statesCall(t, admin, "admin shpo", "shpo")
		got := fmt.Sprintf("%s existenceChecked=%v", c.ans.NamespaceState.State, c.ans.NamespaceState.ExistenceChecked)
		t.Logf("FA2-T7a C5 input=namespace-shpo expected=not-found existenceChecked=true actual=%s result=%s", got, result(got == "not-found existenceChecked=true"))
		if got != "not-found existenceChecked=true" {
			t.Errorf("C5 not found: %s", got)
		}
		kubectl(t, nil, "create", "serviceaccount", "locked-sa", "-n", nsLocked)
		if canI(t, nsLocked, "locked-sa", "list", "pods") != "no" {
			t.Fatal("preparation: the ServiceAccount locked-sa may list pods")
		}
		p := saPanelNS(t, nsLocked, "locked-sa", "")
		c = statesCall(t, p, "locked-sa "+nsLocked, nsLocked)
		got = c.ans.NamespaceState.State
		t.Logf("FA2-T7a C5 input=locked-namespace expected=no-permission actual=%s existenceChecked=%v refused=%d result=%s", got, c.ans.NamespaceState.ExistenceChecked, len(c.ans.Refused), result(got == "no-permission"))
		if got != "no-permission" {
			t.Errorf("C5 locked: %s", got)
		}
	})

	// States in the main namespace (C7, C8, C10 to C14).
	kubectl(t, nil, "create", "-n", nsStates, "-f", "../../fixtures/states/main.yaml")
	if d := gatewayDiscovery(t); !strings.Contains(d, "v1") {
		t.Fatalf("preparation: the Gateway API group is %s; TestG_Bridges installs it before this test", d)
	}
	createJSON(t, nsStates, `{"apiVersion":"gateway.networking.k8s.io/v1","kind":"HTTPRoute","metadata":{"name":"r81"},"spec":{"rules":[{"backendRefs":[{"name":"api","port":81}]}]}}`, 60*time.Second)
	settled(t, nsStates)
	m := statesCall(t, admin, "admin "+nsStates, nsStates)
	pod := func(name string) string { return rawUID(t, "/api/v1/namespaces/"+nsStates+"/pods/"+name) }
	apiUID := rawUID(t, "/api/v1/namespaces/"+nsStates+"/services/api")
	t.Run("C7", func(t *testing.T) {
		checkS(t, "C7", "numeric-target-port", m.ans,
			wantS{"service-pod", "Service/num", nsStates, "Pod", "num-1", "warning", "targetPort 9000 is not declared by the container (traffic not verifiable)", pod("num-1")},
			wantS{"service-pod", "Service/num-ok", nsStates, "Pod", "ok-1", "connected", "", pod("ok-1")})
	})
	t.Run("C8", func(t *testing.T) {
		checkS(t, "C8", "optional-missing", m.ans,
			wantS{"pod-configmap", "Pod/opt", nsStates, "ConfigMap", "feature-flags", "optional", "ConfigMap feature-flags not found", ""},
			wantS{"pod-secret", "Pod/opt", nsStates, "Secret", "maybe", "optional", "Secret maybe not found", ""})
	})
	t.Run("C10", func(t *testing.T) {
		checkS(t, "C10", "selector-no-pods", m.ans,
			wantS{"service-pod", "Service/web", nsStates, "Pod", "", "broken", "selector app=web matches 0 Pods", ""})
	})
	t.Run("C11", func(t *testing.T) {
		checkS(t, "C11", "named-target-port", m.ans,
			wantS{"service-pod", "Service/api2", nsStates, "Pod", "api2-1", "broken", `targetPort "http": no container port with this name`, pod("api2-1")},
			wantS{"service-pod", "Service/api3", nsStates, "Pod", "api3-1", "connected", "", pod("api3-1")})
	})
	t.Run("C12", func(t *testing.T) {
		checkS(t, "C12", "payments-missing", m.ans,
			wantS{"pod-configmap", "Pod/pay", nsStates, "ConfigMap", "payments-config", "broken", "ConfigMap payments-config not found", ""},
			wantS{"pod-secret", "Pod/pay", nsStates, "Secret", "payments-key", "broken", "Secret payments-key not found", ""})
	})
	t.Run("C13", func(t *testing.T) {
		text := `StorageClass "fast" not found (claim cannot bind)`
		checkS(t, "C13", "class-fast-missing", m.ans,
			wantS{"pvc-storageclass", "PersistentVolumeClaim/data-f", "", "StorageClass", "fast", "broken", text, ""},
			wantS{"pvc-pv", "PersistentVolumeClaim/data-f", "", "PersistentVolume", "", "broken", text, ""})
	})
	t.Run("C14", func(t *testing.T) {
		checkS(t, "C14", "traffic-targets", m.ans,
			wantS{"ingress-service", "Ingress/to-gone", nsStates, "Service", "gone", "broken", "Service gone not found", ""},
			wantS{"ingress-service", "Ingress/to-81", nsStates, "Service", "api", "broken", "Service api has no port 81", apiUID},
			wantS{"ingress-service", "Ingress/to-metrics", nsStates, "Service", "api", "broken", `Service api has no port named "metrics"`, apiUID},
			wantS{"httproute-service", "HTTPRoute/r81", nsStates, "Service", "api", "broken", "Service api has no port 81", apiUID},
			wantS{"hpa-target", "HorizontalPodAutoscaler/nope", nsStates, "Deployment", "nope", "broken", "Deployment nope not found", ""})
	})
	countLine(t, "C7-C14", "admin "+nsStates, m.ans)

	// Waiting then connected (C6) and the healthy namespaces (C16).
	mode := strings.TrimSpace(string(kubectl(t, nil, "get", "storageclass", "standard", "-o", "jsonpath={.volumeBindingMode}")))
	t.Logf("FA2-T7a C6 StorageClass standard volumeBindingMode %s", mode)
	class := "standard"
	if mode != "WaitForFirstConsumer" {
		if _, err := kubectlErr([]byte(`{"apiVersion":"storage.k8s.io/v1","kind":"StorageClass","metadata":{"name":"wffc"},"provisioner":"rancher.io/local-path","volumeBindingMode":"WaitForFirstConsumer"}`), "create", "-f", "-"); err != nil {
			t.Fatalf("STOP-4 (section 8): standard is not WaitForFirstConsumer and wffc cannot be created: %v", err)
		}
		class = "wffc"
	}
	createJSON(t, nsWait, fmt.Sprintf(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data-w"},"spec":{"storageClassName":%q,"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"1Gi"}}}}`, class), 0)
	w := statesCall(t, admin, "admin "+nsWait+" before the Pod", nsWait)
	t.Run("C6-waiting", func(t *testing.T) {
		checkS(t, "C6", "claim-without-consumer", w.ans,
			wantS{"pvc-pv", "PersistentVolumeClaim/data-w", "", "PersistentVolume", "", "waiting", "data-w waits for a consumer Pod", ""})
	})
	t.Run("C16", func(t *testing.T) {
		for _, c := range []struct {
			ns  string
			ans stateAnswer
		}{{"kube-system", statesCall(t, admin, "admin kube-system", "kube-system").ans}, {"local-path-storage", statesCall(t, admin, "admin local-path-storage", "local-path-storage").ans}, {nsWait, w.ans}} {
			broken := countLine(t, "C16", c.ns, c.ans)
			t.Logf("FA2-T7a C16 input=%s expected=broken=0 bridges>0 actual=broken=%d bridges=%d result=%s", c.ns, broken, len(c.ans.Bridges), result(broken == 0 && len(c.ans.Bridges) > 0))
			if broken != 0 || len(c.ans.Bridges) == 0 {
				t.Errorf("C16 %s: %d broken of %d bridges; want 0 broken in a namespace with bridges", c.ns, broken, len(c.ans.Bridges))
			}
		}
	})
	created := time.Now()
	createJSON(t, nsWait, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"w"},"spec":{"terminationGracePeriodSeconds":0,"containers":[{"name":"c","image":"registry.k8s.io/pause:3.10",`+
		`"volumeMounts":[{"name":"d","mountPath":"/d"}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"data-w"}}]}}`, 0)
	t.Logf("FA2-T7a C6 Pod w created at %s", created.Format(time.RFC3339Nano))
	t.Run("C6-connected", func(t *testing.T) {
		var last stateCall
		connectedAt := time.Time{}
		for time.Since(created) < 120*time.Second {
			last = statesCall(t, admin, "admin "+nsWait+" polling", nsWait)
			for _, b := range last.ans.Bridges {
				if b.Kind == "pvc-pv" && last.ans.nodeName(b.From.UID) == "PersistentVolumeClaim/data-w" && b.State == "connected" {
					connectedAt = time.Now()
				}
			}
			if !connectedAt.IsZero() {
				break
			}
			time.Sleep(2 * time.Second)
		}
		t.Logf("FA2-T7a C6 first connected call at %s, %s after the Pod was created", connectedAt.Format(time.RFC3339Nano), connectedAt.Sub(created).Round(time.Millisecond))
		if connectedAt.IsZero() {
			t.Fatalf("C6: data-w not connected within 120 s")
		}
		volume := strings.TrimSpace(string(kubectl(t, nil, "get", "pvc", "data-w", "-n", nsWait, "-o", "jsonpath={.spec.volumeName}")))
		checkS(t, "C6", "claim-with-consumer", last.ans,
			wantS{"pvc-pv", "PersistentVolumeClaim/data-w", "", "PersistentVolume", volume, "connected", "", rawUID(t, "/api/v1/persistentvolumes/"+volume)},
			wantS{"pod-pvc", "Pod/w", nsWait, "PersistentVolumeClaim", "data-w", "connected", "", rawUID(t, "/api/v1/namespaces/"+nsWait+"/persistentvolumeclaims/data-w")})
		countLine(t, "C6", "claim-with-consumer", last.ans)
	})

	// Unknown with a view ServiceAccount (C9, C15 Secrets) and no read of values (C19).
	secretCanary := strings.Join([]string{"intuitik", "states", "secret", randomHex(12)}, "-")
	configCanary := strings.Join([]string{"intuitik", "states", "configmap", randomHex(12)}, "-")
	literalCanary := strings.Join([]string{"canary", "states", "literal", randomHex(12)}, "-")
	kubectl(t, nil, "create", "secret", "generic", "db", "-n", nsView, "--from-literal=username="+secretCanary)
	kubectl(t, nil, "create", "secret", "generic", "tls", "-n", nsView, "--from-literal=tls.key="+secretCanary)
	kubectl(t, nil, "create", "configmap", "app", "-n", nsView, "--from-literal=level="+configCanary, "--from-literal=app.yaml="+configCanary, "--from-literal=log.yaml=level: info")
	createJSON(t, nsView, fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"api"},"spec":{
		"nodeSelector":{"intuitik.example/none":"none"},"terminationGracePeriodSeconds":0,
		"containers":[{"name":"api","image":%q,
			"env":[{"name":"DB_USER","valueFrom":{"secretKeyRef":{"name":"db","key":"username"}}},
				{"name":"LOG_LEVEL","valueFrom":{"configMapKeyRef":{"name":"app","key":"level"}}},
				{"name":"MODE","value":%q}],
			"envFrom":[{"prefix":"APP_","configMapRef":{"name":"app"}}],
			"volumeMounts":[{"name":"cfg","mountPath":"/etc/app"},{"name":"tls","mountPath":"/tls"}]}],
		"volumes":[{"name":"cfg","configMap":{"name":"app","items":[{"key":"app.yaml","path":"app.yaml"},{"key":"log.yaml","path":"log.yaml"}]}},
			{"name":"tls","projected":{"sources":[{"secret":{"name":"tls"}}]}}]}}`, pause, literalCanary), 0)
	kubectl(t, nil, "create", "serviceaccount", "viewer", "-n", nsView)
	kubectl(t, nil, "create", "rolebinding", "viewer", "-n", nsView, "--clusterrole=view", "--serviceaccount="+nsView+":viewer")
	settled(t, nsView)
	t.Run("C9", func(t *testing.T) {
		if canI(t, nsView, "viewer", "list", "secrets") != "no" || canI(t, nsView, "viewer", "list", "configmaps") != "yes" {
			t.Fatal("preparation: the ServiceAccount viewer does not have the view permissions of C9")
		}
		p := saPanelNS(t, nsView, "viewer", "")
		c := statesCall(t, p, "viewer "+nsView, nsView)
		if !c.ans.NamespaceState.ExistenceChecked {
			t.Errorf("STOP-4 (section 8): the Namespace GET is refused under the view RoleBinding (existenceChecked false)")
		}
		checkS(t, "C9", "view-role", c.ans,
			wantS{"pod-secret", "Pod/api", nsView, "Secret", "db", "unknown", "no permission to read Secrets", ""},
			wantS{"pod-secret", "Pod/api", nsView, "Secret", "tls", "unknown", "no permission to read Secrets", ""},
			wantS{"pod-configmap", "Pod/api", nsView, "ConfigMap", "app", "connected", "", rawUID(t, "/api/v1/namespaces/"+nsView+"/configmaps/app")})
		hasRefused := false
		for _, r := range c.ans.Refused {
			hasRefused = hasRefused || (r.Kind == "Secret" && r.Status == 403 && r.Text == "Couldn't load Secrets (403)")
		}
		broken, notFound := countLine(t, "C9", "view-role", c.ans), notFoundTexts(c.ans)
		pass := hasRefused && broken == 0 && notFound == 0
		t.Logf("FA2-T7a C9 input=view-role expected=refused has Secret 403 Couldn't load Secrets (403), broken=0, not-found-texts=0 actual=refused=%q broken=%d not-found-texts=%d result=%s", c.ans.refusedTexts(), broken, notFound, result(pass))
		t.Logf("FA2-T7a C15 input=view-role Secrets refused: same call, unknown and never not found result=%s", result(pass))
		if !pass {
			t.Errorf("C9: refused %q, broken %d, not found %d", c.ans.refusedTexts(), broken, notFound)
		}
	})
	t.Run("C19", func(t *testing.T) {
		c := statesCall(t, admin, "admin "+nsView, nsView)
		var reads []string
		for _, e := range c.events {
			if e.ObjectRef != nil && e.ObjectRef.APIGroup == "" && (e.ObjectRef.Resource == "secrets" || e.ObjectRef.Resource == "configmaps") {
				path, _, _ := strings.Cut(e.RequestURI, "?")
				reads = append(reads, e.Verb+" "+path)
			}
		}
		sort.Strings(reads)
		want := []string{"list /api/v1/namespaces/" + nsView + "/configmaps", "list /api/v1/namespaces/" + nsView + "/secrets"}
		t.Logf("FA2-T7a C19 input=view-namespace audit window reads of secrets or configmaps expected=%q actual=%q result=%s", want, reads, result(reflect.DeepEqual(reads, want)))
		if !reflect.DeepEqual(reads, want) {
			t.Errorf("C19: reads %q, want %q", reads, want)
		}
		output := append(admin.stdout.bytes(), admin.stderr.bytes()...)
		for _, cn := range []struct{ name, value, resource, object string }{
			{"secret", secretCanary, "secret", "db"}, {"configmap", configCanary, "configmap", "app"}, {"literal-env", literalCanary, "pod", "api"},
		} {
			control := secretscan.Count(kubectl(t, nil, "get", cn.resource, cn.object, "-n", nsView, "-o", "json"), cn.value)
			inAnswer, inOutput := secretscan.Count(c.raw, cn.value), secretscan.Count(output, cn.value)
			pass := control > 0 && inAnswer == 0 && inOutput == 0
			t.Logf("FA2-T7a C19 input=view-namespace canary=%s positive control in %s %s read by kubectl %d (want at least 1), in the answer %d, in intuitik's output %d (want 0) result=%s",
				cn.name, cn.resource, cn.object, control, inAnswer, inOutput, result(pass))
			if !pass {
				t.Errorf("C19 canary %s: control %d, answer %d, output %d", cn.name, control, inAnswer, inOutput)
			}
		}
	})

	// A ServiceAccount that cannot list PersistentVolumes (C15 V-8).
	createJSON(t, nsPV, `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data-p"},"spec":{"storageClassName":"standard","accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"1Gi"}}}}`, 0)
	createJSON(t, nsPV, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"pv-user"},"spec":{"terminationGracePeriodSeconds":0,"containers":[{"name":"c","image":"registry.k8s.io/pause:3.10",`+
		`"volumeMounts":[{"name":"d","mountPath":"/d"}]}],"volumes":[{"name":"d","persistentVolumeClaim":{"claimName":"data-p"}}]}}`, 0)
	waitFor(t, 180*time.Second, "claim data-p bound", func() bool {
		out, _ := kubectlErr(nil, "get", "pvc", "data-p", "-n", nsPV, "-o", "jsonpath={.status.phase}")
		return strings.TrimSpace(string(out)) == "Bound"
	})
	kubectl(t, nil, "create", "serviceaccount", "no-pv", "-n", nsPV)
	kubectl(t, nil, "create", "rolebinding", "no-pv", "-n", nsPV, "--clusterrole=view", "--serviceaccount="+nsPV+":no-pv")
	clusterRole(t, nsPV, "no-pv", "storageclasses")
	t.Run("C15", func(t *testing.T) {
		if canI(t, nsPV, "no-pv", "list", "persistentvolumes") != "no" || canI(t, nsPV, "no-pv", "list", "storageclasses") != "yes" {
			t.Fatal("preparation: the ServiceAccount no-pv does not have the permissions of C15")
		}
		p := saPanelNS(t, nsPV, "no-pv", "")
		c := statesCall(t, p, "no-pv "+nsPV, nsPV)
		volume := strings.TrimSpace(string(kubectl(t, nil, "get", "pvc", "data-p", "-n", nsPV, "-o", "jsonpath={.spec.volumeName}")))
		checkS(t, "C15", "persistentvolumes-403", c.ans,
			wantS{"pvc-pv", "PersistentVolumeClaim/data-p", "", "PersistentVolume", volume, "unknown", "no permission to read PersistentVolumes", ""})
		hasRefused := false
		for _, r := range c.ans.Refused {
			hasRefused = hasRefused || (r.Kind == "PersistentVolume" && r.Status == 403 && r.Text == "Couldn't load PersistentVolumes (403)")
		}
		notFound := notFoundTexts(c.ans)
		pass := hasRefused && notFound == 0
		t.Logf("FA2-T7a C15 input=persistentvolumes-403 expected=refused has PersistentVolume 403, not-found-texts=0 actual=refused=%q not-found-texts=%d result=%s", c.ans.refusedTexts(), notFound, result(pass))
		if !pass {
			t.Errorf("C15 PV: refused %q, not found %d", c.ans.refusedTexts(), notFound)
		}
		countLine(t, "C15", "persistentvolumes-403", c.ans)
	})

	// Every FA2-T6 kind bridge has the state 3.2 gives it (C17).
	t.Run("C17", func(t *testing.T) {
		b := statesCall(t, admin, "admin "+nsBridges, nsBridges)
		warn := "targetPort 8080 is not declared by the container (traffic not verifiable)"
		var ws []wantS
		for _, o := range objects(t, nsBridges, "pods") {
			if strings.HasPrefix(o.Metadata.Name, "api-") && o.Metadata.Name != "api-other" {
				ws = append(ws, wantS{"service-pod", "Service/api", nsBridges, "Pod", o.Metadata.Name, "warning", warn, o.Metadata.UID})
			}
		}
		ws = append(ws,
			wantS{"ingress-service", "Ingress/shop", nsBridges, "Service", "api", "connected", "", "-"},
			wantS{"ingress-service", "Ingress/plain", nsBridges, "Service", "api", "connected", "", "-"},
			wantS{"pod-pvc", "Pod/db-0", nsBridges, "PersistentVolumeClaim", "data-db-0", "connected", "", "-"},
			wantS{"pod-secret", "Pod/api", nsBridges, "Secret", "db", "connected", "", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/secrets/db")},
			wantS{"pod-configmap", "Pod/api", nsBridges, "ConfigMap", "app", "connected", "", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/configmaps/app")},
			wantS{"pod-secret", "Pod/api", nsBridges, "Secret", "tls", "connected", "", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/secrets/tls")},
			wantS{"hpa-target", "HorizontalPodAutoscaler/api", nsBridges, "Deployment", "api", "connected", "", "-"},
			wantS{"hpa-target", "HorizontalPodAutoscaler/w", nsBridges, "StatefulSet", "db", "connected", "", "-"},
			wantS{"hpa-target", "HorizontalPodAutoscaler/xapi", nsBridges, "Deployment", "api", "connected", "", "-"})
		volume := strings.TrimSpace(string(kubectl(t, nil, "get", "pvc", "data-db-0", "-n", nsBridges, "-o", "jsonpath={.spec.volumeName}")))
		ws = append(ws, wantS{"pvc-pv", "PersistentVolumeClaim/data-db-0", "", "PersistentVolume", volume, "connected", "", rawUID(t, "/api/v1/persistentvolumes/"+volume)})
		checkS(t, "C17", "fa2-t6-"+nsBridges, b.ans, ws...)
		s := statesCall(t, admin, "admin "+nsShop, nsShop)
		checkS(t, "C17", "fa2-t6-"+nsShop, s.ans,
			wantS{"httproute-service", "HTTPRoute/shop", nsShop, "Service", "checkout", "connected", "", "-"},
			wantS{"httproute-service", "HTTPRoute/open", nsShop, "Service", "checkout", "connected", "", "-"},
			wantS{"httproute-service", "HTTPRoute/lost", nsShop, "Service", "gone", "broken", "Service gone not found", ""},
			wantS{"httproute-service", "HTTPRoute/cross", nsOther, "Service", "checkout", "unknown", "Service other/checkout is in another namespace (not checked)", ""})
	})
}
