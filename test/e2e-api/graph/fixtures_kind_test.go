//go:build kind

package graph

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Namespaces of the fixtures, one per group of criteria.
const (
	nsWork      = "graph-work"      // C1, C2, C3, C5
	nsCustom    = "graph-custom"    // C4, C17b (1), C17c
	nsMissing   = "graph-missing"   // C6, C7, C8
	nsForbidden = "graph-forbidden" // C9, C17
	nsMiddle    = "graph-middle"    // C10
	nsSecret    = "graph-secret"    // C14b
)

// ServiceAccounts of the forbidden and restricted cases, all in nsWork except where noted.
const (
	saPodsOnly   = "pods-only"   // nsForbidden: get and list Pods only (C9, C17)
	saNoDeploy   = "no-deploy"   // nsMiddle: Pods and ReplicaSets, no Deployments (C10)
	saNoSecrets  = "no-secrets"  // nsSecret: every listed kind, no Secrets (C14b)
	saNoCRDList  = "no-crd-list" // nsCustom: every kind of the namespace, no CRD list (C17b (1))
	saViewSystem = "view-system" // nsWork, bound to the view role in kube-system (C13)
)

// fakeUID is the uid of an owner that does not exist.
const fakeUID = "00000000-0000-4000-8000-00000000dead"

// setup is the state of the fixtures, made once per run.
type setup struct {
	err    error
	canary string // the value of Secret s1 (C14b), random per run
	steps  []string
}

var (
	setupOnce sync.Once
	fixture   setup
)

// fixtures makes every fixture once and stops the test if that failed.
func fixtures(t *testing.T) *setup {
	t.Helper()
	kindCluster(t)
	setupOnce.Do(func() {
		start := time.Now()
		fixture.build()
		fixture.steps = append(fixture.steps, fmt.Sprintf("fixtures done in %s", time.Since(start).Round(time.Second)))
	})
	for _, s := range fixture.steps {
		t.Logf("fixture: %s", s)
	}
	if fixture.err != nil {
		t.Fatalf("preparation: fixtures: %v", fixture.err)
	}
	return &fixture
}

func (s *setup) k(stdin []byte, args ...string) []byte {
	if s.err != nil {
		return nil
	}
	out, err := kubectlErr(stdin, args...)
	if err != nil {
		s.err = fmt.Errorf("kubectl %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func (s *setup) create(obj map[string]any) {
	raw, _ := json.Marshal(obj)
	s.k(raw, "create", "-f", "-")
}

func (s *setup) uid(ns, resource, name string) string {
	args := []string{"get", resource, name, "-o", "jsonpath={.metadata.uid}"}
	if ns != "" {
		args = append(args, "-n", ns)
	}
	uid := strings.TrimSpace(string(s.k(nil, args...)))
	if s.err == nil && uid == "" {
		s.err = fmt.Errorf("no uid for %s %s/%s", resource, ns, name)
	}
	return uid
}

// count is the number of objects kubectl lists for args.
func (s *setup) count(args ...string) int {
	out := strings.TrimSpace(string(s.k(nil, append(append([]string{"get"}, args...), "-o", "name")...)))
	if out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

func (s *setup) wait(limit time.Duration, what string, cond func() bool) {
	deadline := time.Now().Add(limit)
	for s.err == nil && !cond() {
		if time.Now().After(deadline) {
			s.err = fmt.Errorf("%s not reached within %s", what, limit)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// role is a Role in ns with get and list on resources of groups, bound to sa of saNS.
func (s *setup) role(ns, name, saNS, sa string, groups, resources []string) {
	s.create(map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": map[string]any{"name": name, "namespace": ns},
		"rules": []any{map[string]any{"apiGroups": groups, "resources": resources, "verbs": []any{"get", "list"}}}})
	s.create(map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": map[string]any{"name": name, "namespace": ns},
		"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": name},
		"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": sa, "namespace": saNS}}})
}

func (s *setup) replicaSet(ns, name string, replicas int, owners []any) map[string]any {
	return map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": meta(ns, name, owners),
		"spec": map[string]any{"replicas": replicas, "selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": name}}, "spec": pendingSpec()}}}
}

