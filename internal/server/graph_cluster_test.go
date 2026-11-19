package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// Route tests of packet FA2-T7a 6.9 (job unit), on the graphAPI fake of graph_route_test.go:
// the end of a call at the Namespace GET, and the cluster route GET /api/graph/cluster. Each
// case prints one line "FA2-T7a 6.9 <case> ... result=<PASS|FAIL>".

// clusterBodies are the LIST answers of the cluster route on the fake.
var clusterBodies = map[string]string{
	"/api/v1/namespaces": `{"kind":"NamespaceList","apiVersion":"v1","metadata":{},"items":[{"metadata":{"name":"zeta","uid":"n-z"}},{"metadata":{"name":"alpha","uid":"n-a"}}]}`,
	"/api/v1/nodes": `{"kind":"NodeList","apiVersion":"v1","metadata":{},"items":[{"metadata":{"name":"n1","uid":"node-1"},` +
		`"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`,
	"/apis/storage.k8s.io/v1/storageclasses": `{"kind":"StorageClassList","apiVersion":"storage.k8s.io/v1","metadata":{},"items":[{"metadata":{"name":"standard","uid":"sc-1",` +
		`"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}},"provisioner":"p"}]}`,
	"/api/v1/persistentvolumes": `{"kind":"PersistentVolumeList","apiVersion":"v1","metadata":{},"items":[]}`,
}

func withClusterBodies(api *graphAPI) {
	for p, b := range clusterBodies {
		api.bodies[p] = b
	}
}

// clusterCall calls the registered cluster route with query and body and returns its
// status and raw body.
func clusterCall(t *testing.T, s *server, query string, body []byte) (int, []byte) {
	t.Helper()
	for _, rt := range s.routes {
		if rt.Method == http.MethodGet && rt.Path == "/api/graph/cluster" {
			rec := httptest.NewRecorder()
			rt.Handle(s, rec, httptest.NewRequest(http.MethodGet, "/api/graph/cluster"+query, bytes.NewReader(body)))
			return rec.Code, rec.Body.Bytes()
		}
	}
	t.Fatal("GET /api/graph/cluster is not registered")
	return 0, nil
}

func passWord(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// closeHeld closes the connections of every request that reaches the held path until stop
// closes, so a held request gets no answer, a retried one included.
func closeHeld(api *graphAPI, stop chan struct{}) {
	for {
		select {
		case <-api.arrived:
			api.srv.CloseClientConnections()
		case <-stop:
			return
		}
	}
}

// TestGraphCall_NamespaceGetSwitch (6.9): a context switch while the fake holds the
// Namespace GET: the answer is 503 with the gate object, never 200, and no request goes
// out after the switch.
func TestGraphCall_NamespaceGetSwitch(t *testing.T) {
	api := newGraphAPI(t)
	api.holdPath = "/api/v1/namespaces/" + graphNS
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	s.cluster.attempt = func(context.Context, string) readonly.Outcome {
		return readonly.Outcome{State: readonly.StateUnreachable, TextID: "K-UR", Message: "switch target of the test"}
	}
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
		t.Fatal("preparation: the Namespace GET did not arrive")
	}
	s.cluster.start("other")
	switched := api.count()
	close(api.release)
	r := <-done
	after := api.since(switched)
	_, hasNodes := r.body["nodes"]
	pass := r.code == http.StatusServiceUnavailable && len(after) == 0 && !hasNodes
	t.Logf("FA2-T7a 6.9 namespace-get-switch expected=503 no-request-after no-nodes actual=%d state=%v requests-after=%d nodes-present=%v result=%s",
		r.code, r.body["state"], len(after), hasNodes, passWord(pass))
	if !pass {
		t.Errorf("namespace GET switch: answer %d, %d requests after the switch %v, nodes %v; want 503, none, no nodes", r.code, len(after), after, hasNodes)
	}
}

// TestGraphCall_NamespaceGetNoAnswer (6.9): the fake closes the connection of the
// Namespace GET; the call ends as any request without an answer does: 502 with the
// no-answer text, never 200.
func TestGraphCall_NamespaceGetNoAnswer(t *testing.T) {
	api := newGraphAPI(t)
	api.holdPath = "/api/v1/namespaces/" + graphNS
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	stop := make(chan struct{})
	go closeHeld(api, stop)
	type result struct {
		code int
		body map[string]any
	}
	done := make(chan result, 1)
	go func() {
		code, body := graphCall(t, s, "?namespace="+graphNS)
		done <- result{code, body}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the call did not end within 60 s")
	}
	close(stop)
	close(api.release)
	want := "Couldn't load owners in " + graphNS + " (no answer)"
	_, hasNodes := r.body["nodes"]
	pass := r.code == http.StatusBadGateway && r.body["text"] == want && !hasNodes
	t.Logf("FA2-T7a 6.9 namespace-get-no-answer expected=502 no-nodes actual=%d nodes-present=%v expected-text=%s actual-text=%v result=%s",
		r.code, hasNodes, want, r.body["text"], passWord(pass))
	if !pass {
		t.Errorf("namespace GET without answer: %d %v; want 502 %q and no nodes", r.code, r.body, want)
	}
}

