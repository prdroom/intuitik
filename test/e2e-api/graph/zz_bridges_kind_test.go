//go:build kind

package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// Kind tests of packet FA2-T6 (job e2e-graph): the bridges of GET /api/graph/containment
// and their settings, and the HTTPRoute cases of F-18. TestG_Bridges is one top-level test
// that never runs in parallel, in the file that sorts after every FA2-T5 test file of this package,
// and it runs the phases of E.6 in order: (a) the built-in kinds with no Gateway API group
// in discovery, (b) the test CRD that serves HTTPRoute only at v1alpha2, deleted at the end
// of the phase, (c) the Gateway API v1.7.0 bundle, checked by its sha256 and created. Each
// checked bridge prints one line:
//   FA2-T6 <criterion> input=<fixture> kind=<bridge kind> from=<kind/name> to=<ns/kind/name uid|null> expected=<settings joined by " | "> actual=<same> expected-to=<group>/<uid|null> actual-to=<same> [expected-text=<...> actual-text=<...>] result=<PASS|FAIL>
// with U+2192 and U+2190 printed as \u2192 and \u2190. The expected strings are written
// here independently of the product (packet section 3).

const (
	nsBridges = "graph-bridges"  // phase (a): C1 to C5, C7, C8, C12 (c), C14
	nsAlpha   = "graph-gw-alpha" // phase (b): C13
	nsShop    = "shop"           // phase (c): C6, C9, C10, C11, C12 (a) and (b)
	nsOther   = "other"          // phase (c): the namespace of the cross-namespace backendRef

	saNoRoutes   = "no-routes"   // nsShop: every listed kind but httproutes (C9)
	saNoServices = "no-services" // nsShop: every listed kind but core services (C11)

	gatewayAPIGroup = "gateway.networking.k8s.io"
	// The Gateway API bundle of packet section 11: release v1.7.0, asset
	// standard-install.yaml, and its sha256 as read by Devgru on 2026-11-19.
	gatewayBundleURL    = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.7.0/standard-install.yaml"
	gatewayBundleSHA256 = "4e2602c4cbe1afbc394d86f4ac6522e1a5263b01133bba1e40a6d1a549e753a1"

	toRight = "\u2192"
	toLeft  = "\u2190"
	pause   = "registry.k8s.io/pause:3.10"
)

// escArrows writes the two arrows as their escapes, so the log stays ASCII.
func escArrows(s string) string {
	return strings.NewReplacer(toRight, `\u2192`, toLeft, `\u2190`).Replace(s)
}

