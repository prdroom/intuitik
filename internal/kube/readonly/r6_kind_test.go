//go:build kind

package readonly

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// The R6 cluster tests (packet M1-T4 R6, real cluster part) run in the e2e-kind job.
// The canary Secret is created with kubectl apply in the kind cluster; its value is made
// at run time and never printed.

// kubectl runs kubectl with the kind admin kubeconfig and returns its stdout.
func kubectl(t *testing.T, stdin []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("kubectl", args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+os.Getenv("INTUITIK_E2E_KIND_KUBECONFIG"))
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("kubectl %s: %v: %s", args[0], err, stderr.String())
	}
	return stdout.Bytes()
}

type kindCanary struct {
	canary    string
	namespace string
	name      string
	keys      map[string]bool // the server-normalized data key set, as data.<key>
}

// applyCanary creates, with kubectl apply, a Secret with three keys in data and one in
// stringData, labels and an annotation; kubectl apply adds the last-applied
// annotation, which holds the stringData value. The expected key set is read back
// from the API server as key names only.
func applyCanary(t *testing.T, namespace string) kindCanary {
	t.Helper()
	k := kindCanary{canary: randomValue(t, "r6kind"), namespace: namespace, name: "r6-canary-" + randomValue(t, "")[:8]}
	data := map[string]any{}
	for _, key := range []string{"d1", "d2", "d3"} {
		data[key] = base64.StdEncoding.EncodeToString([]byte(k.canary + "-" + key))
	}
	manifest := map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
		map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace}},
		map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
			"metadata":   map[string]any{"name": k.name, "namespace": namespace, "labels": map[string]any{"app": "r6"}, "annotations": map[string]any{"note": "kept"}},
			"data":       data,
			"stringData": map[string]any{"s1": k.canary + "-s1"}},
	}}
	kubectl(t, mustJSON(t, manifest), "apply", "-f", "-")
	keys := kubectl(t, nil, "get", "secret", "-n", namespace, k.name, "-o", `go-template={{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}`)
	k.keys = map[string]bool{}
	for _, key := range strings.Fields(string(keys)) {
		k.keys["data."+key] = true
	}
	if len(k.keys) < 4 {
		t.Fatalf("R6 kind: the server-normalized key set has %d keys, expected the 3 data keys and the stringData key", len(k.keys))
	}
	return k
}

// connectKind connects through readonly to the kind cluster (KUBECONFIG of the job).
func connectKind(t *testing.T) Outcome {
	t.Helper()
	t.Setenv("KUBECONFIG", os.Getenv("INTUITIK_E2E_KIND_KUBECONFIG"))
	o := Attempt(context.Background(), "")
	if o.State != StateConnected {
		t.Fatalf("R6 kind: attempt state %s: %s", o.State, o.Message)
	}
	return o
}

// kindPaths reads the canary Secret on every serialization path, after an informer on
// the guarded configuration has reached HasSynced; the informer's cached object is one
// more path.
func kindPaths(t *testing.T, o Outcome, k kindCanary) []pathResult {
	t.Helper()
	ctx := context.Background()
	dyn, err := dynamic.NewForConfig(o.config)
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, 0, k.namespace, nil)
	informer := factory.ForResource(gvr).Informer()
	stop := make(chan struct{})
	defer func() { close(stop); factory.Shutdown() }()
	factory.Start(stop)
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	synced := cache.WaitForCacheSync(wctx.Done(), informer.HasSynced)
	cancel()
	if !synced {
		return []pathResult{{name: "informer", err: context.DeadlineExceeded}}
	}
	res := readSecretPaths(ctx, o.config, k.namespace, k.name)
	obj, exists, err := informer.GetStore().GetByKey(k.namespace + "/" + k.name)
	if err == nil && !exists {
		err = os.ErrNotExist
	}
	var out []byte
	if err == nil {
		out, err = json.Marshal(obj)
	}
	return append(res, pathResult{name: "informer cache after HasSynced", out: out, err: err})
}

// TestR6_SecretStrippedOnEveryPath (packet M1-T4 R6, G6): after HasSynced, every
// serialization path and stderr give 0 canary findings; the raw API server response
// read with the admin kubeconfig gives at least 1 (positive control). Negative
// fixture: a conversion that keeps the values is found.
func TestR6_SecretStrippedOnEveryPath(t *testing.T) {
	k := applyCanary(t, "r6-"+randomValue(t, "")[:10])
	raw := kubectl(t, nil, "get", "secret", "-n", k.namespace, k.name, "-o", "json")
	positive := canaryCount(raw, k.canary)
	if positive < 1 {
		t.Errorf("R6 positive control: %d findings in the raw API server response, expected at least 1", positive)
	}
	stop := stderrCapture(t)
	o := connectKind(t)
	results := kindPaths(t, o, k)
	errOut := stop()
	for _, r := range results {
		if r.err != nil {
			t.Errorf("R6 path %s: %v", r.name, r.err)
			continue
		}
		n := canaryCount(r.out, k.canary)
		if n != 0 {
			t.Errorf("R6 path %s: %d canary findings, expected 0", r.name, n)
		}
		t.Logf("R6 kind path %s: canary findings=%d", r.name, n)
	}
	if n := canaryCount(errOut, k.canary); n != 0 {
		t.Errorf("R6 stderr: %d canary findings, expected 0", n)
	}
	t.Logf("R6 kind stderr: bytes=%d canary findings=%d; positive control (raw API server response) findings=%d", len(errOut), canaryCount(errOut, k.canary), positive)
	runR6NegativeKeep(t, func() []pathResult { return kindPaths(t, o, k) }, k.canary, k.namespace, k.name, k.keys)
}