func (s *setup) deployment(ns, name string, replicas int, owners []any) map[string]any {
	d := s.replicaSet(ns, name, replicas, owners)
	d["kind"] = "Deployment"
	return d
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *setup) build() {
	s.canary = strings.Join([]string{"intuitik", "graph", "canary", randomHex(16)}, "-")
	s.k(nil, "apply", "-f", "../../fixtures/owners/crds.yaml")
	s.k(nil, "wait", "--for=condition=Established", "--timeout=90s",
		"crd/wdgts.example.com", "crd/gadgets.example.com", "crd/vaultsecrets.vault.example.com")
	for _, ns := range []string{nsWork, nsCustom, nsMissing, nsForbidden, nsMiddle, nsSecret} {
		s.k(nil, "create", "namespace", ns)
	}
	// The custom kinds must be served before any object of them is made.
	s.wait(60*time.Second, "the custom kinds served", func() bool {
		_, e1 := kubectlErr(nil, "get", "wdgts.v2.example.com", "-n", nsCustom)
		_, e2 := kubectlErr(nil, "get", "gadgets.v1.example.com", "-n", nsCustom)
		_, e3 := kubectlErr(nil, "get", "vaultsecrets.v1.vault.example.com", "-n", nsSecret)
		return e1 == nil && e2 == nil && e3 == nil
	})
	s.steps = append(s.steps, "CRDs established and served")

	// C1, C2, C3, C5: controller-made workloads; one rollout of web makes a second
	// ReplicaSet; the Job of the CronJob is made with the CronJob as its owner.
	s.k(nil, "apply", "-n", nsWork, "-f", "../../fixtures/owners/workloads.yaml")
	s.wait(60*time.Second, "the first ReplicaSet of web", func() bool { return s.count("rs", "-n", nsWork, "-l", "app=web") >= 1 })
	s.k(nil, "patch", "deployment", "web", "-n", nsWork, "--type=merge",
		"-p", `{"spec":{"template":{"metadata":{"annotations":{"intuitik.example/revision":"2"}}}}}`)
	s.wait(60*time.Second, "two ReplicaSets of web", func() bool { return s.count("rs", "-n", nsWork, "-l", "app=web") == 2 })
	report := s.uid(nsWork, "cronjob", "report")
	s.create(map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": meta(nsWork, "report-1", owner("batch/v1", "CronJob", "report", report)),
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"restartPolicy": "Never",
			"nodeSelector": map[string]any{"intuitik.example/none": "none"}, "containers": []any{map[string]any{"name": "c", "image": "registry.k8s.io/pause:3.10"}}}}}})
	s.wait(90*time.Second, "the pods of db, agent, once and report-1", func() bool {
		return s.count("pods", "-n", nsWork, "-l", "app=db") == 2 && s.count("pods", "-n", nsWork, "-l", "app=agent") >= 1 &&
			s.count("pods", "-n", nsWork, "-l", "job-name=once") == 1 && s.count("pods", "-n", nsWork, "-l", "job-name=report-1") == 1
	})
	s.steps = append(s.steps, "workloads of "+nsWork+" made")

	// C4, C17c: Widgets (v2, the preferred version) and a Gadget (v1 only) as owners, as
	// children and as nodes.
	for _, w := range []string{"w1", "w2", "w4"} {
		s.create(map[string]any{"apiVersion": "example.com/v2", "kind": "Widget", "metadata": meta(nsCustom, w, nil)})
	}
	w1, w2 := s.uid(nsCustom, "wdgts.example.com", "w1"), s.uid(nsCustom, "wdgts.example.com", "w2")
	s.create(s.deployment(nsCustom, "api", 1, owner("example.com/v2", "Widget", "w1", w1)))
	s.create(pod(nsCustom, "lone", owner("example.com/v2", "Widget", "w2", w2)))
	api := s.uid(nsCustom, "deployment", "api")
	s.create(map[string]any{"apiVersion": "example.com/v2", "kind": "Widget", "metadata": meta(nsCustom, "w3", owner("apps/v1", "Deployment", "api", api))})
	s.create(map[string]any{"apiVersion": "example.com/v1", "kind": "Gadget", "metadata": meta(nsCustom, "gd", nil)})
	gd := s.uid(nsCustom, "gadgets.example.com", "gd")
	s.create(pod(nsCustom, "in-gd", owner("example.com/v1", "Gadget", "gd", gd)))
	s.wait(60*time.Second, "the ReplicaSet and pod of api", func() bool {
		return s.count("rs", "-n", nsCustom, "-l", "app=api") == 1 && s.count("pods", "-n", nsCustom, "-l", "app=api") == 1
	})
	s.k(nil, "create", "serviceaccount", saNoCRDList, "-n", nsCustom)
	s.role(nsCustom, saNoCRDList, nsCustom, saNoCRDList, []string{"", "apps", "batch", "example.com", "vault.example.com"},
		[]string{"pods", "replicasets", "deployments", "statefulsets", "daemonsets", "jobs", "cronjobs", "wdgts", "gadgets", "vaultsecrets"})
	s.steps = append(s.steps, "custom resources of "+nsCustom+" made")

	// C6, C7, C8: a ReplicaSet whose Deployment does not exist (with its pod), a pod whose
	// ConfigMap owner was recreated under the same name, a pod whose owner kind is not
	// served. The garbage collector controller is off, so they stay.
	s.create(s.replicaSet(nsMissing, "orphan", 1, owner("apps/v1", "Deployment", "gone", fakeUID)))
	s.create(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta(nsMissing, "holder", nil)})
	old := s.uid(nsMissing, "configmap", "holder")
	s.k(nil, "delete", "configmap", "holder", "-n", nsMissing, "--wait=true")
	s.create(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta(nsMissing, "holder", nil)})
	s.create(pod(nsMissing, "child-recreated", owner("v1", "ConfigMap", "holder", old)))
	s.create(pod(nsMissing, "gadget-child", owner("unserved.example.com/v1", "Gadget", "g1", fakeUID)))
	s.wait(60*time.Second, "the pod of orphan", func() bool { return s.count("pods", "-n", nsMissing, "-l", "app=orphan") == 1 })
	s.steps = append(s.steps, "owner-missing fixtures of "+nsMissing+" made")

	// C9, C17: an existing ReplicaSet and a missing one as owners, read by a ServiceAccount
	// that may only get and list Pods.
	s.create(s.replicaSet(nsForbidden, "rs-a", 1, nil))
	s.create(pod(nsForbidden, "pod-b", owner("apps/v1", "ReplicaSet", "rs-ghost", fakeUID)))
	s.wait(60*time.Second, "the pod of rs-a", func() bool { return s.count("pods", "-n", nsForbidden, "-l", "app=rs-a") == 1 })
	s.k(nil, "create", "serviceaccount", saPodsOnly, "-n", nsForbidden)
	s.role(nsForbidden, saPodsOnly, nsForbidden, saPodsOnly, []string{""}, []string{"pods"})

	// C10: a Deployment read by a ServiceAccount that may read Pods and ReplicaSets only.
	s.create(s.deployment(nsMiddle, "web", 2, nil))
	s.wait(60*time.Second, "the ReplicaSet and pods of web in "+nsMiddle, func() bool {
		return s.count("rs", "-n", nsMiddle, "-l", "app=web") == 1 && s.count("pods", "-n", nsMiddle, "-l", "app=web") == 2
	})
	s.k(nil, "create", "serviceaccount", saNoDeploy, "-n", nsMiddle)
	s.role(nsMiddle, saNoDeploy, nsMiddle, saNoDeploy, []string{"", "apps"}, []string{"pods", "replicasets"})

	// C14b: a core Secret owner with a canary value, a missing Secret owner and a custom
	// kind named Secret of another group.
	s.k(nil, "create", "secret", "generic", "s1", "-n", nsSecret, "--from-literal=value="+s.canary)
	s1 := s.uid(nsSecret, "secret", "s1")
	s.create(pod(nsSecret, "p-s1", owner("v1", "Secret", "s1", s1)))
	s.create(pod(nsSecret, "p-gone", owner("v1", "Secret", "gone", fakeUID)))
	s.create(map[string]any{"apiVersion": "vault.example.com/v1", "kind": "Secret", "metadata": meta(nsSecret, "vs1", nil)})
	vs1 := s.uid(nsSecret, "vaultsecrets.vault.example.com", "vs1")
	s.create(pod(nsSecret, "p-vs1", owner("vault.example.com/v1", "Secret", "vs1", vs1)))
	s.k(nil, "create", "serviceaccount", saNoSecrets, "-n", nsSecret)
	s.role(nsSecret, saNoSecrets, nsSecret, saNoSecrets, []string{"", "apps", "batch", "example.com", "vault.example.com"},
		[]string{"pods", "replicasets", "deployments", "statefulsets", "daemonsets", "jobs", "cronjobs", "wdgts", "gadgets", "vaultsecrets"})

	// C13: a ServiceAccount with the view role in kube-system (no Nodes).
	s.k(nil, "create", "serviceaccount", saViewSystem, "-n", nsWork)
	s.k(nil, "create", "rolebinding", saViewSystem, "-n", "kube-system", "--clusterrole=view", "--serviceaccount="+nsWork+":"+saViewSystem)
	if s.err == nil {
		s.steps = append(s.steps, "every fixture made")
	}
}
