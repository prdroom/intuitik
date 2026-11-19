package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Unit tests of the bridges (packet FA2-T6 sections 3 to 6, job unit). Each checked
// bridge prints one line:
//   FA2-T6 <criterion> input=<fixture> kind=<bridge kind> from=<kind/name> to=<ns/kind/name uid|null> expected=<settings joined by " | "> actual=<same> expected-to=<group>/<uid|null> actual-to=<same> [expected-text=<...> actual-text=<...>] result=<PASS|FAIL>
// with U+2192 and U+2190 printed as \u2192 and \u2190, so the log stays ASCII. The
// expected strings are written here independently of the product (section 3).

const (
	arrowRight = "\u2192"
	arrowLeft  = "\u2190"
	gwGroup    = "gateway.networking.k8s.io"
)

// esc writes the two arrows as their escapes.
func esc(s string) string {
	return strings.NewReplacer(arrowRight, `\u2192`, arrowLeft, `\u2190`).Replace(s)
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// objJSON is an object of testNS (obj) with the top-level fields of js; a "labels" field
// becomes its labels.
func objJSON(apiVersion, kind, name, uid, js string, refs ...metav1.OwnerReference) *unstructured.Unstructured {
	u := obj(apiVersion, kind, name, uid, refs...)
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		panic(fmt.Sprintf("fixture %s/%s: %v", kind, name, err))
	}
	for k, v := range m {
		if k == "labels" {
			labels := map[string]string{}
			for lk, lv := range v.(map[string]any) {
				labels[lk] = lv.(string)
			}
			u.SetLabels(labels)
			continue
		}
		u.Object[k] = v
	}
	return u
}

// wantBridge is one expected bridge. from is "<kind>/<name>" of a node; uid is the
// expected to.uid, "" for null.
type wantBridge struct {
	kind, from                string
	group, ns, toKind, toName string
	uid                       string
	settings                  []string
	text                      string
}

func nodeName(a Answer, uid string) string {
	for _, n := range a.Nodes {
		if n.UID == uid {
			return n.Kind + "/" + n.Name
		}
	}
	return "uid:" + uid
}

// bridgesOf are the bridges of kind from the node from to the object ns/toKind/toName
// (any group); toName "" matches every target.
func bridgesOf(a Answer, kind, from, ns, toKind, toName string) []Bridge {
	var out []Bridge
	for _, b := range a.Bridges {
		if b.Kind != kind || nodeName(a, b.From.UID) != from {
			continue
		}
		if toName != "" && (b.To.Namespace != ns || b.To.Kind != toKind || b.To.Name != toName) {
			continue
		}
		out = append(out, b)
	}
	return out
}

func uidText(u *string) string {
	if u == nil {
		return "null"
	}
	return *u
}

// checkBridges compares each expected bridge with the answer and prints its line.
func checkBridges(t *testing.T, criterion, input string, a Answer, ws ...wantBridge) {
	t.Helper()
	for _, w := range ws {
		got := bridgesOf(a, w.kind, w.from, w.ns, w.toKind, w.toName)
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
			b := got[0]
			actual, actualUID, text = strings.Join(b.Settings, " | "), uidText(b.To.UID), b.Text
			actualTo = b.To.Group + "/" + actualUID
			pass = reflect.DeepEqual(b.Settings, w.settings) && b.To.Group == w.group && actualUID == wantUID && b.Text == w.text
		default:
			actual = fmt.Sprintf("%d bridges", len(got))
		}
		line := fmt.Sprintf("FA2-T6 %s input=%s kind=%s from=%s to=%s/%s/%s %s expected=%s actual=%s expected-to=%s/%s actual-to=%s",
			criterion, input, w.kind, w.from, w.ns, w.toKind, w.toName, actualUID, esc(expected), esc(actual), w.group, wantUID, actualTo)
		if w.text != "" || text != "" {
			line += fmt.Sprintf(" expected-text=%s actual-text=%s", w.text, text)
		}
		t.Logf("%s result=%s", line, passFail(pass))
		if !pass {
			t.Errorf("FA2-T6 %s %s %s -> %s/%s/%s: got %q to %s text %q (%d bridges); want %q to %s/%s text %q",
				criterion, w.kind, w.from, w.ns, w.toKind, w.toName, actual, actualTo, text, len(got), expected, w.group, wantUID, w.text)
		}
	}
}

// noBridge checks that the node from has no bridge of kind to toKind/toName in ns (toName
// "" for any target).
func noBridge(t *testing.T, criterion, input string, a Answer, kind, from, ns, toKind, toName string) {
	t.Helper()
	got := bridgesOf(a, kind, from, ns, toKind, toName)
	target := ns + "/" + toKind + "/" + toName
	if toName == "" {
		target = "any"
	}
	t.Logf("FA2-T6 %s input=%s kind=%s from=%s to=%s expected=absent actual=%d bridges result=%s", criterion, input, kind, from, target, len(got), passFail(len(got) == 0))
	if len(got) != 0 {
		t.Errorf("FA2-T6 %s %s %s -> %s: %d bridges %+v, want none", criterion, kind, from, target, len(got), got)
	}
}

// checkNodes compares the place of each object with the answer (FA2-T6 line prefix).
func checkNodes(t *testing.T, criterion, input string, a Answer, ws ...want) {
	t.Helper()
	for _, w := range ws {
		parent, state, _, ok := place(a, w.kind, w.name)
		expected := fmt.Sprintf("%s/%s %s %s", w.kind, w.name, w.parent, w.state)
		actual := "absent"
		if ok {
			actual = fmt.Sprintf("%s/%s %s %s", w.kind, w.name, parent, state)
		}
		pass := expected == actual
		t.Logf("FA2-T6 %s input=%s expected=%s actual=%s result=%s", criterion, input, expected, actual, passFail(pass))
		if !pass {
			t.Errorf("FA2-T6 %s: got %s, want %s", criterion, actual, expected)
		}
	}
}