// TestR6_KeyNamesKept (packet M1-T4 R6, RV3, P-T4-1): on every path the stripped
// Secret holds exactly the server-normalized key set, each key with an empty value,
// with kind, apiVersion, name, namespace, labels, other annotations and type kept.
// Negative fixtures: a conversion that drops the whole maps, and conversions that change
// the namespace or the kind, pass the canary search and fail this check.
func TestR6_KeyNamesKept(t *testing.T) {
	k := applyCanary(t, "r6-"+randomValue(t, "")[:10])
	o := connectKind(t)
	for _, r := range kindPaths(t, o, k) {
		if r.err != nil {
			t.Errorf("R6 path %s: %v", r.name, r.err)
			continue
		}
		keys, problems := strippedKeys(r.out, r.name, k.namespace, k.name)
		if !sameKeys(keys, k.keys) {
			t.Errorf("R6 path %s: keys %s, expected %s", r.name, fmtKeys(keys), fmtKeys(k.keys))
		}
		for _, p := range problems {
			t.Errorf("R6 path %s: %s", r.name, p)
		}
		t.Logf("R6 kind path %s: keys %s (expected %s) %s, RV3 problems=%d", r.name, fmtKeys(keys), fmtKeys(k.keys), identity(r.out, k.name), len(problems))
	}
	read := func() []pathResult { return kindPaths(t, o, k) }
	runR6NegativeDrop(t, read, k.canary, k.namespace, k.name, k.keys)
	runR6NegativeIdentity(t, read, k.canary, k.namespace, k.name)
}

// TestR6_SecretByLocation (packet M1-T4 R6, RV4): the stripping follows the object,
// not a name: a ConfigMap in a namespace named secrets keeps its marker value, and a
// Secret canary in that namespace gives 0 findings.
func TestR6_SecretByLocation(t *testing.T) {
	ctx := context.Background()
	k := applyCanary(t, "secrets")
	marker := randomValue(t, "r6marker")
	cmName := "r6-marker-" + randomValue(t, "")[:8]
	kubectl(t, mustJSON(t, map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": cmName, "namespace": "secrets"}, "data": map[string]any{"marker": marker}}), "apply", "-f", "-")
	o := connectKind(t)
	c, err := NewClient(o)
	if err != nil {
		t.Fatal(err)
	}
	cm := Resource{Version: "v1", Resource: "configmaps"}
	var outs []pathResult
	u, err := c.Object(ctx, cm, "secrets", cmName)
	outs = append(outs, pathResult{name: "configmap dynamic get", out: mustMarshal(u), err: err})
	l, err := c.List(ctx, cm, "secrets")
	outs = append(outs, pathResult{name: "configmap dynamic list", out: mustMarshal(l), err: err})
	if resp, err := c.Get(ctx, "/api/v1/namespaces/secrets/configmaps/"+cmName); err == nil {
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		resp.Body.Close()
		outs = append(outs, pathResult{name: "configmap raw bytes", out: b.Bytes()})
	} else {
		outs = append(outs, pathResult{name: "configmap raw bytes", err: err})
	}
	for _, r := range outs {
		if r.err != nil {
			t.Errorf("R6 location %s: %v", r.name, r.err)
			continue
		}
		n := canaryCount(r.out, marker)
		if n < 1 {
			t.Errorf("R6 location %s: marker findings=%d, expected at least 1 (a ConfigMap is never stripped)", r.name, n)
		}
		t.Logf("R6 location %s in namespace secrets: marker findings=%d", r.name, n)
	}
	results := kindPaths(t, o, k)
	names := make([]string, 0, len(results))
	for _, r := range results {
		if r.err != nil {
			t.Errorf("R6 location Secret path %s: %v", r.name, r.err)
			continue
		}
		n := canaryCount(r.out, k.canary)
		if n != 0 {
			t.Errorf("R6 location Secret path %s in namespace secrets: %d canary findings, expected 0", r.name, n)
		}
		names = append(names, r.name)
	}
	sort.Strings(names)
	t.Logf("R6 location: the Secret canary in namespace secrets gave 0 findings on %d paths %v", len(names), names)
}
