package readonly

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// fakeAPI is a fake API server for the no-cluster R tests (packet M1-T4 R5, R6, R9,
// R10). It counts every request it receives, keeps a fixed object set and serves
// discovery in aggregated or unaggregated mode.
type fakeAPI struct {
	t          *testing.T
	srv        *httptest.Server
	aggregated bool
	received   atomic.Int64
	mu         sync.Mutex
	requests   []string // "METHOD path"
	agents     []string
	auth       []string
	objects    map[string][]map[string]any // "<resource>" -> objects
}

func newFakeAPI(t *testing.T, aggregated bool) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, aggregated: aggregated, objects: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) add(resource string, obj map[string]any) {
	f.mu.Lock()
	f.objects[resource] = append(f.objects[resource], obj)
	f.mu.Unlock()
}

// config is a connection to the fake server with a bearer token, without the guard.
func (f *fakeAPI) config(token string) *rest.Config {
	return &rest.Config{Host: f.srv.URL, BearerToken: token}
}

func (f *fakeAPI) count() int64 { return f.received.Load() }

func (f *fakeAPI) seen() ([]string, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...), append([]string(nil), f.agents...), append([]string(nil), f.auth...)
}

func writeJSON(w http.ResponseWriter, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	json.NewEncoder(w).Encode(v)
}

var coreResources = []map[string]any{
	{"name": "pods", "singularName": "pod", "namespaced": true, "kind": "Pod", "verbs": []any{"get", "list", "watch"}},
	{"name": "pods/log", "singularName": "", "namespaced": true, "kind": "Pod", "verbs": []any{"get"}},
	{"name": "secrets", "singularName": "secret", "namespaced": true, "kind": "Secret", "verbs": []any{"get", "list", "watch"}},
	{"name": "configmaps", "singularName": "configmap", "namespaced": true, "kind": "ConfigMap", "verbs": []any{"get", "list", "watch"}},
}

var kinds = map[string]string{"pods": "Pod", "secrets": "Secret", "configmaps": "ConfigMap"}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.received.Add(1)
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.agents = append(f.agents, r.Header.Get("User-Agent"))
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if r.Method != http.MethodGet {
		writeJSON(w, "application/json", map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Success"})
		return
	}
	aggregatedAccept := strings.Contains(r.Header.Get("Accept"), "apidiscovery.k8s.io")
	switch p := r.URL.Path; {
	case p == "/api" && f.aggregated && aggregatedAccept:
		var resources []any
		for _, res := range coreResources {
			if strings.Contains(res["name"].(string), "/") {
				continue
			}
			resources = append(resources, map[string]any{
				"resource": res["name"], "singularResource": res["singularName"], "scope": "Namespaced",
				"responseKind": map[string]any{"group": "", "version": "v1", "kind": res["kind"]},
				"verbs":        res["verbs"],
				"subresources": subresources(res["name"].(string)),
			})
		}
		writeJSON(w, "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList", map[string]any{
			"kind": "APIGroupDiscoveryList", "apiVersion": "apidiscovery.k8s.io/v2", "metadata": map[string]any{},
			"items": []any{map[string]any{"metadata": map[string]any{}, "versions": []any{map[string]any{"version": "v1", "resources": resources, "freshness": "Current"}}}},
		})
	case p == "/apis" && f.aggregated && aggregatedAccept:
		writeJSON(w, "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList", map[string]any{
			"kind": "APIGroupDiscoveryList", "apiVersion": "apidiscovery.k8s.io/v2", "metadata": map[string]any{}, "items": []any{},
		})
	case p == "/api":
		writeJSON(w, "application/json", map[string]any{"kind": "APIVersions", "versions": []any{"v1"},
			"serverAddressByClientCIDRs": []any{map[string]any{"clientCIDR": "0.0.0.0/0", "serverAddress": r.Host}}})
	case p == "/apis":
		writeJSON(w, "application/json", map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": []any{}})
	case p == "/api/v1":
		writeJSON(w, "application/json", map[string]any{"kind": "APIResourceList", "apiVersion": "v1", "groupVersion": "v1", "resources": coreResources})
	case p == "/version":
		writeJSON(w, "application/json", map[string]any{"major": "1", "minor": "37", "gitVersion": "v1.37.0"})
	default:
		f.serveResource(w, r)
	}
}

func subresources(resource string) []any {
	if resource != "pods" {
		return []any{}
	}
	return []any{map[string]any{"subresource": "log", "verbs": []any{"get"}, "responseKind": map[string]any{"group": "", "version": "v1", "kind": "Pod"}}}
}

// serveResource answers /api/v1/[namespaces/<ns>/]<resource>[/<name>] from the object
// set, and a watch with the objects as ADDED events, the initial-events bookmark and
// then nothing until the client goes away.
func (f *fakeAPI) serveResource(w http.ResponseWriter, r *http.Request) {
	seg := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
	ns := ""
	if len(seg) >= 3 && seg[0] == "namespaces" {
		ns, seg = seg[1], seg[2:]
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") || len(seg) == 0 || kinds[seg[0]] == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": 404})
		return
	}
	resource := seg[0]
	f.mu.Lock()
	var items []map[string]any
	for _, o := range f.objects[resource] {
		md := o["metadata"].(map[string]any)
		if ns == "" || md["namespace"] == ns {
			items = append(items, o)
		}
	}
	f.mu.Unlock()
	if len(seg) == 2 {
		for _, o := range items {
			if o["metadata"].(map[string]any)["name"] == seg[1] {
				writeJSON(w, "application/json", o)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": 404})
		return
	}
	q := r.URL.Query()
	if q.Get("watch") == "true" || q.Get("watch") == "1" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		if q.Get("sendInitialEvents") == "true" || q.Get("resourceVersion") == "" {
			for _, o := range items {
				enc.Encode(map[string]any{"type": "ADDED", "object": o})
			}
		}
		if q.Get("sendInitialEvents") == "true" {
			enc.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{
				"kind": kinds[resource], "apiVersion": "v1",
				"metadata": map[string]any{"resourceVersion": "10", "annotations": map[string]any{"k8s.io/initial-events-end": "true"}},
			}})
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	list := make([]any, 0, len(items))
	for _, o := range items {
		list = append(list, o)
	}
	if strings.Contains(r.Header.Get("Accept"), "as=Table") {
		rows := make([]any, 0, len(items))
		for _, o := range items {
			rows = append(rows, map[string]any{"cells": []any{o["metadata"].(map[string]any)["name"]}, "object": o})
		}
		writeJSON(w, "application/json", map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{"resourceVersion": "10"},
			"columnDefinitions": []any{map[string]any{"name": "Name", "type": "string"}}, "rows": rows})
		return
	}
	writeJSON(w, "application/json", map[string]any{"kind": kinds[resource] + "List", "apiVersion": "v1",
		"metadata": map[string]any{"resourceVersion": "10"}, "items": list})
}