// gateway serves HTTPRoute in versions, the first one preferred.
func gateway(f *fakeAPI, versions ...string) {
	for _, v := range versions {
		f.crd(gwGroup, v, "HTTPRoute", "httproutes", versions[0])
	}
}

func route(version, name, uid, js string) *unstructured.Unstructured {
	return objJSON(gwGroup+"/"+version, "HTTPRoute", name, uid, js)
}

// trafficFixture: Ingresses, Services and Pods of C1 and C2.
func trafficFixture(f *fakeAPI) {
	f.add(
		objJSON("v1", "Service", "api", "svc-api", `{"spec":{"selector":{"app":"api","tier":"web"},"ports":[{"port":80,"targetPort":8080},{"port":53,"targetPort":53,"protocol":"UDP"}]}}`),
		objJSON("v1", "Service", "ext", "svc-ext", `{"spec":{"type":"ExternalName","externalName":"db.external.example","selector":{"app":"api"}}}`),
		objJSON("v1", "Service", "nosel", "svc-nosel", `{"spec":{"ports":[{"port":80}]}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "shop", "ing-shop", `{"spec":{"rules":[{"host":"shop.local","http":{"paths":[
			{"path":"/api","pathType":"Prefix","backend":{"service":{"name":"api","port":{"number":80}}}},
			{"path":"/healthz","pathType":"Exact","backend":{"service":{"name":"api","port":{"name":"http"}}}}]}}]}}`),
		objJSON("networking.k8s.io/v1", "Ingress", "plain", "ing-plain", `{"spec":{"defaultBackend":{"service":{"name":"api","port":{"number":80}}}}}`),
		obj("apps/v1", "Deployment", "api", "d-api"),
		objJSON("v1", "Pod", "api-1", "p-api-1", `{"labels":{"app":"api","tier":"web"}}`),
		objJSON("v1", "Pod", "api-2", "p-api-2", `{"labels":{"app":"api","tier":"web"}}`),
		objJSON("v1", "Pod", "api-3", "p-api-3", `{"labels":{"app":"api"}}`),
	)
}

// storageFixture: the StatefulSet, Pod and PVCs of C3.
func storageFixture(f *fakeAPI) {
	f.add(
		obj("apps/v1", "StatefulSet", "db", "s-db"),
		objJSON("v1", "Pod", "db-0", "p-db-0", `{"spec":{"volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"data-db-0"}}],
			"containers":[{"name":"db","image":"pause","volumeMounts":[{"name":"data","mountPath":"/var/lib/data"}]}],
			"initContainers":[{"name":"restore","image":"pause","volumeMounts":[{"name":"data","mountPath":"/backup","readOnly":true}]}]}}`,
			ctl("apps/v1", "StatefulSet", "db", "s-db")),
		objJSON("v1", "PersistentVolumeClaim", "data-db-0", "pvc-data", `{"spec":{"accessModes":["ReadWriteOnce"],"storageClassName":"standard","volumeName":"pv-1"},"status":{"capacity":{"storage":"1Gi"}}}`),
		objJSON("v1", "PersistentVolumeClaim", "c-default", "pvc-default", `{"spec":{"accessModes":["ReadWriteOnce"],"volumeName":"pv-2"}}`),
		objJSON("v1", "PersistentVolumeClaim", "c-none", "pvc-none", `{"spec":{"accessModes":["ReadWriteMany"],"storageClassName":"","volumeName":"pv-3"}}`),
	)
}

// configFixture: the Pod of C4 and C14 and the Secret and ConfigMap it names, with
// canary values; canaries are the Secret value, the ConfigMap value and the literal env.
func configFixture(f *fakeAPI) (canaries []string) {
	canaries = []string{"intuitik-unit-canary-secret-value", "intuitik-unit-canary-configmap-value", "intuitik-unit-canary-literal-env"}
	f.add(
		objJSON("v1", "Pod", "api", "p-api", fmt.Sprintf(`{"spec":{"containers":[{"name":"api","image":"pause",
			"env":[{"name":"DB_USER","valueFrom":{"secretKeyRef":{"name":"db","key":"username"}}},
				{"name":"LOG_LEVEL","valueFrom":{"configMapKeyRef":{"name":"app","key":"level"}}},
				{"name":"MODE","value":%q}],
			"envFrom":[{"prefix":"APP_","configMapRef":{"name":"app"}}],
			"volumeMounts":[{"name":"cfg","mountPath":"/etc/app"},{"name":"tls","mountPath":"/tls"}]}],
			"volumes":[{"name":"cfg","configMap":{"name":"app","items":[{"key":"app.yaml","path":"app.yaml"},{"key":"log.yaml","path":"log.yaml"}]}},
				{"name":"tls","projected":{"sources":[{"secret":{"name":"tls"}}]}}]}}`, canaries[2])),
		objJSON("v1", "Secret", "db", "sec-db", fmt.Sprintf(`{"data":{"username":%q}}`, canaries[0])),
		objJSON("v1", "Secret", "tls", "sec-tls", fmt.Sprintf(`{"data":{"tls.key":%q}}`, canaries[0])),
		objJSON("v1", "ConfigMap", "app", "cm-app", fmt.Sprintf(`{"data":{"level":%q,"app.yaml":%q}}`, canaries[1], canaries[1])),
	)
	return canaries
}

// hpaFixture: the HPAs and targets of C5.
func hpaFixture(f *fakeAPI) {
	f.add(
		objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "api", "hpa-api", `{"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"api"},"minReplicas":2,"maxReplicas":5}}`),
		objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "w", "hpa-w", `{"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"StatefulSet","name":"w"},"maxReplicas":3}}`),
		obj("apps/v1", "StatefulSet", "w", "s-w"),
	)
}

// routeFixture: the HTTPRoutes and Service of C6 at version.
func routeFixture(f *fakeAPI, version string) {
	f.add(
		objJSON("v1", "Service", "checkout", "svc-checkout", `{"spec":{"selector":{"app":"checkout"},"ports":[{"port":8080}]}}`),
		route(version, "shop", "rt-shop", `{"spec":{"hostnames":["shop.local","www.shop.local"],"rules":[{
			"matches":[{"path":{"type":"PathPrefix","value":"/"}},{"path":{"type":"Exact","value":"/v2"}}],
			"backendRefs":[{"name":"checkout","port":8080}]}]}}`),
		route(version, "open", "rt-open", `{"spec":{"rules":[{"backendRefs":[{"name":"checkout","port":8080}]}]}}`),
	)
}

var (
	wantIngress = []wantBridge{
		{BridgeIngressService, "Ingress/shop", "", testNS, "Service", "api", "svc-api",
			[]string{"/api  shop.local " + arrowRight + " api:80", "/healthz (Exact)  shop.local " + arrowRight + " api:http"}, `Service api has no port named "http"`},
		{BridgeIngressService, "Ingress/plain", "", testNS, "Service", "api", "svc-api",
			[]string{"(default)  * " + arrowRight + " api:80"}, ""},
	}
	serviceSettings = []string{"selector app=api,tier=web", ":80" + arrowRight + "8080", ":53" + arrowRight + "53/UDP"}
	wantService     = []wantBridge{
		{BridgeServicePod, "Service/api", "", testNS, "Pod", "api-1", "p-api-1", serviceSettings, "targetPort 8080 is not declared by the container (traffic not verifiable)"},
		{BridgeServicePod, "Service/api", "", testNS, "Pod", "api-2", "p-api-2", serviceSettings, "targetPort 8080 is not declared by the container (traffic not verifiable)"},
	}
	wantStorage = []wantBridge{
		{BridgePodPVC, "Pod/db-0", "", testNS, "PersistentVolumeClaim", "data-db-0", "pvc-data",
			[]string{"volume data-db-0", "mount db:/var/lib/data", "mount restore:/backup (read-only)", "access ReadWriteOnce"}, ""},
		{BridgePVCPV, "PersistentVolumeClaim/data-db-0", "", "", "PersistentVolume", "pv-1", "", []string{"class standard", "capacity 1Gi"}, "PersistentVolume pv-1 not found"},
		{BridgePVCPV, "PersistentVolumeClaim/c-default", "", "", "PersistentVolume", "pv-2", "", []string{"class (default)"}, "PersistentVolume pv-2 not found"},
		{BridgePVCPV, "PersistentVolumeClaim/c-none", "", "", "PersistentVolume", "pv-3", "", []string{"class (none)"}, "PersistentVolume pv-3 not found"},
	}
	wantConfig = []wantBridge{
		{BridgePodSecret, "Pod/api", "", testNS, "Secret", "db", "sec-db", []string{"env api:DB_USER " + arrowLeft + " username"}, ""},
		{BridgePodConfigMap, "Pod/api", "", testNS, "ConfigMap", "app", "cm-app",
			[]string{"env api:LOG_LEVEL " + arrowLeft + " level", "envFrom api prefix APP_", "volume api:/etc/app: app.yaml, log.yaml"}, ""},
		{BridgePodSecret, "Pod/api", "", testNS, "Secret", "tls", "sec-tls", []string{"volume api:/tls (all keys)"}, ""},
	}
	wantHPA = []wantBridge{
		{BridgeHPATarget, "HorizontalPodAutoscaler/api", "apps", testNS, "Deployment", "api", "d-api", []string{"min 2", "max 5"}, ""},
		{BridgeHPATarget, "HorizontalPodAutoscaler/w", "apps", testNS, "StatefulSet", "w", "s-w", []string{"min 1", "max 3"}, ""},
	}
	routeShop = []string{"/  shop.local,www.shop.local " + arrowRight + " checkout:8080", "/v2 (Exact)  shop.local,www.shop.local " + arrowRight + " checkout:8080"}
	wantRoute = []wantBridge{
		{BridgeHTTPRouteService, "HTTPRoute/shop", "", testNS, "Service", "checkout", "svc-checkout", routeShop, ""},
		{BridgeHTTPRouteService, "HTTPRoute/open", "", testNS, "Service", "checkout", "svc-checkout", []string{"/  * " + arrowRight + " checkout:8080"}, ""},
	}
)

// TestBridges_Ingress (C1): Ingress -> Service with host, path, path type and port.
func TestBridges_Ingress(t *testing.T) {
	f := newFake()
	trafficFixture(f)
	checkBridges(t, "C1", "ingress-shop-plain", mustBuild(t, f), wantIngress...)
}

// TestBridges_Service (C2): Service -> each Pod whose labels hold the whole selector; no
// bridge to a Pod with part of it, from an ExternalName Service or from a Service without
// selector.
func TestBridges_Service(t *testing.T) {
	f := newFake()
	trafficFixture(f)
	a := mustBuild(t, f)
	checkBridges(t, "C2", "service-api", a, wantService...)
	noBridge(t, "C2", "service-api-partial-labels", a, BridgeServicePod, "Service/api", testNS, "Pod", "api-3")
	noBridge(t, "C2", "service-externalname", a, BridgeServicePod, "Service/ext", "", "", "")
	noBridge(t, "C2", "service-no-selector", a, BridgeServicePod, "Service/nosel", "", "", "")
}

// TestBridges_Storage (C3): Pod -> PVC with the claim, every mount and the access modes;
// PVC -> PV with the class (also unset and empty) and the capacity.
func TestBridges_Storage(t *testing.T) {
	f := newFake()
	storageFixture(f)
	checkBridges(t, "C3", "statefulset-db-claims", mustBuild(t, f), wantStorage...)
}

// TestBridges_Config (C4): Pod -> ConfigMap and Pod -> Secret with key names and paths
// only, to.uid null.
func TestBridges_Config(t *testing.T) {
	f := newFake()
	configFixture(f)
	checkBridges(t, "C4", "pod-api-config", mustBuild(t, f), wantConfig...)
}

// TestBridges_ConfigOrder (section 3.1): references in containers then initContainers,
// env before envFrom, volumes after env, item keys in spec order, a Secret volume, an
// envFrom Secret without prefix and a volume that no container mounts.
func TestBridges_ConfigOrder(t *testing.T) {
	f := newFake().add(objJSON("v1", "Pod", "worker", "p-worker", `{"spec":{
		"containers":[{"name":"main","image":"pause","env":[{"name":"TOKEN","valueFrom":{"secretKeyRef":{"name":"creds","key":"token"}}}],
			"volumeMounts":[{"name":"certs","mountPath":"/certs","readOnly":true}]}],
		"initContainers":[{"name":"init","image":"pause","envFrom":[{"secretRef":{"name":"creds"}}],
			"volumeMounts":[{"name":"certs","mountPath":"/init-certs"}]}],
		"volumes":[{"name":"certs","secret":{"secretName":"creds","items":[{"key":"ca.crt","path":"ca.crt"},{"key":"tls.crt","path":"tls.crt"}]}},
			{"name":"spare","configMap":{"name":"extra"}}]}}`))
	checkBridges(t, "C4", "pod-worker-order", mustBuild(t, f),
		wantBridge{BridgePodSecret, "Pod/worker", "", testNS, "Secret", "creds", "", []string{
			"env main:TOKEN " + arrowLeft + " token", "envFrom init",
			"volume main:/certs: ca.crt, tls.crt", "volume init:/init-certs: ca.crt, tls.crt"}, "Secret creds not found"},
		wantBridge{BridgePodConfigMap, "Pod/worker", "", testNS, "ConfigMap", "extra", "", []string{"volume spare (not mounted)"}, "ConfigMap extra not found"})
}

// TestBridges_EphemeralClaim (E.4): a generic ephemeral volume names the claim
// <pod>-<volume>; with that PVC not a node, there is no access entry.
func TestBridges_EphemeralClaim(t *testing.T) {
	f := newFake().add(objJSON("v1", "Pod", "eph", "p-eph", `{"spec":{"containers":[{"name":"c","image":"pause",
		"volumeMounts":[{"name":"scratch","mountPath":"/scratch"}]}],
		"volumes":[{"name":"scratch","ephemeral":{"volumeClaimTemplate":{"spec":{"accessModes":["ReadWriteOnce"]}}}}]}}`))
	checkBridges(t, "C3", "pod-ephemeral-volume", mustBuild(t, f),
		wantBridge{BridgePodPVC, "Pod/eph", "", testNS, "PersistentVolumeClaim", "eph-scratch", "", []string{"volume eph-scratch", "mount c:/scratch"}, "eph-scratch waits to be created for Pod eph"})
}

// TestBridges_HPA (C5): HPA -> its target workload, group-exact, with min (1 when unset)
// and max.
func TestBridges_HPA(t *testing.T) {
	f := newFake()
	trafficFixture(f)
	hpaFixture(f)
	checkBridges(t, "C5", "hpa-api-w", mustBuild(t, f), wantHPA...)
}

// TestBridges_HTTPRoute (C6, B4): HTTPRoute -> Service with hostnames, matches and port,
// read at v1 and at v1beta1.
func TestBridges_HTTPRoute(t *testing.T) {
	for _, v := range []string{"v1", "v1beta1"} {
		t.Run(v, func(t *testing.T) {
			f := newFake()
			gateway(f, v)
			routeFixture(f, v)
			a := mustBuild(t, f)
			checkBridges(t, "C6", "httproute-"+v, a, wantRoute...)
			t.Logf("FA2-T6 C6 input=httproute-%s expected-unsupported=[] actual-unsupported=%v result=%s", v, a.Unsupported, passFail(len(a.Unsupported) == 0))
			if len(a.Unsupported) != 0 {
				t.Errorf("C6 %s: unsupported %+v, want none", v, a.Unsupported)
			}
		})
	}
}

// TestBridges_OutsideBoxes (C7, B7): a Service and a PVC owned by a StatefulSet are nodes
// directly under the namespace with owner state none; the StatefulSet holds its Pods only.
func TestBridges_OutsideBoxes(t *testing.T) {
	f := newFake().add(
		obj("apps/v1", "StatefulSet", "db", "s-db"),
		obj("v1", "Pod", "db-0", "p-db-0", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("v1", "Service", "db", "svc-db", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("v1", "PersistentVolumeClaim", "data-db-0", "pvc-data", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("networking.k8s.io/v1", "Ingress", "web", "ing-web", ctl("apps/v1", "StatefulSet", "db", "s-db")),
		obj("autoscaling/v2", "HorizontalPodAutoscaler", "db", "hpa-db", ctl("apps/v1", "StatefulSet", "db", "s-db")),
	)
	checkNodes(t, "C7", "owned-service-pvc", mustBuild(t, f),
		want{"StatefulSet", "db", "-", StateNone, ""},
		want{"Pod", "db-0", "StatefulSet/db", StateContained, ""},
		want{"Service", "db", "-", StateNone, ""},
		want{"PersistentVolumeClaim", "data-db-0", "-", StateNone, ""},
		want{"Ingress", "web", "-", StateNone, ""},
		want{"HorizontalPodAutoscaler", "db", "-", StateNone, ""})
}

// routeEntries describes the HTTPRoute parts of an answer for the failure lines.
func routeEntries(a Answer) (nodes, bridges int, refused []string, notFound int) {
	for _, n := range a.Nodes {
		if n.Kind == "HTTPRoute" {
			nodes++
		}
	}
	for _, b := range a.Bridges {
		if b.Kind == BridgeHTTPRouteService {
			bridges++
		}
		if strings.Contains(b.Text, "not found") {
			notFound++
		}
	}
	for _, r := range a.Refused {
		if r.Kind == "HTTPRoute" || r.Group == gwGroup {
			refused = append(refused, fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text))
		}
	}
	return
}

// TestBridges_NoGatewayAPI (C8, BT-1): with no Gateway API group in discovery the answer
// has no HTTPRoute node or bridge, unsupported is empty and nothing about HTTPRoutes is
// refused.
func TestBridges_NoGatewayAPI(t *testing.T) {
	f := newFake()
	trafficFixture(f)
	a := mustBuild(t, f)
	nodes, bridges, refused, _ := routeEntries(a)
	pass := nodes == 0 && bridges == 0 && len(refused) == 0 && len(a.Unsupported) == 0
	t.Logf("FA2-T6 C8 input=no-gateway-group expected=nodes=0 bridges=0 refused=[] unsupported=[] actual=nodes=%d bridges=%d refused=%q unsupported=%+v result=%s",
		nodes, bridges, refused, a.Unsupported, passFail(pass))
	if !pass {
		t.Errorf("C8: HTTPRoute nodes %d, bridges %d, refused %q, unsupported %+v; want none", nodes, bridges, refused, a.Unsupported)
	}
}

// TestBridges_RouteListRefused (C9, BT-2): a refused HTTPRoutes LIST is the one GA-4
// entry, never "not found" and never an unsupported entry.
func TestBridges_RouteListRefused(t *testing.T) {
	f := newFake()
	gateway(f, "v1")
	routeFixture(f, "v1")
	f.lists[gwGroup+"/httproutes"] = fail{status: 403}
	a := mustBuild(t, f)
	nodes, bridges, refused, notFound := routeEntries(a)
	want := []string{"HTTPRoute 403 Couldn't load HTTPRoutes (403)"}
	pass := reflect.DeepEqual(refused, want) && nodes == 0 && bridges == 0 && notFound == 0 && len(a.Unsupported) == 0
	t.Logf("FA2-T6 C9 input=httproutes-list-403 expected-refused=%q actual-refused=%q nodes=%d bridges=%d not-found-texts=%d unsupported=%+v result=%s",
		want, refused, nodes, bridges, notFound, a.Unsupported, passFail(pass))
	if !pass {
		t.Errorf("C9: refused %q (want exactly %q), nodes %d, bridges %d, not found %d, unsupported %+v", refused, want, nodes, bridges, notFound, a.Unsupported)
	}
}

// TestBridges_RouteTargetMissing (C10, BT-3): the bridge to a Service that does not exist
// stays, with to.uid null and its text.
func TestBridges_RouteTargetMissing(t *testing.T) {
	f := newFake()
	gateway(f, "v1")
	f.add(route("v1", "lost", "rt-lost", `{"spec":{"rules":[{"backendRefs":[{"name":"gone","port":8080}]}]}}`))
	checkBridges(t, "C10", "route-service-gone", mustBuild(t, f),
		wantBridge{BridgeHTTPRouteService, "HTTPRoute/lost", "", testNS, "Service", "gone", "", []string{"/  * " + arrowRight + " gone:8080"}, "Service gone not found"})
}

// TestBridges_RouteTargetNotReadable (C11, BT-4, B3): with the Services LIST refused the
// bridge stays with "no permission to read Services", never "not found"; any other
// failure of that LIST fails the whole call, and a stopped call stays stopped.
func TestBridges_RouteTargetNotReadable(t *testing.T) {
	fixture := func() *fakeAPI {
		f := newFake()
		gateway(f, "v1")
		routeFixture(f, "v1")
		return f
	}
	t.Run("403", func(t *testing.T) {
		f := fixture()
		f.lists["/services"] = fail{status: 403}
		a := mustBuild(t, f)
		checkBridges(t, "C11", "services-list-403", a,
			wantBridge{BridgeHTTPRouteService, "HTTPRoute/open", "", testNS, "Service", "checkout", "", []string{"/  * " + arrowRight + " checkout:8080"}, "no permission to read Services"})
		_, _, _, notFound := routeEntries(a)
		var refused []string
		for _, r := range a.Refused {
			if r.Kind == "Service" {
				refused = append(refused, fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text))
			}
		}
		want := []string{"Service 403 Couldn't load Services (403)"}
		pass := reflect.DeepEqual(refused, want) && notFound == 0
		t.Logf("FA2-T6 C11 input=services-list-403 expected-refused=%q actual-refused=%q not-found-texts=%d result=%s", want, refused, notFound, passFail(pass))
		if !pass {
			t.Errorf("C11: refused %q, want %q; not found texts %d, want 0", refused, want, notFound)
		}
	})
	for _, c := range []struct {
		name   string
		answer fail
		text   string
	}{
		{"500", fail{status: 500}, "Couldn't load Services in " + testNS + " (500)"},
		{"no-answer", fail{noAnswer: true}, "Couldn't load Services in " + testNS + " (no answer)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fixture()
			f.lists["/services"] = c.answer
			a, err := f.build(t)
			var le *LoadError
			got := ""
			if errors.As(err, &le) {
				got = le.Error()
			}
			pass := got == c.text && len(a.Nodes) == 0 && len(a.Bridges) == 0
			t.Logf("FA2-T6 C11 input=services-list-%s expected=error-no-nodes-no-bridges actual=error=%v nodes=%d bridges=%d expected-text=%s actual-text=%s result=%s",
				c.name, err != nil, len(a.Nodes), len(a.Bridges), c.text, got, passFail(pass))
			if !pass {
				t.Errorf("C11 %s: error %v, %d nodes, %d bridges; want %q and no node or bridge", c.name, err, len(a.Nodes), len(a.Bridges), c.text)
			}
		})
	}
	t.Run("stopped", func(t *testing.T) {
		probe := fixture()
		mustBuild(t, probe)
		at := 0
		for i, r := range probe.requests {
			if r == "LIST /v1/services "+testNS {
				at = i + 1
			}
		}
		f := fixture()
		f.stopAfter = at
		a, err := f.build(t)
		sent := f.count("LIST /v1/services")
		pass := at > 0 && errors.Is(err, ErrStopped) && len(a.Nodes) == 0 && len(a.Bridges) == 0 && sent == 0
		t.Logf("FA2-T6 C11 input=stopped-at-services-list open=%d expected=ErrStopped no-nodes actual=error=%v nodes=%d bridges=%d services-lists=%d result=%s",
			at, err, len(a.Nodes), len(a.Bridges), sent, passFail(pass))
		if !pass {
			t.Errorf("C11 stopped: open %d, error %v, %d nodes, %d bridges, %d Services LISTs; want ErrStopped and nothing else", at, err, len(a.Nodes), len(a.Bridges), sent)
		}
	})
}

// TestBridges_GroupExact (C12, BT-5, B5): a backendRef to another namespace is shown with
// its text and never joined to the Service of the same name here; a core Service target
// is never the custom kind named Service; a backendRef of another group gives no bridge;
// an HPA target is joined in its own group only.
func TestBridges_GroupExact(t *testing.T) {
	f := newFake()
	gateway(f, "v1")
	f.crd("example.com", "v1", "Service", "services", "v1")
	f.crd("example.com", "v1", "Deployment", "deployments", "v1")
	f.add(
		objJSON("v1", "Service", "checkout", "svc-checkout", `{"spec":{"ports":[{"port":8080}]}}`),
		obj("example.com/v1", "Service", "checkout", "cr-checkout"),
		route("v1", "cross", "rt-cross", `{"spec":{"rules":[{"backendRefs":[{"namespace":"other","name":"checkout","port":8080}]}]}}`),
		route("v1", "local", "rt-local", `{"spec":{"rules":[{"backendRefs":[{"name":"checkout","port":8080}]}]}}`),
		route("v1", "custom", "rt-custom", `{"spec":{"rules":[{"backendRefs":[{"group":"example.com","kind":"Service","name":"checkout","port":8080}]}]}}`),
		obj("apps/v1", "Deployment", "api", "d-api"),
		obj("example.com/v1", "Deployment", "api", "x-api"),
		objJSON("autoscaling/v2", "HorizontalPodAutoscaler", "xapi", "hpa-xapi", `{"spec":{"scaleTargetRef":{"apiVersion":"example.com/v1","kind":"Deployment","name":"api"},"minReplicas":1,"maxReplicas":2}}`),
	)
	a := mustBuild(t, f)
	checkBridges(t, "C12", "cross-namespace", a,
		wantBridge{BridgeHTTPRouteService, "HTTPRoute/cross", "", "other", "Service", "checkout", "", []string{"/  * " + arrowRight + " other/checkout:8080"},
			"Service other/checkout is in another namespace (not checked)"})
	noBridge(t, "C12", "cross-namespace-not-joined", a, BridgeHTTPRouteService, "HTTPRoute/cross", testNS, "Service", "checkout")
	checkBridges(t, "C12", "core-not-custom-service", a,
		wantBridge{BridgeHTTPRouteService, "HTTPRoute/local", "", testNS, "Service", "checkout", "svc-checkout", []string{"/  * " + arrowRight + " checkout:8080"}, ""})
	noBridge(t, "C12", "custom-group-backend", a, BridgeHTTPRouteService, "HTTPRoute/custom", "", "", "")
	checkBridges(t, "C12", "hpa-custom-group", a,
		wantBridge{BridgeHPATarget, "HorizontalPodAutoscaler/xapi", "example.com", testNS, "Deployment", "api", "x-api", []string{"min 1", "max 2"}, ""})
}

// TestBridges_UnsupportedVersion (C13, BT-6, B4): routes served only at v1alpha2 are nodes
// with no bridge and one unsupported entry; with v1 or v1beta1 chosen the bridges are made.
func TestBridges_UnsupportedVersion(t *testing.T) {
	f := newFake()
	gateway(f, "v1alpha2")
	routeFixture(f, "v1alpha2")
	a := mustBuild(t, f)
	nodes, bridges, _, _ := routeEntries(a)
	want := []Unsupported{{Group: gwGroup, Kind: "HTTPRoute", Version: "v1alpha2", Text: "Unsupported version v1alpha2"}}
	pass := nodes == 2 && bridges == 0 && reflect.DeepEqual(a.Unsupported, want)
	t.Logf("FA2-T6 C13 input=v1alpha2-only expected=nodes=2 bridges=0 unsupported=%+v actual=nodes=%d bridges=%d unsupported=%+v expected-text=%s actual-text=%s result=%s",
		want, nodes, bridges, a.Unsupported, want[0].Text, unsupportedText(a), passFail(pass))
	if !pass {
		t.Errorf("C13: nodes %d, bridges %d, unsupported %+v; want 2, 0, %+v", nodes, bridges, a.Unsupported, want)
	}
	for _, c := range []struct{ name, preferred string }{{"v1-preferred", "v1"}, {"v1beta1-preferred", "v1beta1"}} {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			versions := []string{c.preferred, "v1alpha2"}
			if c.preferred == "v1" {
				versions = []string{"v1", "v1beta1", "v1alpha2"}
			}
			gateway(f, versions...)
			routeFixture(f, c.preferred)
			a := mustBuild(t, f)
			checkBridges(t, "C13", "served-"+strings.Join(versions, ","), a, wantRoute...)
			t.Logf("FA2-T6 C13 input=served-%s expected-unsupported=[] actual-unsupported=%+v result=%s", strings.Join(versions, ","), a.Unsupported, passFail(len(a.Unsupported) == 0))
			if len(a.Unsupported) != 0 {
				t.Errorf("C13 %s: unsupported %+v, want none", c.name, a.Unsupported)
			}
		})
	}
}

