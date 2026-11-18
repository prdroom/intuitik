//go:build kind

package graph

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// TestG_Containment (C1 to C5): the containment of controller-made workloads, of custom
// resources and of objects without an owner, read by the kind admin.
func TestG_Containment(t *testing.T) {
	fixtures(t)
	settled(t, nsWork)
	settled(t, nsCustom)
	rss, pods := objects(t, nsWork, "replicasets"), objects(t, nsWork, "pods")
	webRS := 0
	for _, r := range rss {
		if expectedOwner(r) == "Deployment/web" {
			webRS++
		}
	}
	if webRS != 2 {
		t.Fatalf("preparation: %d ReplicaSets of Deployment web, want 2 (current and previous revision)", webRS)
	}
	p := adminPanel(t)
	status, work, _ := p.graph(t, "admin "+nsWork, nsWork)
	_, custom, _ := p.graph(t, "admin "+nsCustom, nsCustom)
	if status != 200 {
		t.Errorf("C1-C5: status %d, want 200", status)
	}
	// owned lists the expectations for the objects of kind whose controller reference is of
	// ownerKind with a name in names.
	owned := func(items []object, kind, ownerKind string, names map[string]bool) []want {
		var ws []want
		for _, o := range items {
			e := expectedOwner(o)
			k, n, _ := strings.Cut(e, "/")
			if k == ownerKind && names[n] {
				ws = append(ws, want{kind, o.Metadata.Name, e, "contained", ""})
			}
		}
		return ws
	}
	t.Run("deployment", func(t *testing.T) {
		ws := []want{{"Deployment", "web", "-", "none", ""}}
		names := map[string]bool{}
		for _, r := range rss {
			if expectedOwner(r) == "Deployment/web" {
				names[r.Metadata.Name] = true
				ws = append(ws, want{"ReplicaSet", r.Metadata.Name, "Deployment/web", "contained", ""})
			}
		}
		check(t, "C1", "deployment-web-two-revisions", work, append(ws, owned(pods, "Pod", "ReplicaSet", names)...)...)
	})
	t.Run("statefulset", func(t *testing.T) {
		ws := append([]want{{"StatefulSet", "db", "-", "none", ""}}, owned(pods, "Pod", "StatefulSet", map[string]bool{"db": true})...)
		check(t, "C2", "statefulset-db", work, ws...)
	})
	t.Run("daemonset", func(t *testing.T) {
		ws := append([]want{{"DaemonSet", "agent", "-", "none", ""}}, owned(pods, "Pod", "DaemonSet", map[string]bool{"agent": true})...)
		check(t, "C2", "daemonset-agent", work, ws...)
		revisions := 0
		for _, n := range work.Nodes {
			if n.Kind == "ControllerRevision" {
				revisions++
			}
		}
		t.Logf("FA2-T5 C2 input=statefulset-daemonset ControllerRevision nodes=%d (want 0) result=%s", revisions, map[bool]string{true: "PASS", false: "FAIL"}[revisions == 0])
		if revisions != 0 {
			t.Errorf("C2: %d ControllerRevision nodes", revisions)
		}
	})
	t.Run("cronjob", func(t *testing.T) {
		ws := append([]want{{"CronJob", "report", "-", "none", ""}, {"Job", "report-1", "CronJob/report", "contained", ""}},
			owned(pods, "Pod", "Job", map[string]bool{"report-1": true})...)
		check(t, "C3", "cronjob-report", work, ws...)
	})
	t.Run("custom-owner", func(t *testing.T) {
		crs, cpods := objects(t, nsCustom, "replicasets"), objects(t, nsCustom, "pods")
		ws := []want{
			{"Widget", "w1", "-", "none", ""}, {"Widget", "w2", "-", "none", ""}, {"Widget", "w4", "-", "none", ""},
			{"Deployment", "api", "Widget/w1", "contained", ""}, {"Widget", "w3", "Deployment/api", "contained", ""},
			{"Pod", "lone", "Widget/w2", "contained", ""},
		}
		ws = append(ws, owned(crs, "ReplicaSet", "Deployment", map[string]bool{"api": true})...)
		apiRS := map[string]bool{}
		for _, r := range crs {
			if expectedOwner(r) == "Deployment/api" {
				apiRS[r.Metadata.Name] = true
			}
		}
		ws = append(ws, owned(cpods, "Pod", "ReplicaSet", apiRS)...)
		check(t, "C4", "custom-owner", custom, ws...)
	})
	t.Run("no-owner", func(t *testing.T) {
		ws := append([]want{{"Pod", "debug", "-", "none", ""}, {"Job", "once", "-", "none", ""}},
			owned(pods, "Pod", "Job", map[string]bool{"once": true})...)
		check(t, "C5", "no-owner", work, ws...)
	})
}