func result(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// bridgeAnswer is the graph answer with the fields of FA2-T6 E.1.
type bridgeAnswer struct {
	Nodes   []node `json:"nodes"`
	Refused []struct {
		Kind   string `json:"kind"`
		Group  string `json:"group"`
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
		Settings []string `json:"settings"`
		Text     string   `json:"text"`
	} `json:"bridges"`
	Unsupported []unsupportedEntry `json:"unsupported"`
}

type unsupportedEntry struct {
	Group   string `json:"group"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Text    string `json:"text"`
}

// refusedList is every refused entry as "<kind> <status> <text>" (group entries as
// "<group> <status> <text>").
func (a bridgeAnswer) refusedList() []string {
	out := []string{}
	for _, r := range a.Refused {
		k := r.Kind
		if k == "" {
			k = r.Group
		}
		out = append(out, fmt.Sprintf("%s %d %s", k, r.Status, r.Text))
	}
	return out
}

func (a bridgeAnswer) nodeName(uid string) string {
	for _, n := range a.Nodes {
		if n.UID == uid {
			return n.Kind + "/" + n.Name
		}
	}
	return "uid:" + uid
}

// call is one graph call of these tests: the answer, its raw body and the product's
// requests of its audit window, which G7 checked with no owner-named ConfigMap or Secret.
type call struct {
	status int
	ans    bridgeAnswer
	raw    []byte
	events []auditEvent
}

// bridgeCall sends one graph call for ns, prints what discovery shows for the Gateway API
// group at that moment, and checks its audit window (G7).
func bridgeCall(t *testing.T, p *panel, label, ns string) call {
	t.Helper()
	t.Logf("FA2-T6 discovery input=%s %s=%s", label, gatewayAPIGroup, gatewayDiscovery(t))
	a := auditStart(t)
	start := time.Now()
	status, raw := p.request("GET", "/api/graph/containment?namespace="+ns, nil, true)
	end := time.Now()
	g7(t, label, p, a, start, end, ns, nil)
	c := call{status: status, raw: raw, events: a.events(t, start, end)}
	if err := json.Unmarshal(raw, &c.ans); err != nil {
		t.Errorf("FA2-T6 %s: the answer is not JSON (%v)", label, err)
	}
	t.Logf("FA2-T6 call input=%s namespace=%s status=%d nodes=%d bridges=%d refused=%d unsupported=%d",
		label, ns, status, len(c.ans.Nodes), len(c.ans.Bridges), len(c.ans.Refused), len(c.ans.Unsupported))
	if status != 200 {
		t.Errorf("FA2-T6 %s: status %d, want 200", label, status)
	}
	return c
}

// wantB is one expected bridge; from is "<kind>/<name>" of a node and uid the expected
// to.uid, "" for null.
type wantB struct {
	kind, from                string
	group, ns, toKind, toName string
	uid                       string
	settings                  []string
	text                      string
}

func uidOrNull(u *string) string {
	if u == nil {
		return "null"
	}
	return *u
}

// matching returns the indexes of the bridges of kind from from to ns/toKind/toName (any
// group); toName "" matches every target.
func matching(a bridgeAnswer, kind, from, ns, toKind, toName string) []int {
	var out []int
	for i, b := range a.Bridges {
		if b.Kind != kind || a.nodeName(b.From.UID) != from {
			continue
		}
		if toName != "" && (b.To.Namespace != ns || b.To.Kind != toKind || b.To.Name != toName) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// checkB compares each expected bridge with the answer and prints its line.
func checkB(t *testing.T, criterion, input string, a bridgeAnswer, ws ...wantB) {
	t.Helper()
	for _, w := range ws {
		got := matching(a, w.kind, w.from, w.ns, w.toKind, w.toName)
		wantUID := w.uid
		if wantUID == "" {
			wantUID = "null"
		}
		expected := strings.Join(w.settings, " | ")
		actual, actualTo, actualUID, text := "absent", "-", "-", ""
		pass := false
		switch len(got) {
		case 0:
		case 1:
			b := a.Bridges[got[0]]
			actual, actualUID, text = strings.Join(b.Settings, " | "), uidOrNull(b.To.UID), b.Text
			actualTo = b.To.Group + "/" + actualUID
			pass = reflect.DeepEqual(b.Settings, w.settings) && b.To.Group == w.group && actualUID == wantUID && b.Text == w.text
		default:
			actual = fmt.Sprintf("%d bridges", len(got))
		}
		line := fmt.Sprintf("FA2-T6 %s input=%s kind=%s from=%s to=%s/%s/%s %s expected=%s actual=%s expected-to=%s/%s actual-to=%s",
			criterion, input, w.kind, w.from, w.ns, w.toKind, w.toName, actualUID, escArrows(expected), escArrows(actual), w.group, wantUID, actualTo)
		if w.text != "" || text != "" {
			line += fmt.Sprintf(" expected-text=%s actual-text=%s", w.text, text)
		}
		t.Logf("%s result=%s", line, result(pass))
		if !pass {
			t.Errorf("FA2-T6 %s %s %s -> %s/%s/%s: got %q to %s text %q (%d bridges); want %q to %s/%s text %q",
				criterion, w.kind, w.from, w.ns, w.toKind, w.toName, actual, actualTo, text, len(got), expected, w.group, wantUID, w.text)
		}
	}
}

// noB checks that from has no bridge of kind to ns/toKind/toName (toName "" for any).
func noB(t *testing.T, criterion, input string, a bridgeAnswer, kind, from, ns, toKind, toName string) {
	t.Helper()
	got := matching(a, kind, from, ns, toKind, toName)
	target := ns + "/" + toKind + "/" + toName
	if toName == "" {
		target = "any"
	}
	t.Logf("FA2-T6 %s input=%s kind=%s from=%s to=%s expected=absent actual=%d bridges result=%s", criterion, input, kind, from, target, len(got), result(len(got) == 0))
	if len(got) != 0 {
		t.Errorf("FA2-T6 %s %s %s -> %s: %d bridges, want none", criterion, kind, from, target, len(got))
	}
}

// checkPlace compares the place of one object with the answer (FA2-T6 line prefix).
func checkPlace(t *testing.T, criterion, input string, a bridgeAnswer, w want) {
	t.Helper()
	byUID := map[string]node{}
	for _, n := range a.Nodes {
		byUID[n.UID] = n
	}
	actual := "absent"
	for _, n := range a.Nodes {
		if n.Kind != w.kind || n.Name != w.name {
			continue
		}
		parent := "-"
		if n.Parent != nil {
			parent = "uid:" + *n.Parent
			if pn, ok := byUID[*n.Parent]; ok {
				parent = pn.Kind + "/" + pn.Name
			}
		}
		actual = fmt.Sprintf("%s/%s %s %s", w.kind, w.name, parent, n.Owner.State)
	}
	expected := fmt.Sprintf("%s/%s %s %s", w.kind, w.name, w.parent, w.state)
	t.Logf("FA2-T6 %s input=%s expected=%s actual=%s result=%s", criterion, input, expected, actual, result(actual == expected))
	if actual != expected {
		t.Errorf("FA2-T6 %s: got %s, want %s", criterion, actual, expected)
	}
}

// gatewayDiscovery is what /apis shows for the Gateway API group: "absent", or its served
// versions.
func gatewayDiscovery(t *testing.T) string {
	t.Helper()
	var v struct {
		Groups []struct {
			Name     string `json:"name"`
			Versions []struct {
				Version string `json:"version"`
			} `json:"versions"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(kubectl(t, nil, "get", "--raw", "/apis"), &v); err != nil {
		t.Fatalf("preparation: /apis: %v", err)
	}
	for _, g := range v.Groups {
		if g.Name == gatewayAPIGroup {
			var vs []string
			for _, x := range g.Versions {
				vs = append(vs, x.Version)
			}
			return "served " + strings.Join(vs, ",")
		}
	}
	return "absent"
}

// phase prints the start line of one phase of E.6.
func phase(t *testing.T, name, extra string) {
	t.Helper()
	t.Logf("FA2-T6 phase=%s start=%s discovery %s=%s%s", name, time.Now().Format(time.RFC3339Nano), gatewayAPIGroup, gatewayDiscovery(t), extra)
}

// rawUID reads the uid of the object at an API path (no resource name lookup, so a custom
// kind named like a built-in one is never read in its place).
func rawUID(t *testing.T, path string) string {
	t.Helper()
	var o object
	if err := json.Unmarshal(kubectl(t, nil, "get", "--raw", path), &o); err != nil || o.Metadata.UID == "" {
		t.Fatalf("preparation: no uid at %s (%v)", path, err)
	}
	return o.Metadata.UID
}

// createJSON creates one object given as JSON in ns, retrying for up to limit while the
// API server does not know its kind yet.
func createJSON(t *testing.T, ns, js string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		_, err := kubectlErr([]byte(js), "create", "-n", ns, "-f", "-")
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("preparation: create in %s: %v", ns, err)
		}
		time.Sleep(time.Second)
	}
}