func unsupportedText(a Answer) string {
	if len(a.Unsupported) == 0 {
		return ""
	}
	return a.Unsupported[0].Text
}

// TestBridges_NoValueNoRead (C14, B1, B2): no canary value in the answer, with a positive
// control in the fixture, and no LIST of Secrets or ConfigMaps and no GET of one in the
// call (the fixture has no owner reference to either).
func TestBridges_NoValueNoRead(t *testing.T) {
	f := newFake()
	canaries := configFixture(f)
	a, err := f.build(t)
	if err != nil {
		t.Errorf("C14: Build: %v", err)
	}
	body, _ := json.Marshal(a)
	fixtureBody, _ := json.Marshal(f.objects)
	for i, c := range canaries {
		found, control := strings.Count(string(body), c), strings.Count(string(fixtureBody), c)
		pass := found == 0 && control > 0
		t.Logf("FA2-T6 C14 input=pod-api-config canary=%d in the answer %d, positive control in the fixture %d (want 0, at least 1) result=%s", i+1, found, control, passFail(pass))
		if !pass {
			t.Errorf("C14 canary %d: in the answer %d, in the fixture %d", i+1, found, control)
		}
	}
	var reads []string
	for _, r := range f.requests {
		for _, p := range []string{"GET /v1/configmaps ", "GET /v1/secrets ", "LIST /v1/configmaps ", "LIST /v1/secrets "} {
			if strings.HasPrefix(r, p) {
				reads = append(reads, r)
			}
		}
	}
	t.Logf("FA2-T6 C14 input=pod-api-config forbidden reads of ConfigMaps or Secrets expected=[] actual=%q result=%s", reads, passFail(len(reads) == 0))
	if len(reads) != 0 {
		t.Errorf("C14: the call read %q; no ConfigMap or Secret may be read for a bridge (B1)", reads)
	}
}

