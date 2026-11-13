package readonly

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

func podObject(ns, name string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": ns, "resourceVersion": "5"}}
}

// TestR5_RequestGuard (packet M1-T4 R5): every write, upgrade, proxy, non-listed
// non-resource and impersonated request built through readonly is an error and the
// fake API server receives 0 requests, through the dynamic, typed, discovery and
// rest.CopyConfig paths. Positive control: the same requests without the guard are
// received. An allowed GET carries the User-Agent marker.
func TestR5_RequestGuard(t *testing.T) {
	ctx := context.Background()
	obj := &unstructured.Unstructured{Object: podObject("a", "p")}
	type attempt struct {
		name string
		do   func(cfg *rest.Config) error
	}
	rawGet := func(path string, header http.Header) func(cfg *rest.Config) error {
		return func(cfg *rest.Config) error {
			hc, err := rest.HTTPClientFor(cfg)
			if err != nil {
				return err
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Host+path, nil)
			for k, v := range header {
				req.Header[k] = v
			}
			resp, err := hc.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			return err
		}
	}
	dyn := func(cfg *rest.Config) dynamic.NamespaceableResourceInterface {
		c, err := dynamic.NewForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return c.Resource(podsGVR)
	}
	attempts := []attempt{
		{"dynamic POST (Create)", func(cfg *rest.Config) error {
			_, err := dyn(cfg).Namespace("a").Create(ctx, obj, metav1.CreateOptions{})
			return err
		}},
		{"dynamic PUT (Update)", func(cfg *rest.Config) error {
			_, err := dyn(cfg).Namespace("a").Update(ctx, obj, metav1.UpdateOptions{})
			return err
		}},
		{"dynamic PATCH (Patch)", func(cfg *rest.Config) error {
			_, err := dyn(cfg).Namespace("a").Patch(ctx, "p", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{})
			return err
		}},
		{"dynamic DELETE (Delete)", func(cfg *rest.Config) error {
			return dyn(cfg).Namespace("a").Delete(ctx, "p", metav1.DeleteOptions{})
		}},
		{"typed DELETE through rest.CopyConfig", func(cfg *rest.Config) error {
			cs, err := kubernetes.NewForConfig(rest.CopyConfig(cfg))
			if err != nil {
				return err
			}
			return cs.CoreV1().Pods("a").Delete(ctx, "p", metav1.DeleteOptions{})
		}},
		{"discovery client POST", func(cfg *rest.Config) error {
			dc, err := discovery.NewDiscoveryClientForConfig(cfg)
			if err != nil {
				return err
			}
			return dc.RESTClient().Post().AbsPath("/api/v1/namespaces/a/pods").Body([]byte(`{}`)).Do(ctx).Error()
		}},
		{"WebSocket exec (GET with Upgrade)", rawGet("/api/v1/namespaces/a/pods/p/exec?command=sh", http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}})},
		// Upgrade on an allowed path: the header check alone must refuse it, whatever the
		// case of the header field names in the map (F5, review 5885839864).
		{"GET /api with Upgrade", rawGet("/api", http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}})},
		{"GET /api with lower-case upgrade and connection keys", rawGet("/api", http.Header{"upgrade": {"websocket"}, "connection": {"upgrade"}})},
		{"GET /api with mixed-case upgrade and connection keys", rawGet("/api", http.Header{"uPgRaDe": {"websocket"}, "CONNECTION": {"keep-alive, Upgrade"}})},
		{"GET /api with a lower-case connection upgrade key only", rawGet("/api", http.Header{"connection": {"Upgrade"}})},
		{"GET pods/exec without Upgrade", rawGet("/api/v1/namespaces/a/pods/p/exec?command=sh", nil)},
		{"GET pods/attach", rawGet("/api/v1/namespaces/a/pods/p/attach", nil)},
		{"GET pods/portforward", rawGet("/api/v1/namespaces/a/pods/p/portforward", nil)},
		{"GET nodes/<n>/proxy", rawGet("/api/v1/nodes/n/proxy/x", nil)},
		{"GET services/<s>/proxy", rawGet("/api/v1/namespaces/a/services/s:80/proxy", nil)},
		{"GET /logs", rawGet("/logs", nil)},
		{"GET /metrics", rawGet("/metrics", nil)},
		{"kubeconfig as: (Impersonate-User)", func(cfg *rest.Config) error {
			c := rest.CopyConfig(cfg)
			c.Impersonate = rest.ImpersonationConfig{UserName: "someone-else"}
			_, err := dyn(c).Namespace("a").List(ctx, metav1.ListOptions{})
			return err
		}},
	}
	for _, a := range attempts {
		guardedSrv := newFakeAPI(t, false)
		err := a.do(guarded(guardedSrv.config("token-r5")))
		if err == nil || !errors.Is(err, errRefused) {
			t.Errorf("R5 %s: expected a guard refusal, got err=%v", a.name, err)
		}
		if n := guardedSrv.count(); n != 0 {
			t.Errorf("R5 %s: the fake API server received %d request(s) through readonly, expected 0", a.name, n)
		}
		openSrv := newFakeAPI(t, false)
		a.do(openSrv.config("token-r5"))
		if openSrv.count() < 1 {
			t.Errorf("R5 %s positive control: the server without the guard received %d, expected at least 1", a.name, openSrv.count())
		}
		t.Logf("R5 %s: guarded received=%d err=%v; guard-off positive control received=%d", a.name, guardedSrv.count(), err, openSrv.count())
	}

	// An allowed GET is sent once and carries the User-Agent marker.
	srv := newFakeAPI(t, false)
	srv.add("pods", podObject("a", "p"))
	c, err := newClient(guarded(srv.config("token-r5")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Object(ctx, Resource{Version: "v1", Resource: "pods"}, "a", "p"); err != nil {
		t.Fatalf("R5 allowed GET: %v", err)
	}
	_, agents, _ := srv.seen()
	if srv.count() != 1 || !strings.HasSuffix(agents[0], " "+userAgentMarker) {
		t.Errorf("R5 allowed GET: received=%d user agent %q, expected 1 request with the marker %q", srv.count(), agents, userAgentMarker)
	}
	t.Logf("R5 allowed GET: received=%d user agent %q", srv.count(), agents[0])
}

// TestR5_PathGrammar (packet M1-T4 R5 with P-d, hoan 2026-11-10): the guard interprets
// a path by position and refuses a non-canonical path before interpretation.
func TestR5_PathGrammar(t *testing.T) {
	cases := []struct {
		path string
		pass bool
	}{
		{"/api/v1/namespaces/a/pods/proxy", true},
		{"/api/v1/namespaces/log/pods", true},
		{"/api/v1/namespaces/a/pods/p/log", true},
		{"/apis/rbac.authorization.k8s.io/v1/clusterroles/system:Admin-X", true},
		{"/api", true},
		{"/apis", true},
		{"/api/v1", true},
		{"/apis/apps/v1", true},
		{"/version", true},
		{"/openapi/v2", true},
		{"/openapi/v3", true},
		{"/openapi/v3/apis/apps/v1", true},
		{"/api/v1/pods", true},
		{"/api/v1/namespaces", true},
		{"/api/v1/namespaces/a", true},
		{"/api/v1/namespaces/a/secrets/s", true},
		{"/apis/apps/v1/namespaces/a/deployments/d", true},
		{"/api/v1/namespaces/a/pods/p/proxy", false},
		{"/api/v1/nodes/n/proxy/x", false},
		{"/api/v1/nodes/n/proxy", false},
		{"/api/v1/namespaces/a/services/s:80/proxy", false},
		{"/api//v1/pods", false},
		{"/api/v1/pods/", false},
		{"/api/v1/namespaces/a%2Fpods", false},
		{"/api/v1/namespaces/a/pods/%2E%2E", false},
		{"/api/v1/./pods", false},
		{"/api/v1/../v1/pods", false},
		{"/API/v1/pods", false},
		{"/api/V1/pods", false},
		{"/api/v1/namespaces/A/pods", false},
		{"/api/v1/namespaces/a/pods/p/exec", false},
		{"/api/v1/namespaces/a/pods/p/attach", false},
		{"/api/v1/namespaces/a/pods/p/portforward", false},
		{"/api/v1/namespaces/a/status", false},
		{"/api/v1/namespaces/a/finalize", false},
		{"/api/v1/watch/pods", false},
		{"/apis/apps/v1/namespaces/a/deployments/d/scale", false},
		{"/logs", false},
		{"/metrics", false},
		{"/", false},
	}
	for _, c := range cases {
		_, err := classify(c.path)
		if (err == nil) != c.pass {
			t.Errorf("R5 grammar %s: pass=%v, expected pass=%v (err=%v)", c.path, err == nil, c.pass, err)
			continue
		}
		t.Logf("R5 grammar %s: pass=%v", c.path, err == nil)
	}
}

// TestR5_DiscoveryFlows (packet M1-T4 R5): with the guard, the real client-go discovery
// (ServerGroupsAndResources), a dynamic list and an informer reaching HasSynced succeed
// against a fake API server in aggregated (apidiscovery.k8s.io/v2) and in unaggregated
// mode, with 0 guard refusals.
func TestR5_DiscoveryFlows(t *testing.T) {
	for _, aggregated := range []bool{true, false} {
		srv := newFakeAPI(t, aggregated)
		srv.add("pods", podObject("a", "p1"))
		srv.add("pods", podObject("a", "p2"))
		cfg := guarded(srv.config("token-r5"))
		before := refusals.Load()

		dc, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, lists, err := dc.ServerGroupsAndResources()
		if err != nil {
			t.Errorf("R5 discovery aggregated=%v: %v", aggregated, err)
		}
		found := false
		for _, l := range lists {
			for _, r := range l.APIResources {
				found = found || r.Name == "pods"
			}
		}
		if !found {
			t.Errorf("R5 discovery aggregated=%v: pods not in %d resource lists", aggregated, len(lists))
		}

		c, err := newClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		list, err := c.List(context.Background(), Resource{Version: "v1", Resource: "pods"}, "a")
		if err != nil || len(list.Items) != 2 {
			t.Errorf("R5 dynamic list aggregated=%v: %v items, err=%v", aggregated, lenItems(list), err)
		}
		if _, err := c.Resources(); err != nil {
			t.Errorf("R5 readonly Resources aggregated=%v: %v", aggregated, err)
		}

		dyn, err := dynamic.NewForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, 0, "a", nil)
		informer := factory.ForResource(podsGVR).Informer()
		stop := make(chan struct{})
		factory.Start(stop)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		synced := cache.WaitForCacheSync(ctx.Done(), informer.HasSynced)
		cancel()
		close(stop)
		factory.Shutdown()
		if !synced {
			t.Errorf("R5 informer aggregated=%v: HasSynced not reached", aggregated)
		}
		refused := refusals.Load() - before
		if refused != 0 {
			reqs, _, _ := srv.seen()
			t.Errorf("R5 discovery flows aggregated=%v: %d guard refusal(s), expected 0; requests %v", aggregated, refused, reqs)
		}
		t.Logf("R5 discovery flows aggregated=%v: resource lists=%d dynamic list items=%d informer synced=%v guard refusals=%d requests=%d",
			aggregated, len(lists), lenItems(list), synced, refused, srv.count())
	}
}

func lenItems(l *unstructured.UnstructuredList) int {
	if l == nil {
		return 0
	}
	return len(l.Items)
}