// readBack reads one fixture object again and prints that it exists with its owner
// reference (D.6): absence is never the expected output.
func readBack(t *testing.T, ns, resource, name string) object {
	t.Helper()
	out, err := kubectlErr(nil, "get", resource, name, "-n", ns, "-o", "json")
	var o object
	if err != nil || json.Unmarshal(out, &o) != nil {
		t.Fatalf("preparation: fixture %s %s/%s is gone before the request (%v); the garbage collector or something else removed it", resource, ns, name, err)
	}
	var refs []string
	for _, r := range o.Metadata.OwnerReferences {
		refs = append(refs, fmt.Sprintf("%s %s/%s uid=%s", r.APIVersion, r.Kind, r.Name, r.UID))
	}
	t.Logf("FA2-T5 read-back %s %s/%s uid=%s exists, ownerReferences %v", resource, ns, name, o.Metadata.UID, refs)
	return o
}

// TestG_OwnerMissing (C6, C7, C8): on the graph cluster with the garbage collector
// controller off, a ReplicaSet whose Deployment does not exist, a pod whose ConfigMap owner
// was recreated with another UID, and a pod whose owner kind is not served.
func TestG_OwnerMissing(t *testing.T) {
	fixtures(t)
	settled(t, nsMissing)
	p := adminPanel(t)
	orphan := readBack(t, nsMissing, "replicaset", "orphan")
	child := readBack(t, nsMissing, "pod", "child-recreated")
	holder := readBack(t, nsMissing, "configmap", "holder")
	readBack(t, nsMissing, "pod", "gadget-child")
	if len(orphan.Metadata.OwnerReferences) != 1 || len(child.Metadata.OwnerReferences) != 1 || child.Metadata.OwnerReferences[0].UID == holder.Metadata.UID {
		t.Fatalf("preparation: the dangling references are not in place")
	}
	t.Logf("FA2-T5 read-back ConfigMap holder uid %s, the reference of child-recreated names uid %s", holder.Metadata.UID, child.Metadata.OwnerReferences[0].UID)
	_, ans, _ := p.graph(t, "admin "+nsMissing, nsMissing)
	pods := objects(t, nsMissing, "pods")
	t.Run("gone", func(t *testing.T) {
		ws := []want{{"ReplicaSet", "orphan", "-", "owner-missing", fmt.Sprintf(textMissing, "Deployment", "gone")}}
		for _, o := range pods {
			if expectedOwner(o) == "ReplicaSet/orphan" {
				ws = append(ws, want{"Pod", o.Metadata.Name, "ReplicaSet/orphan", "contained", ""})
			}
		}
		check(t, "C6", "deployment-gone", ans, ws...)
	})
	t.Run("recreated", func(t *testing.T) {
		check(t, "C7", "configmap-recreated", ans, want{"Pod", "child-recreated", "-", "owner-missing", fmt.Sprintf(textMissing, "ConfigMap", "holder")})
	})
	t.Run("unserved", func(t *testing.T) {
		check(t, "C8", "kind-not-served", ans, want{"Pod", "gadget-child", "-", "owner-missing", fmt.Sprintf(textMissing, "Gadget", "g1")})
	})
}

// canI prints what a ServiceAccount may do, as the API server decides it.
func canI(t *testing.T, ns, sa, verb, resource string) string {
	t.Helper()
	out, _ := kubectlErr(nil, "auth", "can-i", verb, resource, "-n", ns, "--as=system:serviceaccount:"+ns+":"+sa)
	got := strings.TrimSpace(string(out))
	t.Logf("FA2-T5 permission %s may %s %s in %s: %s", sa, verb, resource, ns, got)
	return got
}

