package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prdroom/intuitik/internal/graph"
	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// Route-level tests of GET /api/graph/containment (packet FA2-T5 G4, GA-7, and
// CLIENT-PER-REQ-T5), job unit. Each test connects a server to a TLS fake API server
// through a kubeconfig whose user is a credential program, run by a fake runner: the
// readonly Generation, its refresh and its state changes are the product's. The fake
// server records the method, path and bearer token of every request.

const graphNS = "team-a"

// apiRecord is one request the fake API server received.
type apiRecord struct{ method, path, token string }

func (r apiRecord) String() string { return r.method + " " + r.path + " " + r.token }

// graphAPI is the TLS fake API server of these tests.
type graphAPI struct {
	srv      *httptest.Server
	mu       sync.Mutex
	records  []apiRecord
	rejected map[string]bool
	status   map[string]int    // path -> status to answer
	bodies   map[string]string // path -> JSON body to answer
	holdPath string            // a request for this path waits for release
	arrived  chan struct{}
	release  chan struct{}
}

// listKinds are the LIST paths of the seven built-in kinds and the kind of their list.
var listKinds = map[string]string{
	"/api/v1/namespaces/%s/pods":               "PodList",
	"/apis/apps/v1/namespaces/%s/replicasets":  "ReplicaSetList",
	"/apis/apps/v1/namespaces/%s/deployments":  "DeploymentList",
	"/apis/apps/v1/namespaces/%s/statefulsets": "StatefulSetList",
	"/apis/apps/v1/namespaces/%s/daemonsets":   "DaemonSetList",
	"/apis/batch/v1/namespaces/%s/jobs":        "JobList",
	"/apis/batch/v1/namespaces/%s/cronjobs":    "CronJobList",

	// The four LISTs of the bridge kinds (FA2-T6 E.3).
	"/api/v1/namespaces/%s/services":                              "ServiceList",
	"/api/v1/namespaces/%s/persistentvolumeclaims":                "PersistentVolumeClaimList",
	"/apis/networking.k8s.io/v1/namespaces/%s/ingresses":          "IngressList",
	"/apis/autoscaling/v2/namespaces/%s/horizontalpodautoscalers": "HorizontalPodAutoscalerList",
}

func newGraphAPI(t *testing.T) *graphAPI {
	t.Helper()
	a := &graphAPI{rejected: map[string]bool{}, status: map[string]int{}, bodies: map[string]string{},
		arrived: make(chan struct{}, 1), release: make(chan struct{})}
	a.srv = httptest.NewTLSServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *graphAPI) serve(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a.mu.Lock()
	a.records = append(a.records, apiRecord{r.Method, r.URL.Path, token})
	reject, hold := a.rejected[token], a.holdPath != "" && r.URL.Path == a.holdPath
	status, body := a.status[r.URL.Path], a.bodies[r.URL.Path]
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if reject {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","code":401,"reason":"Unauthorized"}`))
		return
	}
	if hold {
		a.arrived <- struct{}{}
		<-a.release
	}
	if status != 0 {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","code":%d}`, status)
		return
	}
	if body != "" {
		w.Write([]byte(body))
		return
	}
	switch r.URL.Path {
	case "/api":
		w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		return
	case "/apis":
		w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
		return
	}
	for pattern, kind := range listKinds {
		for _, ns := range []string{graphNS, "one", "two"} {
			if r.URL.Path == fmt.Sprintf(pattern, ns) {
				fmt.Fprintf(w, `{"kind":%q,"apiVersion":"v1","metadata":{"resourceVersion":"1"},"items":[]}`, kind)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","code":404,"reason":"NotFound"}`))
}

func (a *graphAPI) reject(tokens ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range tokens {
		a.rejected[k] = true
	}
}

// since returns the records from index from on.
func (a *graphAPI) since(from int) []apiRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]apiRecord(nil), a.records[from:]...)
}

func (a *graphAPI) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.records)
}

// runPlan is what one run of the fake credential program does.
type runPlan struct {
	token   string
	fail    bool          // exit code 4
	started chan struct{} // closed when the run starts
	wait    chan struct{} // the run gives its credential only after this closes
}

