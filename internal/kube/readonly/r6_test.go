package readonly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// pathResult is what one serialization path returned, serialized as bytes.
type pathResult struct {
	name string
	out  []byte
	err  error
}

var secretsResource = Resource{Version: "v1", Resource: "secrets"}

// readSecretPaths reads the Secret ns/name through every serialization path of
// packet M1-T4 R6 on a guarded configuration: typed get and list, dynamic
// (unstructured) get and list, Table with includeObject, Watch and raw bytes.
func readSecretPaths(ctx context.Context, cfg *rest.Config, ns, name string) []pathResult {
	var res []pathResult
	add := func(n string, v any, err error) {
		var out []byte
		if err == nil {
			if b, ok := v.([]byte); ok {
				out = b
			} else {
				out, err = json.Marshal(v)
			}
		}
		res = append(res, pathResult{name: n, out: out, err: err})
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return []pathResult{{name: "typed", err: err}}
	}
	s, err := cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	add("typed get", s, err)
	sl, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	add("typed list", sl, err)
	c, err := newClient(cfg)
	if err != nil {
		return append(res, pathResult{name: "client", err: err})
	}
	u, err := c.Object(ctx, secretsResource, ns, name)
	add("dynamic get (unstructured)", u, err)
	ul, err := c.List(ctx, secretsResource, ns)
	add("dynamic list (unstructured)", ul, err)

	hc, err := rest.HTTPClientFor(cfg)
	if err == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Host+"/api/v1/namespaces/"+ns+"/secrets?includeObject=Object", nil)
		req.Header.Set("Accept", "application/json;as=Table;v=v1;g=meta.k8s.io")
		var resp *http.Response
		if resp, err = hc.Do(req); err == nil {
			var b []byte
			b, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			add("table with includeObject", b, err)
		}
	}
	if err != nil {
		add("table with includeObject", nil, err)
	}

	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	w, err := c.Watch(wctx, secretsResource, ns)
	if err != nil {
		add("watch", nil, err)
	} else {
		var events []any
		for ev := range w.ResultChan() {
			events = append(events, map[string]any{"type": ev.Type, "object": ev.Object})
			if found := strings.Contains(string(mustMarshal(ev.Object)), `"name":"`+name+`"`); found {
				break
			}
		}
		w.Stop()
		if len(events) == 0 {
			add("watch", nil, fmt.Errorf("no watch event for %s", name))
		} else {
			add("watch", events, nil)
		}
	}
	cancel()

	raw, err := cs.CoreV1().RESTClient().Get().AbsPath("/api/v1/namespaces", ns, "secrets", name).DoRaw(ctx)
	add("raw bytes (Do().Raw)", raw, err)
	resp, err := c.Get(ctx, "/api/v1/namespaces/"+ns+"/secrets/"+name)
	if err == nil {
		var b []byte
		b, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		add("raw bytes (Client.Get)", b, err)
	} else {
		add("raw bytes (Client.Get)", nil, err)
	}
	return res
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// strippedKeys finds the Secret named name in a serialized path result and returns
// its key names of data and stringData with the RV3 problems found: a key with a
// non-empty value, a missing kept field, or a last-applied annotation left.
func strippedKeys(out []byte, name string) (map[string]bool, []string) {
	var doc any
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, []string{"output is not JSON: " + err.Error()}
	}
	keys := map[string]bool{}
	var problems []string
	found := 0
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			md, _ := t["metadata"].(map[string]any)
			_, hasData := t["data"]
			_, hasString := t["stringData"]
			if md != nil && md["name"] == name && (hasData || hasString || t["type"] != nil) {
				found++
				for _, f := range []string{"data", "stringData"} {
					if m, ok := t[f].(map[string]any); ok {
						for k, val := range m {
							keys[f+"."+k] = true
							if val != "" {
								problems = append(problems, fmt.Sprintf("%s.%s has a value", f, k))
							}
						}
					}
				}
				labels, _ := md["labels"].(map[string]any)
				ann, _ := md["annotations"].(map[string]any)
				if labels["app"] != "r6" {
					problems = append(problems, "label app=r6 missing")
				}
				if ann["note"] != "kept" {
					problems = append(problems, "annotation note=kept missing")
				}
				if _, ok := ann[lastAppliedAnnotation]; ok {
					problems = append(problems, "last-applied annotation present")
				}
				if t["type"] != "Opaque" {
					problems = append(problems, "type Opaque missing")
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(doc)
	if found == 0 {
		problems = append(problems, "Secret "+name+" not found")
	}
	return keys, problems
}

func expectedKeys(dataKeys, stringKeys []string) map[string]bool {
	m := map[string]bool{}
	for _, k := range dataKeys {
		m["data."+k] = true
	}
	for _, k := range stringKeys {
		m["stringData."+k] = true
	}
	return m
}

func sameKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// pathFindings sums the canary findings and the key problems of every path.
func pathFindings(results []pathResult, canary, name string, want map[string]bool) (findings int, keyFailures []string, errs []string) {
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.name+": "+r.err.Error())
			continue
		}
		findings += canaryCount(r.out, canary)
		keys, problems := strippedKeys(r.out, name)
		if !sameKeys(keys, want) {
			problems = append(problems, fmt.Sprintf("keys %s, expected %s", fmtKeys(keys), fmtKeys(want)))
		}
		for _, p := range problems {
			keyFailures = append(keyFailures, r.name+": "+p)
		}
	}
	return
}