// TestG_OwnerForbidden (C9, C10): a ServiceAccount refused (403) on the owner's type sees
// owner-forbidden, whether the owner exists or not, and never owner-missing.
func TestG_OwnerForbidden(t *testing.T) {
	fixtures(t)
	settled(t, nsForbidden)
	settled(t, nsMiddle)
	forbidden := textForbidden
	if canI(t, nsForbidden, saPodsOnly, "get", "replicasets") != "no" || canI(t, nsForbidden, saPodsOnly, "list", "pods") != "yes" {
		t.Fatal("preparation: the ServiceAccount pods-only does not have the permissions of C9")
	}
	p := saPanel(t, nsForbidden, saPodsOnly)
	_, ans, _ := p.graph(t, "pods-only "+nsForbidden, nsForbidden)
	var rsaPods []want
	for _, o := range objects(t, nsForbidden, "pods") {
		if expectedOwner(o) == "ReplicaSet/rs-a" {
			rsaPods = append(rsaPods, want{"Pod", o.Metadata.Name, "-", "owner-forbidden", forbidden})
		}
	}
	if len(rsaPods) != 1 {
		t.Fatalf("preparation: %d pods of rs-a, want 1", len(rsaPods))
	}
	check(t, "C9", "owner-exists-403", ans, rsaPods...)
	check(t, "C9", "owner-missing-403", ans, want{"Pod", "pod-b", "-", "owner-forbidden", forbidden})
	t.Run("middle", func(t *testing.T) {
		if canI(t, nsMiddle, saNoDeploy, "get", "deployments") != "no" || canI(t, nsMiddle, saNoDeploy, "get", "replicasets") != "yes" {
			t.Fatal("preparation: the ServiceAccount no-deploy does not have the permissions of C10")
		}
		m := saPanel(t, nsMiddle, saNoDeploy)
		_, mid, _ := m.graph(t, "no-deploy "+nsMiddle, nsMiddle)
		var ws []want
		var rs string
		for _, r := range objects(t, nsMiddle, "replicasets") {
			rs = r.Metadata.Name
			ws = append(ws, want{"ReplicaSet", rs, "-", "owner-forbidden", forbidden})
		}
		for _, o := range objects(t, nsMiddle, "pods") {
			ws = append(ws, want{"Pod", o.Metadata.Name, "ReplicaSet/" + rs, "contained", ""})
		}
		check(t, "C10", "deployment-403", mid, ws...)
	})
}

// TestG_ListRefused (C17, GA-4): the LIST of ReplicaSets is refused; the answer is 200
// with the Pods and the refusal, never silent and never owner-missing.
func TestG_ListRefused(t *testing.T) {
	fixtures(t)
	settled(t, nsForbidden)
	if canI(t, nsForbidden, saPodsOnly, "list", "replicasets") != "no" {
		t.Fatal("preparation: the ServiceAccount pods-only may list ReplicaSets")
	}
	p := saPanel(t, nsForbidden, saPodsOnly)
	status, ans, _ := p.graph(t, "pods-only "+nsForbidden, nsForbidden)
	got := ""
	for _, r := range ans.Refused {
		if r.Kind == "ReplicaSet" {
			got = fmt.Sprintf("%s %d %s", r.Kind, r.Status, r.Text)
		}
	}
	want := "ReplicaSet 403 " + fmt.Sprintf(textKinds, "ReplicaSets", 403)
	pass := status == 200 && got == want
	t.Logf("FA2-T5 C17 input=replicasets-list-403 status=%d expected-refused=%s actual-refused=%s result=%s", status, want, got, map[bool]string{true: "PASS", false: "FAIL"}[pass])
	if !pass {
		t.Errorf("C17: status %d, refused %q; want 200 and %q", status, got, want)
	}
	check(t, "C17", "pods-kept", ans, wantPodB())
	for _, n := range ans.Nodes {
		if n.Kind == "Pod" && n.Owner.State == "owner-missing" {
			t.Errorf("C17: Pod %s is owner-missing because its ReplicaSet was not read", n.Name)
		}
	}
}

func wantPodB() want { return want{"Pod", "pod-b", "-", "owner-forbidden", textForbidden} }

