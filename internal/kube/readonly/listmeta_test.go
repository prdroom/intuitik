package readonly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// Tests of ListMetadata and the context namespace (packet FA2-T7a 6.2 and 6.8, job unit).
// Each request goes through a real guarded client to an httptest API server that records
// the method, the path and the Accept header. Every case prints one line
// "FA2-T7a 6.8 <case> ... result=<PASS|FAIL>".

// metaServer answers every request with one fixed status, media type and body.
type metaServer struct {
	mu          sync.Mutex
	requests    []string // "<method> <path> <accept>"
	status      int
	contentType string
	body        string
}

func (m *metaServer) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.requests = append(m.requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Accept"))
	status, ct, body := m.status, m.contentType, m.body
	m.mu.Unlock()
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	w.Write([]byte(body))
}

func metaClient(t *testing.T, m *metaServer) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(srv.Close)
	c, err := newClient(guarded(&rest.Config{Host: srv.URL, BearerToken: "token-6-8"}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func passText(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// metadataBody is a PartialObjectMetadataList with one item whose last-applied annotation,
// label and managedFields hold the canary.
func metadataBody(resource, canary string) string {
	applied, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "X", "data": map[string]any{"k": canary}})
	return fmt.Sprintf(`{"kind":"PartialObjectMetadataList","apiVersion":"meta.k8s.io/v1","metadata":{"resourceVersion":"7"},"items":[`+
		`{"kind":"PartialObjectMetadata","apiVersion":"meta.k8s.io/v1","metadata":{"name":"%s-one","namespace":"shop","uid":"uid-%s-one",`+
		`"labels":{"canary":%q},"annotations":{"kubectl.kubernetes.io/last-applied-configuration":%q},`+
		`"managedFields":[{"manager":"kubectl","operation":"Apply","fieldsType":"FieldsV1","fieldsV1":{"f:data":{"f:k":{}}}}]}}]}`,
		resource, resource, canary, string(applied))
}

func listMetadataCase(t *testing.T, resource string) {
	canary := strings.Join([]string{"intuitik", "metadata", "canary", resource}, "-")
	m := &metaServer{status: 200, contentType: "application/json", body: metadataBody(resource, canary)}
	c := metaClient(t, m)
	got, err := c.ListMetadata(context.Background(), Resource{Version: "v1", Resource: resource}, "shop")
	want := []ObjectMeta{{Name: resource + "-one", Namespace: "shop", UID: "uid-" + resource + "-one"}}
	wantReq := "GET /api/v1/namespaces/shop/" + resource + " " + metadataAccept
	result, _ := json.Marshal(got)
	found, control := strings.Count(string(result), canary), strings.Count(m.body, canary)
	pass := err == nil && fmt.Sprint(got) == fmt.Sprint(want) && len(m.requests) == 1 && m.requests[0] == wantReq && found == 0 && control >= 1
	t.Logf("FA2-T7a 6.8 %s expected-request=%q actual-requests=%q expected=%v actual=%v error=%v canary in the result %d, positive control in the body %d result=%s",
		resource, wantReq, m.requests, want, got, err, found, control, passText(pass))
	if !pass {
		t.Errorf("ListMetadata %s: requests %q, result %v, error %v, canary %d (control %d)", resource, m.requests, got, err, found, control)
	}
}

// TestListMetadata_ConfigMaps (6.2, 6.8): one GET of the collection path with the
// metadata Accept exactly; the result carries name, namespace and uid only.
func TestListMetadata_ConfigMaps(t *testing.T) { listMetadataCase(t, "configmaps") }

// TestListMetadata_Secrets (6.2, 6.8): the same for Secrets.
func TestListMetadata_Secrets(t *testing.T) { listMetadataCase(t, "secrets") }

// TestListMetadata_Errors (6.2 (ii), (iii), 6.8): a non-2xx answer is the API status error
// with its code; a 200 answer that is a plain list, of another apiVersion or not JSON is
// an error with no item; there is no fallback.
func TestListMetadata_Errors(t *testing.T) {
	status := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`
	for _, c := range []struct {
		name, resource, contentType, body string
		status, code                      int
		refused                           bool
	}{
		{"403", "configmaps", "application/json", status, 403, 403, false},
		{"406", "configmaps", "application/json", `{"kind":"Status","apiVersion":"v1","status":"Failure","code":406}`, 406, 406, false},
		{"500", "secrets", "application/json", "", 500, 500, false},
		{"plain-configmaplist", "configmaps", "application/json", `{"kind":"ConfigMapList","apiVersion":"v1","items":[{"metadata":{"name":"a","namespace":"shop","uid":"u"},"data":{"k":"v"}}]}`, 200, 0, false},
		{"plain-secretlist", "secrets", "application/json", `{"kind":"SecretList","apiVersion":"v1","items":[{"metadata":{"name":"a","namespace":"shop","uid":"u"}}]}`, 200, 0, false},
		{"wrong-apiversion", "configmaps", "application/json", `{"kind":"PartialObjectMetadataList","apiVersion":"v1","items":[{"metadata":{"name":"a","namespace":"shop","uid":"u"}}]}`, 200, 0, false},
		{"not-json", "configmaps", "application/json", `not json`, 200, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &metaServer{status: c.status, contentType: c.contentType, body: c.body}
			got, err := metaClient(t, m).ListMetadata(context.Background(), Resource{Version: "v1", Resource: c.resource}, "shop")
			code := 0
			var st apierrors.APIStatus
			if errors.As(err, &st) {
				code = int(st.Status().Code)
			}
			pass := err != nil && got == nil && code == c.code && errors.Is(err, errRefused) == c.refused
			t.Logf("FA2-T7a 6.8 errors input=%s expected=error code=%d refused=%v no-items actual=error=%v code=%d refused=%v items=%d result=%s",
				c.name, c.code, c.refused, err != nil, code, errors.Is(err, errRefused), len(got), passText(pass))
			if !pass {
				t.Errorf("%s: error %v (code %d, refused %v), items %v", c.name, err, code, errors.Is(err, errRefused), got)
			}
		})
	}
}

// TestOutcome_ContextNamespace (6.5, 6.8): the namespace of the kubeconfig context is in
// Outcome.Namespace and ContextEntry.Namespace; "" when the context names none.
func TestOutcome_ContextNamespace(t *testing.T) {
	for _, c := range []struct{ name, namespace string }{{"with-namespace", "shop"}, {"without-namespace", ""}} {
		t.Run(c.name, func(t *testing.T) {
			srv := newTLSFakeAPI(t, false)
			cfg := kubeconfig(srv.srv.URL, srv.caPEM(), map[string]any{"token": "token-6-8"})
			if c.namespace != "" {
				cfg["contexts"] = []any{map[string]any{"name": "ctx", "context": map[string]any{"cluster": "cl", "user": "u", "namespace": c.namespace}}}
			}
			useKubeconfig(t, cfg)
			o := Attempt(context.Background(), "")
			entry := ""
			for _, e := range o.Contexts {
				if e.Name == "ctx" {
					entry = e.Namespace
				}
			}
			pass := o.Namespace == c.namespace && entry == c.namespace
			t.Logf("FA2-T7a 6.8 context-namespace input=%s state=%s expected=%q actual-outcome=%q actual-entry=%q result=%s", c.name, o.State, c.namespace, o.Namespace, entry, passText(pass))
			if !pass {
				t.Errorf("%s: Outcome.Namespace %q, ContextEntry.Namespace %q, want %q", c.name, o.Namespace, entry, c.namespace)
			}
		})
	}
}