// fullFixture holds the objects of C1 to C6.
func fullFixture() *fakeAPI {
	f := newFake()
	gateway(f, "v1")
	trafficFixture(f)
	storageFixture(f)
	configFixture(f)
	hpaFixture(f)
	routeFixture(f, "v1")
	return f
}

// TestBridges_Stable (C15, B6): the same objects in two LIST orders give byte-identical
// JSON; bridges follow the B6 order.
func TestBridges_Stable(t *testing.T) {
	first := mustBuild(t, fullFixture())
	f := fullFixture()
	f.reversed = true
	second := mustBuild(t, f)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	t.Logf("FA2-T6 C15 input=c1-to-c6-two-orders answer bytes %d and %d, bridges %d, identical %v result=%s", len(a), len(b), len(first.Bridges), string(a) == string(b), passFail(string(a) == string(b)))
	if string(a) != string(b) {
		t.Errorf("C15: answers differ:\n%s\n%s", a, b)
	}
	key := func(x Bridge) []string {
		return []string{nodeKind(first, x.From.UID), nodeShort(first, x.From.UID), x.Kind, x.To.Kind, x.To.Name, strings.Join(x.Settings, "\x00")}
	}
	ordered := sort.SliceIsSorted(first.Bridges, func(i, j int) bool {
		ki, kj := key(first.Bridges[i]), key(first.Bridges[j])
		for n := range ki {
			if ki[n] != kj[n] {
				return ki[n] < kj[n]
			}
		}
		return false
	})
	t.Logf("FA2-T6 C15 input=c1-to-c6 bridges in B6 order (from kind, from name, kind, to kind, to name, settings): %v result=%s", ordered, passFail(ordered && len(first.Bridges) > 0))
	if !ordered || len(first.Bridges) == 0 {
		t.Errorf("C15: %d bridges, B6 order %v", len(first.Bridges), ordered)
	}
}