// TestG_ClusterScopedOwner (C13, D.7): the static pods of kube-system are owned by their
// Node: owner-cluster-scoped for the admin, owner-forbidden for a ServiceAccount that
// cannot read Nodes.
func TestG_ClusterScopedOwner(t *testing.T) {
	fixtures(t)
	var static []want
	var forbidden []want
	for _, o := range objects(t, "kube-system", "pods") {
		if _, mirror := o.Metadata.Annotations["kubernetes.io/config.mirror"]; !mirror {
			continue
		}
		var refs []string
		for _, r := range o.Metadata.OwnerReferences {
			refs = append(refs, fmt.Sprintf("%s %s/%s controller=%v", r.APIVersion, r.Kind, r.Name, r.Controller != nil && *r.Controller))
		}
		t.Logf("FA2-T5 D.7 kube-system static pod %s ownerReferences %v", o.Metadata.Name, refs)
		if len(o.Metadata.OwnerReferences) != 1 || o.Metadata.OwnerReferences[0].Kind != "Node" || o.Metadata.OwnerReferences[0].APIVersion != "v1" {
			t.Fatalf("STOP-4 STATIC-OWNER-T5: static pod %s has ownerReferences %v, not one v1 Node", o.Metadata.Name, refs)
		}
		nodeName := o.Metadata.OwnerReferences[0].Name
		out, err := kubectlErr(nil, "get", "node", nodeName, "-o", "jsonpath={.metadata.uid}")
		t.Logf("FA2-T5 D.7 Node %s exists: %v (uid %s, the reference names %s)", nodeName, err == nil, strings.TrimSpace(string(out)), o.Metadata.OwnerReferences[0].UID)
		static = append(static, want{"Pod", o.Metadata.Name, "-", "owner-cluster-scoped", fmt.Sprintf(textClusterScoped, "Node", nodeName)})
		forbidden = append(forbidden, want{"Pod", o.Metadata.Name, "-", "owner-forbidden", textForbidden})
	}
	if len(static) == 0 {
		t.Fatal("preparation: no static pod in kube-system")
	}
	p := adminPanel(t)
	_, ans, _ := p.graph(t, "admin kube-system", "kube-system")
	check(t, "C13", "static-pods", ans, static...)
	if canI(t, nsWork, saViewSystem, "get", "nodes") != "no" {
		t.Fatal("preparation: the ServiceAccount view-system may get Nodes")
	}
	v := saPanel(t, nsWork, saViewSystem)
	_, vans, _ := v.graph(t, "view-system kube-system", "kube-system")
	check(t, "C13", "static-pods-nodes-403", vans, forbidden...)
}

// TestG_SecretOwner (C14b): a core Secret owner is read like any owner, with its values
// removed; a missing Secret owner is owner-missing; a ServiceAccount without Secret read
// gets owner-forbidden; a custom kind named Secret is resolved by its own apiVersion. No
// Secret value reaches any response or output line (canary with positive control).
func TestG_SecretOwner(t *testing.T) {
	fx := fixtures(t)
	settled(t, nsSecret)
	p := adminPanel(t)
	_, ans, adminRaw := p.graph(t, "admin "+nsSecret, nsSecret, "s1", "gone")
	check(t, "C14b", "secret-owner", ans,
		want{"Pod", "p-s1", "Secret/s1", "contained", ""},
		want{"Pod", "p-gone", "-", "owner-missing", fmt.Sprintf(textMissing, "Secret", "gone")},
		want{"Pod", "p-vs1", "Secret/vs1", "contained", ""})
	for _, n := range ans.Nodes {
		if n.Kind == "Secret" {
			t.Logf("FA2-T5 C14b node Secret/%s apiVersion %s", n.Name, n.APIVersion)
			if (n.Name == "s1" && n.APIVersion != "v1") || (n.Name == "vs1" && n.APIVersion != "vault.example.com/v1") {
				t.Errorf("C14b: Secret/%s has apiVersion %s", n.Name, n.APIVersion)
			}
		}
	}
	if canI(t, nsSecret, saNoSecrets, "get", "secrets") != "no" {
		t.Fatal("preparation: the ServiceAccount no-secrets may get Secrets")
	}
	s := saPanel(t, nsSecret, saNoSecrets)
	_, sans, saRaw := s.graph(t, "no-secrets "+nsSecret, nsSecret, "s1", "gone")
	check(t, "C14b", "secret-owner-get-403", sans, want{"Pod", "p-s1", "-", "owner-forbidden", textForbidden})
	control := secretscan.Count(kubectl(t, nil, "get", "secret", "s1", "-n", nsSecret, "-o", "json"), fx.canary)
	output := bytes.Join([][]byte{p.stdout.bytes(), p.stderr.bytes(), s.stdout.bytes(), s.stderr.bytes()}, []byte("\n"))
	responses.mu.Lock()
	all := bytes.Join(responses.raw, []byte("\n"))
	responses.mu.Unlock()
	found, inAnswers := secretscan.Count(output, fx.canary), secretscan.Count(append(append(all, adminRaw...), saRaw...), fx.canary)
	t.Logf("FA2-T5 C14b canary: positive control in the Secret read by kubectl %d (want at least 1), in intuitik's output %d, in every graph response %d (want 0)", control, found, inAnswers)
	if control == 0 || found != 0 || inAnswers != 0 {
		t.Errorf("C14b canary: control %d, output %d, responses %d", control, found, inAnswers)
	}
}