// TestR6_StringDataStripped (packet M1-T4 R6, decoder part, no cluster): responses
// that carry stringData (a Secret, a List item, a Table row with includeObject, a
// Watch event, a raw body) lose the values of data, stringData and the last-applied
// annotation on every serialization path; every key name stays with an empty value
// (RV3). Positive control: the raw response of the fake server holds the canary.
// Negative fixtures: a conversion that keeps the values is found by the canary search;
// a conversion that drops the maps passes the search and fails the key check.
func TestR6_StringDataStripped(t *testing.T) {
	ctx := context.Background()
	canary := randomValue(t, "r6canary")
	dataKeys, stringKeys := []string{"d1", "d2", "d3"}, []string{"s1"}
	srv := newFakeAPI(t, false)
	srv.add("secrets", secretObject("a", "s1", canary, dataKeys, stringKeys))
	want := expectedKeys(dataKeys, stringKeys)

	// Positive control: the same Secret through a client without readonly.
	resp, err := http.Get(srv.srv.URL + "/api/v1/namespaces/a/secrets/s1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	positive := canaryCount(raw, canary)
	if positive < 1 {
		t.Errorf("R6 positive control: %d findings in the raw fake server response, expected at least 1", positive)
	}

	results := readSecretPaths(ctx, guarded(srv.config("token-r6")), "a", "s1")
	for _, r := range results {
		if r.err != nil {
			t.Errorf("R6 path %s: %v", r.name, r.err)
			continue
		}
		n := canaryCount(r.out, canary)
		keys, problems := strippedKeys(r.out, "s1")
		if n != 0 {
			t.Errorf("R6 path %s: %d canary findings, expected 0", r.name, n)
		}
		if !sameKeys(keys, want) {
			t.Errorf("R6 path %s: keys %s, expected %s", r.name, fmtKeys(keys), fmtKeys(want))
		}
		for _, p := range problems {
			t.Errorf("R6 path %s: %s", r.name, p)
		}
		t.Logf("R6 decoder path %s: canary findings=%d keys=%s", r.name, n, fmtKeys(keys))
	}
	t.Logf("R6 decoder positive control: raw fake server response findings=%d", positive)

	runR6NegativeFixtures(t, func() []pathResult { return readSecretPaths(ctx, guarded(srv.config("token-r6")), "a", "s1") }, canary, "s1", want)
}

// runR6NegativeFixtures replaces the conversion with each negative fixture of R6 and
// shows that the checks above fail on it.
func runR6NegativeFixtures(t *testing.T, read func() []pathResult, canary, name string, want map[string]bool) {
	t.Helper()
	runR6NegativeKeep(t, read, canary, name, want)
	runR6NegativeDrop(t, read, canary, name, want)
}

// runR6NegativeKeep: a conversion that keeps the values is found by the canary search.
func runR6NegativeKeep(t *testing.T, read func() []pathResult, canary, name string, want map[string]bool) {
	t.Helper()
	defer func(c func(map[string]any)) { secretConversion = c }(secretConversion)
	secretConversion = func(map[string]any) {}
	findings, _, errs := pathFindings(read(), canary, name, want)
	if findings < 1 || len(errs) > 0 {
		t.Errorf("R6 negative fixture keep-values: findings=%d errors=%v, expected the canary search to find the values", findings, errs)
	}
	t.Logf("R6 negative fixture keep-values: canary findings=%d (the zero-finding check fails on it)", findings)
}

// runR6NegativeDrop: a conversion that deletes the whole data and stringData maps
// passes the canary search and fails the key-name check (P-T4-1).
func runR6NegativeDrop(t *testing.T, read func() []pathResult, canary, name string, want map[string]bool) {
	t.Helper()
	defer func(c func(map[string]any)) { secretConversion = c }(secretConversion)
	secretConversion = func(m map[string]any) {
		delete(m, "data")
		delete(m, "stringData")
		if md, ok := m["metadata"].(map[string]any); ok {
			if ann, ok := md["annotations"].(map[string]any); ok {
				delete(ann, lastAppliedAnnotation)
			}
		}
	}
	findings, keyFailures, errs := pathFindings(read(), canary, name, want)
	if findings != 0 || len(keyFailures) == 0 || len(errs) > 0 {
		t.Errorf("R6 negative fixture drop-maps: findings=%d key failures=%d errors=%v, expected 0 findings and a key-name failure", findings, len(keyFailures), errs)
	}
	sort.Strings(keyFailures)
	first := ""
	if len(keyFailures) > 0 {
		first = keyFailures[0]
	}
	t.Logf("R6 negative fixture drop-maps: canary findings=%d key-name failures=%d (first: %s)", findings, len(keyFailures), first)
}