// roleAllBut binds sa of ns to a Role with get and list on every namespaced resource that
// discovery lists, except core secrets and configmaps and the resource except
// ("<resource>" or "<resource>.<group>").
func roleAllBut(t *testing.T, ns, sa, except string) {
	t.Helper()
	kubectl(t, nil, "create", "serviceaccount", sa, "-n", ns)
	var rules []any
	for _, name := range strings.Fields(string(kubectl(t, nil, "api-resources", "--namespaced=true", "--verbs=list", "-o", "name"))) {
		if name == except || name == "secrets" || name == "configmaps" {
			continue
		}
		res, group, _ := strings.Cut(name, ".")
		rules = append(rules, map[string]any{"apiGroups": []any{group}, "resources": []any{res}, "verbs": []any{"get", "list"}})
	}
	// FA2-T7a 8 edit 6: the list (not get) of core ConfigMaps and Secrets, which every call
	// sends, so the refusals the tests assert stay what they were.
	rules = append(rules, map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps", "secrets"}, "verbs": []any{"list"}})
	role, _ := json.Marshal(map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": map[string]any{"name": sa, "namespace": ns}, "rules": rules})
	kubectl(t, role, "create", "-f", "-")
	kubectl(t, nil, "create", "rolebinding", sa, "-n", ns, "--role="+sa, "--serviceaccount="+ns+":"+sa)
}

// TestG_Bridges (C1 to C14 on kind, E.6): the three phases in order.
func TestG_Bridges(t *testing.T) {
	kindCluster(t)
	for _, ns := range []string{nsBridges, nsAlpha, nsShop, nsOther} {
		kubectl(t, nil, "create", "namespace", ns)
	}

	// Phase (a): built-in kinds, the example.com kinds named Service and Deployment, and no
	// Gateway API group.
	secretCanary := strings.Join([]string{"intuitik", "bridge", "secret", randomHex(12)}, "-")
	configCanary := strings.Join([]string{"intuitik", "bridge", "configmap", randomHex(12)}, "-")
	literalCanary := strings.Join([]string{"canary", "literal", randomHex(12)}, "-")
	kubectl(t, nil, "apply", "-f", "../../fixtures/bridges/crds.yaml")
	kubectl(t, nil, "wait", "--for=condition=Established", "--timeout=90s", "crd/services.example.com", "crd/deployments.example.com")
	kubectl(t, nil, "create", "-n", nsBridges, "-f", "../../fixtures/bridges/builtins.yaml")
	kubectl(t, nil, "create", "secret", "generic", "db", "-n", nsBridges, "--from-literal=username="+secretCanary)
	kubectl(t, nil, "create", "secret", "generic", "tls", "-n", nsBridges, "--from-literal=tls.key="+secretCanary)
	kubectl(t, nil, "create", "configmap", "app", "-n", nsBridges, "--from-literal=level="+configCanary, "--from-literal=app.yaml="+configCanary, "--from-literal=log.yaml=level: info")
	createJSON(t, nsBridges, fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"api"},"spec":{
		"nodeSelector":{"intuitik.example/none":"none"},"terminationGracePeriodSeconds":0,
		"containers":[{"name":"api","image":%q,
			"env":[{"name":"DB_USER","valueFrom":{"secretKeyRef":{"name":"db","key":"username"}}},
				{"name":"LOG_LEVEL","valueFrom":{"configMapKeyRef":{"name":"app","key":"level"}}},
				{"name":"MODE","value":%q}],
			"envFrom":[{"prefix":"APP_","configMapRef":{"name":"app"}}],
			"volumeMounts":[{"name":"cfg","mountPath":"/etc/app"},{"name":"tls","mountPath":"/tls"}]}],
		"volumes":[{"name":"cfg","configMap":{"name":"app","items":[{"key":"app.yaml","path":"app.yaml"},{"key":"log.yaml","path":"log.yaml"}]}},
			{"name":"tls","projected":{"sources":[{"secret":{"name":"tls"}}]}}]}}`, pause, literalCanary), 0)
	createJSON(t, nsBridges, `{"apiVersion":"example.com/v1","kind":"Deployment","metadata":{"name":"api"}}`, 60*time.Second)
	createJSON(t, nsBridges, `{"apiVersion":"autoscaling/v2","kind":"HorizontalPodAutoscaler","metadata":{"name":"xapi"},
		"spec":{"scaleTargetRef":{"apiVersion":"example.com/v1","kind":"Deployment","name":"api"},"minReplicas":1,"maxReplicas":2}}`, 0)
	stsUID := rawUID(t, "/apis/apps/v1/namespaces/"+nsBridges+"/statefulsets/db")
	createJSON(t, nsBridges, fmt.Sprintf(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"db",
		"ownerReferences":[{"apiVersion":"apps/v1","kind":"StatefulSet","name":"db","uid":%q,"controller":true}]},
		"spec":{"ports":[{"port":5432}]}}`, stsUID), 0)
	waitFor(t, 60*time.Second, "the two Pods of Deployment api", func() bool {
		out, _ := kubectlErr(nil, "get", "pods", "-n", nsBridges, "-l", "app=api,tier=web", "-o", "name")
		return len(strings.Fields(string(out))) == 2
	})
	var pvc struct {
		Metadata struct {
			OwnerReferences []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
		Spec struct {
			VolumeName string `json:"volumeName"`
		} `json:"spec"`
		Status struct {
			Phase    string            `json:"phase"`
			Capacity map[string]string `json:"capacity"`
		} `json:"status"`
	}
	waitFor(t, 180*time.Second, "claim data-db-0 bound and owned by StatefulSet db", func() bool {
		out, err := kubectlErr(nil, "get", "--raw", "/api/v1/namespaces/"+nsBridges+"/persistentvolumeclaims/data-db-0")
		if err != nil || json.Unmarshal(out, &pvc) != nil {
			return false
		}
		owned := false
		for _, r := range pvc.Metadata.OwnerReferences {
			owned = owned || (r.Kind == "StatefulSet" && r.Name == "db")
		}
		return pvc.Status.Phase == "Bound" && pvc.Spec.VolumeName != "" && pvc.Status.Capacity["storage"] != "" && owned
	})
	t.Logf("FA2-T6 read-back claim data-db-0 phase %s volume %s capacity %s ownerReferences %v", pvc.Status.Phase, pvc.Spec.VolumeName, pvc.Status.Capacity["storage"], pvc.Metadata.OwnerReferences)
	// C3 input: the Pod of the claim runs, its init container restore has completed.
	var dbPod struct {
		Status struct {
			Phase                 string `json:"phase"`
			InitContainerStatuses []struct {
				Name  string `json:"name"`
				State struct {
					Terminated *struct {
						ExitCode int    `json:"exitCode"`
						Reason   string `json:"reason"`
					} `json:"terminated"`
				} `json:"state"`
			} `json:"initContainerStatuses"`
			ContainerStatuses []struct {
				Name  string `json:"name"`
				Ready bool   `json:"ready"`
				State struct {
					Running *struct {
						StartedAt string `json:"startedAt"`
					} `json:"running"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	}
	waitFor(t, 180*time.Second, "Pod db-0 Running with its init container completed", func() bool {
		out, err := kubectlErr(nil, "get", "--raw", "/api/v1/namespaces/"+nsBridges+"/pods/db-0")
		if err != nil || json.Unmarshal(out, &dbPod) != nil {
			return false
		}
		running := false
		for _, c := range dbPod.Status.ContainerStatuses {
			running = running || (c.Name == "db" && c.State.Running != nil)
		}
		return dbPod.Status.Phase == "Running" && running
	})
	var inits, mains []string
	for _, c := range dbPod.Status.InitContainerStatuses {
		state := "not terminated"
		if c.State.Terminated != nil {
			state = fmt.Sprintf("terminated exitCode=%d reason=%s", c.State.Terminated.ExitCode, c.State.Terminated.Reason)
		}
		inits = append(inits, c.Name+" "+state)
	}
	for _, c := range dbPod.Status.ContainerStatuses {
		mains = append(mains, fmt.Sprintf("%s running=%v ready=%v", c.Name, c.State.Running != nil, c.Ready))
	}
	out, _ := kubectlErr(nil, "get", "--raw", "/api/v1/namespaces/"+nsBridges+"/persistentvolumeclaims/data-db-0")
	json.Unmarshal(out, &pvc)
	t.Logf("FA2-T6 C3 input=statefulset-db-claim read-back pod db-0 phase %s init %v containers %v; claim data-db-0 phase %s volume %s", dbPod.Status.Phase, inits, mains, pvc.Status.Phase, pvc.Spec.VolumeName)
	settled(t, nsBridges)

	phase(t, "a", "")
	admin := adminPanel(t)
	a := bridgeCall(t, admin, "admin "+nsBridges, nsBridges)
	apiUID := rawUID(t, "/api/v1/namespaces/"+nsBridges+"/services/api")
	t.Run("C1", func(t *testing.T) {
		checkB(t, "C1", "ingress-shop-plain", a.ans,
			wantB{"ingress-service", "Ingress/shop", "", nsBridges, "Service", "api", apiUID,
				[]string{"/api  shop.local " + toRight + " api:80", "/healthz (Exact)  shop.local " + toRight + " api:http"}, ""},
			wantB{"ingress-service", "Ingress/plain", "", nsBridges, "Service", "api", apiUID, []string{"(default)  * " + toRight + " api:80"}, ""})
	})
	t.Run("C2", func(t *testing.T) {
		settings := []string{"selector app=api,tier=web", ":80" + toRight + "8080", ":53" + toRight + "53/UDP"}
		var ws []wantB
		for _, o := range objects(t, nsBridges, "pods") {
			if o.Metadata.Name == "api-other" || !strings.HasPrefix(o.Metadata.Name, "api-") {
				continue
			}
			ws = append(ws, wantB{"service-pod", "Service/api", "", nsBridges, "Pod", o.Metadata.Name, o.Metadata.UID, settings, "targetPort 8080 is not declared by the container (traffic not verifiable)"})
		}
		if len(ws) != 2 {
			t.Fatalf("preparation: %d Pods of Deployment api, want 2", len(ws))
		}
		checkB(t, "C2", "service-api", a.ans, ws...)
		noB(t, "C2", "service-api-partial-labels", a.ans, "service-pod", "Service/api", nsBridges, "Pod", "api-other")
		noB(t, "C2", "service-externalname", a.ans, "service-pod", "Service/ext", "", "", "")
		noB(t, "C2", "service-no-selector", a.ans, "service-pod", "Service/nosel", "", "", "")
	})
	t.Run("C3", func(t *testing.T) {
		checkB(t, "C3", "statefulset-db-claim", a.ans,
			wantB{"pod-pvc", "Pod/db-0", "", nsBridges, "PersistentVolumeClaim", "data-db-0", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/persistentvolumeclaims/data-db-0"),
				[]string{"volume data-db-0", "mount db:/var/lib/data", "mount restore:/backup (read-only)", "access ReadWriteOnce"}, ""},
			wantB{"pvc-pv", "PersistentVolumeClaim/data-db-0", "", "", "PersistentVolume", pvc.Spec.VolumeName, rawUID(t, "/api/v1/persistentvolumes/"+pvc.Spec.VolumeName),
				[]string{"class standard", "capacity " + pvc.Status.Capacity["storage"]}, ""})
	})
	t.Run("C4", func(t *testing.T) {
		checkB(t, "C4", "pod-api-config", a.ans,
			wantB{"pod-secret", "Pod/api", "", nsBridges, "Secret", "db", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/secrets/db"), []string{"env api:DB_USER " + toLeft + " username"}, ""},
			wantB{"pod-configmap", "Pod/api", "", nsBridges, "ConfigMap", "app", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/configmaps/app"),
				[]string{"env api:LOG_LEVEL " + toLeft + " level", "envFrom api prefix APP_", "volume api:/etc/app: app.yaml, log.yaml"}, ""},
			wantB{"pod-secret", "Pod/api", "", nsBridges, "Secret", "tls", rawUID(t, "/api/v1/namespaces/"+nsBridges+"/secrets/tls"), []string{"volume api:/tls (all keys)"}, ""})
	})
	t.Run("C5", func(t *testing.T) {
		checkB(t, "C5", "hpa-api-w", a.ans,
			wantB{"hpa-target", "HorizontalPodAutoscaler/api", "apps", nsBridges, "Deployment", "api", rawUID(t, "/apis/apps/v1/namespaces/"+nsBridges+"/deployments/api"),
				[]string{"min 2", "max 5"}, ""},
			wantB{"hpa-target", "HorizontalPodAutoscaler/w", "apps", nsBridges, "StatefulSet", "db", stsUID, []string{"min 1", "max 3"}, ""})
	})
	t.Run("C7", func(t *testing.T) {
		checkPlace(t, "C7", "service-owned-by-statefulset", a.ans, want{"Service", "db", "-", "none", ""})
		checkPlace(t, "C7", "claim-owned-by-statefulset", a.ans, want{"PersistentVolumeClaim", "data-db-0", "-", "none", ""})
		checkPlace(t, "C7", "statefulset-pod", a.ans, want{"Pod", "db-0", "StatefulSet/db", "contained", ""})
	})
	t.Run("C8", func(t *testing.T) {
		routes, routeBridges, routeRefused := 0, 0, 0
		for _, n := range a.ans.Nodes {
			if n.Kind == "HTTPRoute" {
				routes++
			}
		}
		for _, b := range a.ans.Bridges {
			if b.Kind == "httproute-service" {
				routeBridges++
			}
		}
		for _, r := range a.ans.Refused {
			if r.Kind == "HTTPRoute" || r.Group == gatewayAPIGroup {
				routeRefused++
			}
		}
		pass := routes == 0 && routeBridges == 0 && routeRefused == 0 && len(a.ans.Unsupported) == 0
		t.Logf("FA2-T6 C8 input=no-gateway-group expected=nodes=0 bridges=0 refused=0 unsupported=[] actual=nodes=%d bridges=%d refused=%d unsupported=%+v result=%s",
			routes, routeBridges, routeRefused, a.ans.Unsupported, result(pass))
		if !pass {
			t.Errorf("C8: HTTPRoute nodes %d, bridges %d, refused %d, unsupported %+v; want none", routes, routeBridges, routeRefused, a.ans.Unsupported)
		}
	})
	t.Run("C12-hpa-group", func(t *testing.T) {
		checkB(t, "C12", "hpa-custom-group", a.ans,
			wantB{"hpa-target", "HorizontalPodAutoscaler/xapi", "example.com", nsBridges, "Deployment", "api", rawUID(t, "/apis/example.com/v1/namespaces/"+nsBridges+"/deployments/api"),
				[]string{"min 1", "max 2"}, ""})
	})
	t.Run("C14", func(t *testing.T) {
		var reads []string
		for _, e := range a.events {
			if e.ObjectRef != nil && e.ObjectRef.APIGroup == "" && (e.ObjectRef.Resource == "secrets" || e.ObjectRef.Resource == "configmaps") {
				reads = append(reads, e.Verb+" "+e.ObjectRef.Resource+" "+e.ObjectRef.Name)
			}
		}
		// FA2-T7a 8 edit 2: exactly the list of each collection of the namespace, and no get.
		sort.Strings(reads)
		wantReads := []string{"list configmaps ", "list secrets "}
		t.Logf("FA2-T6 C14 input=pod-api-config audit window reads of secrets or configmaps expected=%q actual=%q result=%s", wantReads, reads, result(reflect.DeepEqual(reads, wantReads)))
		if !reflect.DeepEqual(reads, wantReads) {
			t.Errorf("C14: the call read %q; want exactly %q (no get of a ConfigMap or Secret, B1)", reads, wantReads)
		}
		output := append(admin.stdout.bytes(), admin.stderr.bytes()...)
		for _, c := range []struct{ name, value, resource, object string }{
			{"secret", secretCanary, "secret", "db"}, {"configmap", configCanary, "configmap", "app"}, {"literal-env", literalCanary, "pod", "api"},
		} {
			control := secretscan.Count(kubectl(t, nil, "get", c.resource, c.object, "-n", nsBridges, "-o", "json"), c.value)
			inAnswer, inOutput := secretscan.Count(a.raw, c.value), secretscan.Count(output, c.value)
			pass := control > 0 && inAnswer == 0 && inOutput == 0
			t.Logf("FA2-T6 C14 input=pod-api-config canary=%s positive control in %s %s read by kubectl %d (want at least 1), in the answer %d, in intuitik's output %d (want 0) result=%s",
				c.name, c.resource, c.object, control, inAnswer, inOutput, result(pass))
			if !pass {
				t.Errorf("C14 canary %s: control %d, answer %d, output %d", c.name, control, inAnswer, inOutput)
			}
		}
	})

	// Phase (b): HTTPRoute served only at v1alpha2 by the test CRD, before the bundle.
	if _, err := kubectlErr(nil, "create", "-f", "../../fixtures/bridges/httproute-v1alpha2-crd.yaml"); err != nil {
		t.Fatalf("STOP-4 (section 8): the v1alpha2 test CRD cannot be created before the bundle: %v", err)
	}
	kubectl(t, nil, "wait", "--for=condition=Established", "--timeout=90s", "crd/httproutes."+gatewayAPIGroup)
	waitFor(t, 90*time.Second, "discovery serving "+gatewayAPIGroup+"/v1alpha2 only", func() bool {
		return gatewayDiscovery(t) == "served v1alpha2"
	})
	createJSON(t, nsAlpha, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"checkout"},"spec":{"selector":{"app":"checkout"},"ports":[{"port":8080}]}}`, 0)
	createJSON(t, nsAlpha, `{"apiVersion":"gateway.networking.k8s.io/v1alpha2","kind":"HTTPRoute","metadata":{"name":"alpha"},
		"spec":{"hostnames":["alpha.local"],"rules":[{"backendRefs":[{"name":"checkout","port":8080}]}]}}`, 60*time.Second)
	phase(t, "b", "")
	b := bridgeCall(t, admin, "admin "+nsAlpha, nsAlpha)
	t.Run("C13", func(t *testing.T) {
		checkPlace(t, "C13", "v1alpha2-route-node", b.ans, want{"HTTPRoute", "alpha", "-", "none", ""})
		version := ""
		for _, n := range b.ans.Nodes {
			if n.Kind == "HTTPRoute" && n.Name == "alpha" {
				version = n.APIVersion
			}
		}
		noB(t, "C13", "v1alpha2-route", b.ans, "httproute-service", "HTTPRoute/alpha", "", "", "")
		want := []unsupportedEntry{{gatewayAPIGroup, "HTTPRoute", "v1alpha2", "Unsupported version v1alpha2"}}
		got := b.ans.Unsupported
		pass := reflect.DeepEqual(got, want) && version == gatewayAPIGroup+"/v1alpha2"
		actualText := ""
		if len(got) == 1 {
			actualText = got[0].Text
		}
		t.Logf("FA2-T6 C13 input=v1alpha2-only node-apiVersion=%s expected-unsupported=%+v actual-unsupported=%+v expected-text=%s actual-text=%s result=%s",
			version, want, got, want[0].Text, actualText, result(pass))
		if !pass {
			t.Errorf("C13: unsupported %+v, node apiVersion %s; want %+v and %s/v1alpha2", got, version, want, gatewayAPIGroup)
		}
	})
	kubectl(t, nil, "delete", "crd", "httproutes."+gatewayAPIGroup, "--wait=true", "--timeout=120s")
	waitFor(t, 120*time.Second, "discovery without "+gatewayAPIGroup, func() bool { return gatewayDiscovery(t) == "absent" })
	t.Logf("FA2-T6 phase=b end=%s discovery %s=absent", time.Now().Format(time.RFC3339Nano), gatewayAPIGroup)

	// Phase (c): the Gateway API v1.7.0 bundle, checked by its sha256, created with
	// kubectl create.
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Get(gatewayBundleURL)
	if err != nil {
		t.Fatalf("STOP-4 GW-FETCH-T6: download of %s: %v", gatewayBundleURL, err)
	}
	bundle, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("STOP-4 GW-FETCH-T6: download of %s: status %d, %d bytes, %v", gatewayBundleURL, resp.StatusCode, len(bundle), err)
	}
	sum := sha256.Sum256(bundle)
	got := hex.EncodeToString(sum[:])
	t.Logf("FA2-T6 bundle url=%s bytes=%d sha256=%s expected=%s result=%s", gatewayBundleURL, len(bundle), got, gatewayBundleSHA256, result(got == gatewayBundleSHA256))
	if got != gatewayBundleSHA256 {
		t.Fatalf("STOP-4 (section 8): the bundle sha256 is %s, want %s", got, gatewayBundleSHA256)
	}
	path := filepath.Join(t.TempDir(), "standard-install.yaml")
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	kubectl(t, nil, "create", "-f", path)
	var crds struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Group string `json:"group"`
			} `json:"spec"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	var established []string
	waitFor(t, 120*time.Second, "every bundle CRD Established", func() bool {
		if json.Unmarshal(kubectl(t, nil, "get", "crd", "-o", "json"), &crds) != nil {
			return false
		}
		established = nil
		all := true
		for _, c := range crds.Items {
			if c.Spec.Group != gatewayAPIGroup {
				continue
			}
			state := "Established=<none>"
			for _, cond := range c.Status.Conditions {
				if cond.Type == "Established" {
					state = "Established=" + cond.Status
				}
			}
			all = all && state == "Established=True"
			established = append(established, c.Metadata.Name+" "+state)
		}
		return all && len(established) > 0
	})
	waitFor(t, 90*time.Second, "discovery serving "+gatewayAPIGroup+"/v1 httproutes", func() bool {
		out, err := kubectlErr(nil, "get", "--raw", "/apis/"+gatewayAPIGroup+"/v1")
		return err == nil && strings.Contains(string(out), `"name":"httproutes"`)
	})
	createJSON(t, nsShop, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"checkout"},"spec":{"selector":{"app":"checkout"},"ports":[{"port":8080}]}}`, 0)
	createJSON(t, nsOther, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"checkout"},"spec":{"selector":{"app":"checkout"},"ports":[{"port":8080}]}}`, 0)
	createJSON(t, nsShop, `{"apiVersion":"example.com/v1","kind":"Service","metadata":{"name":"checkout"}}`, 60*time.Second)
	for _, r := range []string{
		`{"metadata":{"name":"shop"},"spec":{"hostnames":["shop.local","www.shop.local"],"rules":[{"matches":[{"path":{"type":"PathPrefix","value":"/"}},{"path":{"type":"Exact","value":"/v2"}}],"backendRefs":[{"name":"checkout","port":8080}]}]}}`,
		`{"metadata":{"name":"open"},"spec":{"rules":[{"backendRefs":[{"name":"checkout","port":8080}]}]}}`,
		`{"metadata":{"name":"lost"},"spec":{"rules":[{"backendRefs":[{"name":"gone","port":8080}]}]}}`,
		`{"metadata":{"name":"cross"},"spec":{"rules":[{"backendRefs":[{"namespace":"other","name":"checkout","port":8080}]}]}}`,
		`{"metadata":{"name":"custom"},"spec":{"rules":[{"backendRefs":[{"group":"example.com","kind":"Service","name":"checkout","port":8080}]}]}}`,
	} {
		createJSON(t, nsShop, `{"apiVersion":"gateway.networking.k8s.io/v1","kind":"HTTPRoute",`+r[1:], 60*time.Second)
	}
	roleAllBut(t, nsShop, saNoRoutes, "httproutes."+gatewayAPIGroup)
	roleAllBut(t, nsShop, saNoServices, "services")
	checkoutUID := rawUID(t, "/api/v1/namespaces/"+nsShop+"/services/checkout")
	customUID := rawUID(t, "/apis/example.com/v1/namespaces/"+nsShop+"/services/checkout")
	t.Logf("FA2-T6 read-back %s: core Service checkout uid %s, example.com Service checkout uid %s", nsShop, checkoutUID, customUID)
	phase(t, "c", "; bundle CRDs "+strings.Join(established, ", "))
	c := bridgeCall(t, admin, "admin "+nsShop, nsShop)
	routeOpen := []string{"/  * " + toRight + " checkout:8080"}
	t.Run("C6", func(t *testing.T) {
		checkB(t, "C6", "httproute-v1", c.ans,
			wantB{"httproute-service", "HTTPRoute/shop", "", nsShop, "Service", "checkout", checkoutUID,
				[]string{"/  shop.local,www.shop.local " + toRight + " checkout:8080", "/v2 (Exact)  shop.local,www.shop.local " + toRight + " checkout:8080"}, ""},
			wantB{"httproute-service", "HTTPRoute/open", "", nsShop, "Service", "checkout", checkoutUID, routeOpen, ""})
		t.Logf("FA2-T6 C6 input=httproute-v1 expected-unsupported=[] actual-unsupported=%+v result=%s", c.ans.Unsupported, result(len(c.ans.Unsupported) == 0))
		if len(c.ans.Unsupported) != 0 {
			t.Errorf("C6: unsupported %+v, want none", c.ans.Unsupported)
		}
	})
	t.Run("C10", func(t *testing.T) {
		checkB(t, "C10", "route-service-gone", c.ans,
			wantB{"httproute-service", "HTTPRoute/lost", "", nsShop, "Service", "gone", "", []string{"/  * " + toRight + " gone:8080"}, "Service gone not found"})
	})
	t.Run("C12", func(t *testing.T) {
		checkB(t, "C12", "cross-namespace", c.ans,
			wantB{"httproute-service", "HTTPRoute/cross", "", nsOther, "Service", "checkout", "", []string{"/  * " + toRight + " other/checkout:8080"},
				"Service other/checkout is in another namespace (not checked)"})
		noB(t, "C12", "cross-namespace-not-joined", c.ans, "httproute-service", "HTTPRoute/cross", nsShop, "Service", "checkout")
		checkB(t, "C12", "core-not-custom-service", c.ans,
			wantB{"httproute-service", "HTTPRoute/open", "", nsShop, "Service", "checkout", checkoutUID, routeOpen, ""})
		noB(t, "C12", "custom-group-backend", c.ans, "httproute-service", "HTTPRoute/custom", "", "", "")
	})
	t.Run("C9", func(t *testing.T) {
		if canI(t, nsShop, saNoRoutes, "list", "httproutes."+gatewayAPIGroup) != "no" || canI(t, nsShop, saNoRoutes, "list", "services") != "yes" {
			t.Fatal("preparation: the ServiceAccount no-routes does not have the permissions of C9")
		}
		p := saPanel(t, nsShop, saNoRoutes)
		r := bridgeCall(t, p, "no-routes "+nsShop, nsShop)
		routeBridges, notFound := 0, 0
		for _, br := range r.ans.Bridges {
			if br.Kind == "httproute-service" {
				routeBridges++
			}
			if strings.Contains(br.Text, "not found") {
				notFound++
			}
		}
		want := []string{"HTTPRoute 403 Couldn't load HTTPRoutes (403)"}
		pass := r.status == 200 && reflect.DeepEqual(r.ans.refusedList(), want) && routeBridges == 0 && notFound == 0 && len(r.ans.Unsupported) == 0
		t.Logf("FA2-T6 C9 input=httproutes-list-403 status=%d expected-refused=%q actual-refused=%q bridges=%d not-found-texts=%d unsupported=%+v result=%s",
			r.status, want, r.ans.refusedList(), routeBridges, notFound, r.ans.Unsupported, result(pass))
		if !pass {
			t.Errorf("C9: status %d, refused %q (want exactly %q), route bridges %d, not found %d, unsupported %+v", r.status, r.ans.refusedList(), want, routeBridges, notFound, r.ans.Unsupported)
		}
	})
	t.Run("C11", func(t *testing.T) {
		if canI(t, nsShop, saNoServices, "list", "services") != "no" || canI(t, nsShop, saNoServices, "list", "httproutes."+gatewayAPIGroup) != "yes" {
			t.Fatal("preparation: the ServiceAccount no-services does not have the permissions of C11")
		}
		p := saPanel(t, nsShop, saNoServices)
		r := bridgeCall(t, p, "no-services "+nsShop, nsShop)
		checkB(t, "C11", "services-list-403", r.ans,
			wantB{"httproute-service", "HTTPRoute/open", "", nsShop, "Service", "checkout", "", routeOpen, "no permission to read Services"})
		notFound := 0
		for _, br := range r.ans.Bridges {
			if strings.Contains(br.Text, "not found") {
				notFound++
			}
		}
		want := []string{"Service 403 Couldn't load Services (403)"}
		pass := reflect.DeepEqual(r.ans.refusedList(), want) && notFound == 0
		t.Logf("FA2-T6 C11 input=services-list-403 expected-refused=%q actual-refused=%q not-found-texts=%d result=%s", want, r.ans.refusedList(), notFound, result(pass))
		if !pass {
			t.Errorf("C11: refused %q, want exactly %q; not found texts %d, want 0", r.ans.refusedList(), want, notFound)
		}
	})
}