// TestG_CustomWithoutCRDList (C17b (1), GA-5): a ServiceAccount that cannot list CRDs
// but can list the custom resources of the namespace sees them, with nothing refused.
func TestG_CustomWithoutCRDList(t *testing.T) {
	fixtures(t)
	settled(t, nsCustom)
	if canI(t, nsCustom, saNoCRDList, "list", "customresourcedefinitions") != "no" || canI(t, nsCustom, saNoCRDList, "list", "wdgts.example.com") != "yes" {
		t.Fatal("preparation: the ServiceAccount no-crd-list does not have the permissions of C17b (1)")
	}
	p := saPanel(t, nsCustom, saNoCRDList)
	_, ans, _ := p.graph(t, "no-crd-list "+nsCustom, nsCustom)
	check(t, "C17b", "no-crd-list", ans,
		want{"Widget", "w1", "-", "none", ""}, want{"Widget", "w2", "-", "none", ""},
		want{"Deployment", "api", "Widget/w1", "contained", ""}, want{"Pod", "lone", "Widget/w2", "contained", ""},
		want{"Gadget", "gd", "-", "none", ""}, want{"Pod", "in-gd", "Gadget/gd", "contained", ""})
	t.Logf("FA2-T5 C17b input=no-crd-list refused=%d %v (want 0)", len(ans.Refused), ans.Refused)
	if len(ans.Refused) != 0 {
		t.Errorf("C17b (1): refused %v, want none", ans.Refused)
	}
}

// TestG_VersionPerResource (C17c, D.2 r3): Widget is served in v1 and v2 (the group's
// preferred version), Gadget only in v1; each is read at its own version.
func TestG_VersionPerResource(t *testing.T) {
	fixtures(t)
	settled(t, nsCustom)
	// D.2: the groups of /apis outside list B, and whether a CRD serves them.
	var groups struct {
		Groups []struct {
			Name             string `json:"name"`
			PreferredVersion struct {
				Version string `json:"version"`
			} `json:"preferredVersion"`
		} `json:"groups"`
	}
	json.Unmarshal(kubectl(t, nil, "get", "--raw", "/apis"), &groups)
	crdGroups := map[string]bool{}
	for _, g := range strings.Fields(string(kubectl(t, nil, "get", "crd", "-o", "jsonpath={.items[*].spec.group}"))) {
		crdGroups[g] = true
	}
	var outside []string
	preferred := ""
	for _, g := range groups.Groups {
		if g.Name == "example.com" {
			preferred = g.PreferredVersion.Version
		}
		if !builtIn[g.Name] {
			outside = append(outside, fmt.Sprintf("%s(crd=%v)", g.Name, crdGroups[g.Name]))
		}
	}
	t.Logf("FA2-T5 D.2 groups of /apis outside list B: %v; example.com preferred version %s", outside, preferred)
	if preferred != "v2" {
		t.Fatalf("preparation: the preferred version of example.com is %q, want v2", preferred)
	}
	p := adminPanel(t)
	_, ans, _ := p.graph(t, "admin "+nsCustom, nsCustom)
	check(t, "C17c", "versions", ans,
		want{"Widget", "w2", "-", "none", ""}, want{"Gadget", "gd", "-", "none", ""},
		want{"Pod", "lone", "Widget/w2", "contained", ""}, want{"Pod", "in-gd", "Gadget/gd", "contained", ""})
	versions := map[string]string{}
	for _, n := range ans.Nodes {
		versions[n.Kind] = n.APIVersion
	}
	pass := versions["Widget"] == "example.com/v2" && versions["Gadget"] == "example.com/v1"
	t.Logf("FA2-T5 C17c input=versions version per kind Widget=%s Gadget=%s (want example.com/v2, example.com/v1) result=%s",
		versions["Widget"], versions["Gadget"], map[bool]string{true: "PASS", false: "FAIL"}[pass])
	if !pass {
		t.Errorf("C17c: versions %v", versions)
	}
}