// secretObject is a Secret whose data, stringData and last-applied annotation carry the
// canary, with the RV3 fields set.
func secretObject(ns, name, canary string, dataKeys, stringKeys []string) map[string]any {
	data := map[string]any{}
	for _, k := range dataKeys {
		data[k] = base64.StdEncoding.EncodeToString([]byte(canary + "-" + k))
	}
	obj := map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{
			"name": name, "namespace": ns, "resourceVersion": "5",
			"labels":      map[string]any{"app": "r6"},
			"annotations": map[string]any{"note": "kept", lastAppliedAnnotation: `{"stringData":{"k":"` + canary + `"}}`},
		},
		"data": data,
	}
	if len(stringKeys) > 0 {
		sd := map[string]any{}
		for _, k := range stringKeys {
			sd[k] = canary + "-" + k
		}
		obj["stringData"] = sd
	}
	return obj
}

// canaryCount counts the canary in data with the T2 searcher (packet M1-T4 6.1, R8),
// also inside base64 runs and their gzip decompression, up to three layers deep, so
// that a Helm release (gzip, then base64, then the base64 of a Secret value) is found.
var canaryCount = func(data []byte, canary string) int {
	return countLayers(data, canary, 3)
}

var base64Run = regexp.MustCompile(`[A-Za-z0-9+/_-]{16,}={0,2}`)

func countLayers(data []byte, canary string, depth int) int {
	n := secretscan.Count(data, canary)
	if depth == 0 {
		return n
	}
	for _, run := range base64Run.FindAll(data, -1) {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			dec, err := enc.DecodeString(string(run))
			if err != nil {
				continue
			}
			n += countLayers(dec, canary, depth-1)
			if plain, ok := gunzipAll(dec); ok {
				n += countLayers(plain, canary, depth-1)
			}
			break
		}
	}
	return n
}

func gunzipAll(data []byte) ([]byte, bool) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return nil, false
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	out, err := io.ReadAll(io.LimitReader(r, 64<<20))
	return out, err == nil && len(out) > 0
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fmtKeys(m map[string]bool) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return fmt.Sprint(sortStrings(keys))
}

func sortStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}