func nodeKind(a Answer, uid string) string {
	k, _, _ := strings.Cut(nodeName(a, uid), "/")
	return k
}

func nodeShort(a Answer, uid string) string {
	_, n, _ := strings.Cut(nodeName(a, uid), "/")
	return n
}

// TestBridges_OptionalFlag (C4, E.4, review 5969050058 F1): each settings entry of a Pod ->
// ConfigMap or Pod -> Secret bridge keeps the optional flag of the reference it comes from
// (env, envFrom, volume, projected source; unset is false), outside the settings text; a
// bridge of another kind has no flags.
func TestBridges_OptionalFlag(t *testing.T) {
	f := newFake().add(objJSON("v1", "Pod", "opt", "p-opt", `{"spec":{
		"containers":[{"name":"c","image":"pause",
			"env":[{"name":"A","valueFrom":{"configMapKeyRef":{"name":"cfg","key":"a","optional":true}}},
				{"name":"B","valueFrom":{"configMapKeyRef":{"name":"cfg","key":"b"}}}],
			"envFrom":[{"secretRef":{"name":"sec","optional":false}}],
			"volumeMounts":[{"name":"v1","mountPath":"/v1"},{"name":"v2","mountPath":"/v2"},{"name":"data","mountPath":"/data"}]}],
		"volumes":[{"name":"v1","configMap":{"name":"cfg","optional":true}},
			{"name":"v2","projected":{"sources":[{"secret":{"name":"sec","optional":true}}]}},
			{"name":"data","persistentVolumeClaim":{"claimName":"data-opt"}}]}}`))
	a := mustBuild(t, f)
	checkBridges(t, "C4", "pod-opt-optional", a,
		wantBridge{BridgePodConfigMap, "Pod/opt", "", testNS, "ConfigMap", "cfg", "",
			[]string{"env c:A " + arrowLeft + " a", "env c:B " + arrowLeft + " b", "volume c:/v1 (all keys)"}, "ConfigMap cfg not found"},
		wantBridge{BridgePodSecret, "Pod/opt", "", testNS, "Secret", "sec", "", []string{"envFrom c", "volume c:/v2 (all keys)"}, "Secret sec not found"})
	for _, c := range []struct {
		kind, toKind, toName string
		want                 []bool
	}{
		{BridgePodConfigMap, "ConfigMap", "cfg", []bool{true, false, true}},
		{BridgePodSecret, "Secret", "sec", []bool{false, true}},
		{BridgePodPVC, "PersistentVolumeClaim", "data-opt", nil},
	} {
		var got []bool
		bs := bridgesOf(a, c.kind, "Pod/opt", testNS, c.toKind, c.toName)
		if len(bs) == 1 {
			got = bs[0].Optional
		}
		pass := len(bs) == 1 && reflect.DeepEqual(got, c.want)
		t.Logf("FA2-T6 C4 input=pod-opt-optional kind=%s from=Pod/opt to=%s/%s/%s expected-optional=%v actual-optional=%v bridges=%d result=%s",
			c.kind, testNS, c.toKind, c.toName, c.want, got, len(bs), passFail(pass))
		if !pass {
			t.Errorf("E.4 optional %s -> %s/%s: %d bridges, flags %v, want %v", c.kind, c.toKind, c.toName, len(bs), got, c.want)
		}
	}
	body, _ := json.Marshal(a)
	t.Logf("FA2-T6 C4 input=pod-opt-optional the word optional in a settings entry: %v (want false)", strings.Contains(fmt.Sprint(a.Bridges), "optional"))
	if strings.Count(string(body), `"optional":`) != 2 {
		t.Errorf("E.4 optional: %d optional fields in the answer, want 2 (the two config bridges only)", strings.Count(string(body), `"optional":`))
	}
	for _, b := range a.Bridges {
		for _, s := range b.Settings {
			if strings.Contains(s, "optional") {
				t.Errorf("E.4 optional: settings entry %q carries the flag as text", s)
			}
		}
	}
}