// builtIn is list B of packet D.2, written here independently of the product.
var builtIn = map[string]bool{
	"admissionregistration.k8s.io": true, "apiextensions.k8s.io": true, "apiregistration.k8s.io": true, "apps": true,
	"authentication.k8s.io": true, "authorization.k8s.io": true, "autoscaling": true, "batch": true, "certificates.k8s.io": true,
	"coordination.k8s.io": true, "discovery.k8s.io": true, "events.k8s.io": true, "flowcontrol.apiserver.k8s.io": true,
	"internal.apiserver.k8s.io": true, "lifecycle.k8s.io": true, "networking.k8s.io": true, "node.k8s.io": true, "policy": true,
	"rbac.authorization.k8s.io": true, "resource.k8s.io": true, "scheduling.k8s.io": true, "storage.k8s.io": true,
	"storagemigration.k8s.io": true, "metrics.k8s.io": true, "custom.metrics.k8s.io": true, "external.metrics.k8s.io": true,
}

// closedPort returns a 127.0.0.1 port that nothing listens on and the OS error that a
// connection to it gets.
func closedPort(t *testing.T) (int, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err = net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	var op *net.OpError
	if !errors.As(err, &op) {
		t.Fatalf("dial of the closed port %d: %v", port, err)
	}
	return port, op.Err.Error()
}

// TestG_RouteGate (C16, GA-1, GA-2): without a session the route answers 401 with no
// cluster data; while the state is not connected it answers 503 with the state and its
// exact K-text, and no nodes.
func TestG_RouteGate(t *testing.T) {
	ki := kindCluster(t)
	t.Run("no-session", func(t *testing.T) {
		kc := kubeconfig(t, "gate-ok", ki.server, ki.caData, map[string]any{"client-certificate-data": ki.certData, "client-key-data": ki.keyData})
		p := startPanel(t, kc, adminUser(t))
		p.settle()
		status, raw := p.request("GET", "/api/graph/containment?namespace=default", nil, false)
		leaks := []string{}
		for _, s := range []string{"gate-ok", ki.server, `"nodes"`, `"namespace"`} {
			if strings.Contains(string(raw), s) {
				leaks = append(leaks, s)
			}
		}
		pass := status == 401 && len(leaks) == 0
		t.Logf("FA2-T5 C16 input=no-session expected=401 no-cluster-data actual=%d cluster-data=%v result=%s", status, leaks, map[bool]string{true: "PASS", false: "FAIL"}[pass])
		if !pass {
			t.Errorf("C16 no session: %d, body carries %v", status, leaks)
		}
	})
	port, osErr := closedPort(t)
	cases := []struct {
		name, ctx, server, ca, state, text string
		user                               map[string]any
	}{
		{"unreachable", "gate-ur", "https://127.0.0.1:" + strconv.Itoa(port), "", "unreachable",
			fmt.Sprintf(textUR, "https://127.0.0.1:"+strconv.Itoa(port), "gate-ur", osErr), map[string]any{"token": "intuitik-gate-token-" + randomHex(8)}},
		{"credentials-rejected", "gate-au", ki.server, ki.caData, "credentials-rejected",
			fmt.Sprintf(textAU, "gate-au-cluster", "gate-au"), map[string]any{"token": "intuitik-rejected-token-" + randomHex(8)}},
		{"credential-program-failed", "gate-ep", ki.server, ki.caData, "credential-program-failed",
			fmt.Sprintf(textEP, "/bin/false", "gate-ep", "exit code 1"),
			map[string]any{"exec": map[string]any{"apiVersion": "client.authentication.k8s.io/v1", "command": "/bin/false", "interactiveMode": "Never"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := startPanel(t, kubeconfig(t, c.ctx, c.server, c.ca, c.user), "")
			o := p.settle()
			if o.State != c.state {
				t.Fatalf("preparation: state %s, want %s (%q)", o.State, c.state, str(o.Message))
			}
			status, raw := p.request("GET", "/api/graph/containment?namespace=default", nil, true)
			var body struct {
				State   string          `json:"state"`
				Message *string         `json:"message"`
				Nodes   json.RawMessage `json:"nodes"`
			}
			json.Unmarshal(raw, &body)
			pass := status == 503 && body.State == c.state && str(body.Message) == c.text && body.Nodes == nil
			t.Logf("FA2-T5 C16 input=%s expected=503 %s actual=%d %s nodes-present=%v expected-text=%s actual-text=%s result=%s",
				c.name, c.state, status, body.State, body.Nodes != nil, c.text, str(body.Message), map[bool]string{true: "PASS", false: "FAIL"}[pass])
			if !pass {
				t.Errorf("C16 %s: %d %s %q nodes %s; want 503 %s %q and no nodes", c.name, status, body.State, str(body.Message), body.Nodes, c.state, c.text)
			}
		})
	}
}