// TestClusterRoute_Session (6.9): without a bearer and with a wrong bearer the cluster
// route answers 401 as every session route, and the fake API receives no request.
func TestClusterRoute_Session(t *testing.T) {
	api := newGraphAPI(t)
	withClusterBodies(api)
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	s.host = "127.0.0.1:1"
	s.st.sessions = []string{"session-of-the-test"}
	before := api.count()
	for _, c := range []struct{ name, bearer string }{{"no-bearer", ""}, {"wrong-bearer", "not-the-session"}} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/api/graph/cluster", nil)
		req.Host = s.host
		if c.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+c.bearer)
		}
		rec := httptest.NewRecorder()
		s.serve(rec, req)
		sent := api.count() - before
		pass := rec.Code == http.StatusUnauthorized && sent == 0
		t.Logf("FA2-T7a 6.9 cluster-route-session input=%s expected=401 no-request actual=%d requests=%d result=%s", c.name, rec.Code, sent, passWord(pass))
		if !pass {
			t.Errorf("%s: answer %d, %d requests; want 401 and none", c.name, rec.Code, sent)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/api/graph/cluster", nil)
	req.Host = s.host
	req.Header.Set("Authorization", "Bearer session-of-the-test")
	rec := httptest.NewRecorder()
	s.serve(rec, req)
	t.Logf("FA2-T7a 6.9 cluster-route-session input=valid-session positive control answer %d (want 200) result=%s", rec.Code, passWord(rec.Code == 200))
	if rec.Code != 200 {
		t.Errorf("valid session: answer %d, want 200", rec.Code)
	}
}

// TestClusterRoute_SwitchMidCall (6.9): a switch while the fake holds the Nodes LIST: 503,
// no 200, no request after the switch.
func TestClusterRoute_SwitchMidCall(t *testing.T) {
	api := newGraphAPI(t)
	withClusterBodies(api)
	api.holdPath = "/api/v1/nodes"
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	s.cluster.attempt = func(context.Context, string) readonly.Outcome {
		return readonly.Outcome{State: readonly.StateUnreachable, TextID: "K-UR", Message: "switch target of the test"}
	}
	type result struct {
		code int
		body []byte
	}
	done := make(chan result, 1)
	go func() {
		code, body := clusterCall(t, s, "", nil)
		done <- result{code, body}
	}()
	select {
	case <-api.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("preparation: the Nodes LIST did not arrive")
	}
	s.cluster.start("other")
	switched := api.count()
	close(api.release)
	r := <-done
	after := api.since(switched)
	pass := r.code == http.StatusServiceUnavailable && len(after) == 0 && !strings.Contains(string(r.body), `"namespaces"`)
	t.Logf("FA2-T7a 6.9 cluster-route-switch expected=503 no-request-after actual=%d requests-after=%d result=%s", r.code, len(after), passWord(pass))
	if !pass {
		t.Errorf("cluster route switch: answer %d %s, %d requests after the switch; want 503 and none", r.code, r.body, len(after))
	}
}

// TestClusterRoute_NoInput (6.9): a query and a body change nothing: byte-identical JSON.
func TestClusterRoute_NoInput(t *testing.T) {
	api := newGraphAPI(t)
	withClusterBodies(api)
	s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
	c1, b1 := clusterCall(t, s, "", nil)
	c2, b2 := clusterCall(t, s, "?namespace=x&a=b", []byte(`{"namespace":"x"}`))
	pass := c1 == 200 && c2 == 200 && bytes.Equal(b1, b2) && strings.Contains(string(b1), `"namespaces"`)
	t.Logf("FA2-T7a 6.9 cluster-route-no-input answers %d and %d, bytes %d and %d, identical %v result=%s",
		c1, c2, len(b1), len(b2), bytes.Equal(b1, b2), passWord(pass))
	if !pass {
		t.Errorf("no input: %d %s / %d %s", c1, b1, c2, b2)
	}
}

// TestClusterRoute_LoadError (6.9): the Nodes LIST answers 500, or no answer: 502 with
// Couldn't load Nodes (<reason>).
func TestClusterRoute_LoadError(t *testing.T) {
	t.Run("500", func(t *testing.T) {
		api := newGraphAPI(t)
		withClusterBodies(api)
		delete(api.bodies, "/api/v1/nodes")
		api.status["/api/v1/nodes"] = 500
		s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
		code, body := clusterCall(t, s, "", nil)
		var v map[string]any
		json.Unmarshal(body, &v)
		want := "Couldn't load Nodes (500)"
		pass := code == http.StatusBadGateway && v["text"] == want
		t.Logf("FA2-T7a 6.9 cluster-route-load-error input=nodes-500 expected=502 actual=%d expected-text=%s actual-text=%v result=%s", code, want, v["text"], passWord(pass))
		if !pass {
			t.Errorf("nodes 500: %d %s; want 502 %q", code, body, want)
		}
	})
	t.Run("no-answer", func(t *testing.T) {
		api := newGraphAPI(t)
		withClusterBodies(api)
		api.holdPath = "/api/v1/nodes"
		s := connectGraph(t, api, &fakeRunner{plans: []runPlan{{token: "tok-a"}}})
		stop := make(chan struct{})
		go closeHeld(api, stop)
		type result struct {
			code int
			body []byte
		}
		done := make(chan result, 1)
		go func() {
			code, body := clusterCall(t, s, "", nil)
			done <- result{code, body}
		}()
		var r result
		select {
		case r = <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("the cluster call did not end within 60 s")
		}
		close(stop)
		close(api.release)
		var v map[string]any
		json.Unmarshal(r.body, &v)
		want := "Couldn't load Nodes (no answer)"
		pass := r.code == http.StatusBadGateway && v["text"] == want
		t.Logf("FA2-T7a 6.9 cluster-route-load-error input=nodes-no-answer expected=502 actual=%d expected-text=%s actual-text=%v result=%s", r.code, want, v["text"], passWord(pass))
		if !pass {
			t.Errorf("nodes no answer: %d %s; want 502 %q", r.code, r.body, want)
		}
	})
}