// TestBridges_RouteTargetOwnerFetched (C11, BT-4, B3, review 5969050058 F2): with the
// Services LIST refused, a route target stays not checkable even when an owner lookup of
// the call read that Service: to.uid null and the BT-4 text, never its uid.
func TestBridges_RouteTargetOwnerFetched(t *testing.T) {
	f := newFake()
	gateway(f, "v1")
	f.add(
		objJSON("v1", "Service", "checkout", "svc-checkout", `{"spec":{"ports":[{"port":8080}]}}`),
		obj("v1", "Pod", "held", "p-held", ctl("v1", "Service", "checkout", "svc-checkout")),
		route("v1", "open", "rt-open", `{"spec":{"rules":[{"backendRefs":[{"name":"checkout","port":8080}]}]}}`),
	)
	f.lists["/services"] = fail{status: 403}
	a := mustBuild(t, f)
	gets := f.count("GET /v1/services " + testNS + "/checkout")
	parent, _, _, _ := place(a, "Pod", "held")
	t.Logf("FA2-T6 C11 input=services-list-403-owner-fetched owner GETs of Service checkout %d (want 1), Pod held parent %s (want Service/checkout)", gets, parent)
	if gets != 1 || parent != "Service/checkout" {
		t.Errorf("C11 owner-fetched: %d owner GETs, parent %s; the fixture must read Service checkout as an owner", gets, parent)
	}
	checkBridges(t, "C11", "services-list-403-owner-fetched", a,
		wantBridge{BridgeHTTPRouteService, "HTTPRoute/open", "", testNS, "Service", "checkout", "", []string{"/  * " + arrowRight + " checkout:8080"}, "no permission to read Services"})
}