// fakeRunner runs the credential program without a process: plan n for run n, the last
// plan for every run after the plans.
type fakeRunner struct {
	mu       sync.Mutex
	runs     int
	plans    []runPlan
	signaled bool
}

func (f *fakeRunner) run(ctx context.Context, p readonly.Program, admit func(start func() error) error) readonly.RunResult {
	if err := admit(func() error { return nil }); err != nil {
		return readonly.RunResult{Closed: true}
	}
	f.mu.Lock()
	plan := f.plans[min(f.runs, len(f.plans)-1)]
	f.runs++
	signal := plan.started != nil && !f.signaled
	if signal {
		f.signaled = true
	}
	f.mu.Unlock()
	if signal {
		close(plan.started)
	}
	if plan.wait != nil {
		select {
		case <-plan.wait:
		case <-ctx.Done():
			return readonly.RunResult{Class: "timeout"}
		}
	}
	if plan.fail {
		return readonly.RunResult{Class: "exit", Code: 4}
	}
	out := fmt.Sprintf(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":%q}}`, plan.token)
	return readonly.RunResult{Class: "ok", Stdout: []byte(out)}
}

func (f *fakeRunner) starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

// connectGraph writes a kubeconfig with the contexts "exec-ctx" (current) and "other" on
// the fake server, both with the fake credential program, and returns a server whose
// first attempt connected on it.
func connectGraph(t *testing.T, api *graphAPI, runner *fakeRunner) *server {
	t.Helper()
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.srv.Certificate().Raw}))
	exec := map[string]any{"apiVersion": "client.authentication.k8s.io/v1", "command": "intuitik-fake-credential", "interactiveMode": "Never"}
	doc := map[string]any{
		"apiVersion": "v1", "kind": "Config", "current-context": "exec-ctx",
		"clusters": []any{map[string]any{"name": "fake-c", "cluster": map[string]any{"server": api.srv.URL, "certificate-authority-data": ca}}},
		"users":    []any{map[string]any{"name": "exec-user", "user": map[string]any{"exec": exec}}},
		"contexts": []any{
			map[string]any{"name": "exec-ctx", "context": map[string]any{"cluster": "fake-c", "user": "exec-user"}},
			map[string]any{"name": "other", "context": map[string]any{"cluster": "fake-c", "user": "exec-user"}},
		},
	}
	raw, _ := json.Marshal(doc)
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kc, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kc)
	t.Setenv("HOME", t.TempDir())
	s := &server{}
	s.cluster.print = func(readonly.Outcome) {}
	s.cluster.run = runner.run
	s.registerRoutes()
	s.cluster.start("")
	t.Cleanup(s.cluster.stop)
	deadline := time.Now().Add(15 * time.Second)
	for {
		o, obj := s.cluster.snapshot()
		if obj.State != string(readonly.StateConnecting) {
			if o.State != readonly.StateConnected {
				t.Fatalf("preparation: the exec context did not connect: state %s %q", o.State, o.Message)
			}
			return s
		}
		if time.Now().After(deadline) {
			t.Fatal("preparation: still connecting after 15 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// graphCall calls the registered graph route for namespace and returns its status and body.
func graphCall(t *testing.T, s *server, query string) (int, map[string]any) {
	t.Helper()
	for _, rt := range s.routes {
		if rt.Method == http.MethodGet && rt.Path == "/api/graph/containment" {
			rec := httptest.NewRecorder()
			rt.Handle(s, rec, httptest.NewRequest(http.MethodGet, "/api/graph/containment"+query, nil))
			var body map[string]any
			json.Unmarshal(rec.Body.Bytes(), &body)
			return rec.Code, body
		}
	}
	t.Fatal("GET /api/graph/containment is not registered")
	return 0, nil
}

// tokens names the tokens of records for the log: A, B or other.
func tokens(records []apiRecord, names map[string]string) string {
	var out []string
	for _, r := range records {
		n, ok := names[r.token]
		if !ok {
			n = "other"
		}
		out = append(out, r.method+" "+r.path+" "+n)
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// callPaths is the fixed request sequence of one call in namespace ns on the fake server
// (D.3: /apis and the seven LISTs).
func callPaths(ns string) []string {
	return []string{"/apis",
		"/api/v1/namespaces/" + ns + "/pods", "/apis/apps/v1/namespaces/" + ns + "/replicasets",
		"/apis/apps/v1/namespaces/" + ns + "/deployments", "/apis/apps/v1/namespaces/" + ns + "/statefulsets",
		"/apis/apps/v1/namespaces/" + ns + "/daemonsets", "/apis/batch/v1/namespaces/" + ns + "/jobs",
		"/apis/batch/v1/namespaces/" + ns + "/cronjobs",
		"/api/v1/namespaces/" + ns + "/services", "/api/v1/namespaces/" + ns + "/persistentvolumeclaims",
		"/apis/networking.k8s.io/v1/namespaces/" + ns + "/ingresses", "/apis/autoscaling/v2/namespaces/" + ns + "/horizontalpodautoscalers"}
}

// TestGraphRoute_LookupError (C11, G4 at the route): the owner GET answers 500; the route
// answers 502 with the GA-3 text and no nodes, never owner-missing.
func TestGraphRoute_LookupError(t *testing.T) {
	api := newGraphAPI(t)
	api.bodies["/api/v1/namespaces/"+graphNS+"/pods"] = `{"kind":"PodList","apiVersion":"v1","metadata":{},"items":[{"apiVersion":"v1","kind":"Pod","metadata":{"name":"p","namespace":"` + graphNS +
		`","uid":"p-1","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"r","uid":"rs-1","controller":true}]}}]}`
	api.bodies["/apis/apps/v1"] = `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"replicasets","singularName":"replicaset","namespaced":true,"kind":"ReplicaSet","verbs":["get","list"]}]}`
	api.status["/apis/apps/v1/namespaces/"+graphNS+"/replicasets/r"] = 500
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	code, body := graphCall(t, s, "?namespace="+graphNS)
	want := graph.LoadText(graphNS, "500")
	_, hasNodes := body["nodes"]
	pass := code == http.StatusBadGateway && body["text"] == want && !hasNodes
	t.Logf("FA2-T5 C11 input=route-owner-get-500 expected=502 no-nodes actual=%d nodes-present=%v expected-text=%s actual-text=%v result=%s",
		code, hasNodes, want, body["text"], map[bool]string{true: "PASS", false: "FAIL"}[pass])
	if !pass {
		t.Errorf("G4 route: %d %v; want 502 with %q and no nodes", code, body, want)
	}
}

// TestGraphRoute_Namespace (GA-7): a missing namespace or one that is not a DNS-1123 label
// answers 400 before any cluster read.
func TestGraphRoute_Namespace(t *testing.T) {
	api := newGraphAPI(t)
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	for _, q := range []string{"", "?namespace=", "?namespace=Team_A", "?namespace=" + strings.Repeat("a", 64)} {
		before := api.count()
		code, _ := graphCall(t, s, q)
		reads := len(api.since(before))
		t.Logf("FA2-T5 GA-7 input=%q status %d (want 400), cluster requests %d (want 0)", q, code, reads)
		if code != http.StatusBadRequest || reads != 0 {
			t.Errorf("GA-7 %q: status %d, %d requests; want 400 and none", q, code, reads)
		}
	}
}

// TestGraphCall_RefreshAfter401 (C15/G8, D.4 "401 after connected", CLIENT-PER-REQ-T5):
// after a 401 the call repeats that request once with the new credential, and every
// request of the call after it carries only the new credential: A then B x 12.
func TestGraphCall_RefreshAfter401(t *testing.T) {
	api := newGraphAPI(t)
	runner := &fakeRunner{plans: []runPlan{{token: "tok-a"}, {token: "tok-b"}}}
	s := connectGraph(t, api, runner)
	before := api.count()
	api.reject("tok-a")
	code, _ := graphCall(t, s, "?namespace="+graphNS)
	got := api.since(before)
	names := map[string]string{"tok-a": "A", "tok-b": "B"}
	var seq []string
	for _, r := range got {
		seq = append(seq, names[r.token])
	}
	paths := append([]string{"/apis"}, callPaths(graphNS)...)
	pathsOK := len(got) == len(paths)
	for i := 0; pathsOK && i < len(got); i++ {
		pathsOK = got[i].path == paths[i]
	}
	t.Logf("FA2-T5 C15 input=401-after-connected answer %d, program starts %d, call record %s", code, runner.starts(), tokens(got, names))
	if code != 200 || runner.starts() != 2 || fmt.Sprint(seq) != "[A B B B B B B B B B B B B]" || !pathsOK {
		t.Errorf("401 after connected: answer %d, starts %d, tokens %v, paths in order %v; want 200, 2, [A B B B B B B B B B B B B] and the D.3 order with the one repeat", code, runner.starts(), seq, pathsOK)
	}
}

// TestGraphCall_RefreshInProgress (CLIENT-PER-REQ-T5 test (i)): a second call starts
// while the refresh of the first is running. Each call sends A at most once, on its first
// request, and only B after it.
func TestGraphCall_RefreshInProgress(t *testing.T) {
	api := newGraphAPI(t)
	started, release := make(chan struct{}), make(chan struct{})
	runner := &fakeRunner{plans: []runPlan{{token: "tok-a"}, {token: "tok-b", started: started, wait: release}}}
	s := connectGraph(t, api, runner)
	before := api.count()
	api.reject("tok-a")
	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); codes[0], _ = graphCall(t, s, "?namespace=one") }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("preparation: the refresh run of the first call did not start")
	}
	go func() { defer wg.Done(); codes[1], _ = graphCall(t, s, "?namespace=two") }()
	// The second call's first request has to arrive while the refresh runs.
	deadline := time.Now().Add(10 * time.Second)
	for len(api.since(before)) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	wg.Wait()
	got := api.since(before)
	names := map[string]string{"tok-a": "A", "tok-b": "B"}
	perCall := map[string][]string{}
	var apis []string
	for _, r := range got {
		if r.path == "/apis" {
			apis = append(apis, names[r.token])
			continue
		}
		for _, ns := range []string{"one", "two"} {
			if strings.Contains(r.path, "/namespaces/"+ns+"/") {
				perCall[ns] = append(perCall[ns], names[r.token])
			}
		}
	}
	t.Logf("FA2-T5 C15 input=refresh-in-progress answers %v, program starts %d; /apis tokens %v; call one LISTs %v; call two LISTs %v; record %s",
		codes, runner.starts(), apis, perCall["one"], perCall["two"], tokens(got, names))
	ok := codes[0] == 200 && codes[1] == 200 && runner.starts() == 2 && fmt.Sprint(apis) == "[A A B B]" &&
		fmt.Sprint(perCall["one"]) == "[B B B B B B B B B B B]" && fmt.Sprint(perCall["two"]) == "[B B B B B B B B B B B]"
	if !ok {
		t.Errorf("refresh in progress: answers %v, starts %d, /apis %v, LISTs %v %v; want 200 200, 2 starts, [A A B B] and eleven B LISTs per call",
			codes, runner.starts(), apis, perCall["one"], perCall["two"])
	}
}

// TestGraphCall_SwitchMidCall (CLIENT-PER-REQ-T5 test (ii)): a context switch while one
// request of the call is held. After the switch no request of the old generation goes
// out, and the answer is the gate's answer for the state then, with no nodes.
func TestGraphCall_SwitchMidCall(t *testing.T) {
	api := newGraphAPI(t)
	api.holdPath = "/apis/apps/v1/namespaces/" + graphNS + "/replicasets"
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	s.cluster.attempt = func(context.Context, string) readonly.Outcome {
		return readonly.Outcome{State: readonly.StateUnreachable, TextID: "K-UR", Message: "switch target of the test"}
	}
	before := api.count()
	type result struct {
		code int
		body map[string]any
	}
	done := make(chan result, 1)
	go func() {
		code, body := graphCall(t, s, "?namespace="+graphNS)
		done <- result{code, body}
	}()
	select {
	case <-api.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("preparation: the held request did not arrive")
	}
	s.cluster.start("other")
	switched := api.count()
	close(api.release)
	r := <-done
	after := api.since(switched)
	_, hasNodes := r.body["nodes"]
	t.Logf("FA2-T5 G4 input=switch-mid-call answer %d state %v nodes-present %v; requests before the switch %s; requests after the switch %d %v",
		r.code, r.body["state"], hasNodes, tokens(api.since(before)[:switched-before], nil), len(after), after)
	if len(after) != 0 || hasNodes || r.code != http.StatusServiceUnavailable {
		t.Errorf("switch mid-call: %d requests after the switch, answer %d, nodes present %v; want none, 503 and no nodes", len(after), r.code, hasNodes)
	}
}

// TestGraphCall_EndedByStateChange (CLIENT-PER-REQ-T5 test (iii), G4): a call whose
// refreshed credential is rejected again (K-AU), and one whose refresh run fails (K-EP),
// ends at that request: no request after it, 503 with the new state, no nodes.
func TestGraphCall_EndedByStateChange(t *testing.T) {
	for _, c := range []struct {
		name   string
		plans  []runPlan
		reject []string
		state  readonly.State
		record string
	}{
		{"K-AU", []runPlan{{token: "tok-a"}, {token: "tok-b"}}, []string{"tok-a", "tok-b"}, readonly.StateCredentialsRejected, "[A B]"},
		{"K-EP", []runPlan{{token: "tok-a"}, {fail: true}}, []string{"tok-a"}, readonly.StateCredentialProgramFailed, "[A]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newGraphAPI(t)
			runner := &fakeRunner{plans: c.plans}
			s := connectGraph(t, api, runner)
			before := api.count()
			api.reject(c.reject...)
			code, body := graphCall(t, s, "?namespace="+graphNS)
			got := api.since(before)
			names := map[string]string{"tok-a": "A", "tok-b": "B"}
			var seq []string
			for _, r := range got {
				seq = append(seq, names[r.token])
			}
			_, hasNodes := body["nodes"]
			t.Logf("FA2-T5 G4 input=ended-by-%s answer %d state %v nodes-present %v, program starts %d, call record %s",
				c.name, code, body["state"], hasNodes, runner.starts(), tokens(got, names))
			if code != http.StatusServiceUnavailable || body["state"] != string(c.state) || hasNodes || fmt.Sprint(seq) != c.record {
				t.Errorf("%s: answer %d state %v nodes %v record %v; want 503, %s, no nodes and %s", c.name, code, body["state"], hasNodes, seq, c.state, c.record)
			}
		})
	}
}

// TestGraphCall_SwitchAfterCheck (REDSPLIT-T5 amendment A2, CLIENT-PER-REQ-T5 point 3):
// the context is switched after the call's check that its generation is current and
// before the operation's client sends its request. The old generation has an unexpired
// credential. No request of the old generation may reach the old server, and the answer
// is the gate's answer with no nodes.
func TestGraphCall_SwitchAfterCheck(t *testing.T) {
	api := newGraphAPI(t)
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	s.cluster.attempt = func(context.Context, string) readonly.Outcome {
		return readonly.Outcome{State: readonly.StateUnreachable, TextID: "K-UR", Message: "switch target of the test"}
	}
	before := api.count()
	hooks := 0
	switched := -1
	s.cluster.afterAdmit = func() {
		hooks++
		if hooks != 1 {
			return
		}
		// The switch completes here, inside the window, before the operation's client is
		// made; the old request resumes only after start has returned.
		s.cluster.start("other")
		switched = api.count()
	}
	code, body := graphCall(t, s, "?namespace="+graphNS)
	got := api.since(before)
	_, hasNodes := body["nodes"]
	t.Logf("FA2-T5 G4 input=switch-after-check hook runs %d (want 1), requests at the switch %d, requests of the call on the old server %d %v (want 0), answer %d state %v nodes-present %v",
		hooks, switched-before, len(got), got, code, body["state"], hasNodes)
	if hooks != 1 {
		t.Errorf("switch after check: the hook ran %d times, want 1", hooks)
	}
	if len(got) != 0 {
		t.Errorf("switch after check: %d requests of the old generation reached the old server after the switch, want 0: %v", len(got), got)
	}
	if code != http.StatusServiceUnavailable || hasNodes {
		t.Errorf("switch after check: answer %d, nodes present %v; want 503 and no nodes", code, hasNodes)
	}
}
