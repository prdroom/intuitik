package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// testNS is the namespace of every unit fixture.
const testNS = "team-a"

// fail is one answer of the fake API other than success: an HTTP status, or no answer.
type fail struct {
	status   int
	noAnswer bool
}

func (f fail) err() error {
	if f.noAnswer {
		return fmt.Errorf("fake API: %w", context.DeadlineExceeded)
	}
	return &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: int32(f.status), Message: http.StatusText(f.status)}}
}

// fakeResource is one resource of a discovery document of the fake API.
type fakeResource struct {
	group, version, name, kind string
	namespaced                 bool
	verbs                      []string
}

// fakeAPI is the cluster of the unit tests, behind the Reader interface. Each Open gives
// a new reader; a reader used for more than one request is recorded, so a test sees
// whether every operation had its own client.
type fakeAPI struct {
	mu        sync.Mutex
	groups    []metav1.APIGroup // the groups of /apis
	resources []fakeResource
	objects   []*unstructured.Unstructured
	apis      *fail
	docs      map[string]fail // "<group>/<version>" -> answer
	lists     map[string]fail // "<group>/<resource>" -> answer
	gets      map[string]fail // "<kind>/<name>" -> answer
	reversed  bool            // LIST answers in reverse order (G6)
	stopAfter int             // Open returns ErrStopped from this open on; 0 never
	requests  []string
	opens     int
	reused    []string

	// ownerVersion, when set for "<kind>/<name>", is the apiVersion of that object in
	// the answer of an owner GET, whatever version was requested (REDSPLIT-T5
	// amendment A2). Unset, an owner GET answers the requested version.
	ownerVersion map[string]string
}

func newFake() *fakeAPI {
	f := &fakeAPI{docs: map[string]fail{}, lists: map[string]fail{}, gets: map[string]fail{}}
	all := []string{"get", "list", "watch"}
	for _, r := range []fakeResource{
		{"", "v1", "pods", "Pod", true, all},
		{"", "v1", "secrets", "Secret", true, all},
		{"", "v1", "configmaps", "ConfigMap", true, all},
		{"", "v1", "nodes", "Node", false, all},
		{"", "v1", "pods/log", "Pod", true, []string{"get"}},
		{"", "v1", "services", "Service", true, all},
		{"", "v1", "persistentvolumeclaims", "PersistentVolumeClaim", true, all},
		{"networking.k8s.io", "v1", "ingresses", "Ingress", true, all},
		{"autoscaling", "v2", "horizontalpodautoscalers", "HorizontalPodAutoscaler", true, all},
		{"apps", "v1", "deployments", "Deployment", true, all},
		{"apps", "v1", "replicasets", "ReplicaSet", true, all},
		{"apps", "v1", "statefulsets", "StatefulSet", true, all},
		{"apps", "v1", "daemonsets", "DaemonSet", true, all},
		{"apps", "v1", "controllerrevisions", "ControllerRevision", true, all},
		{"batch", "v1", "jobs", "Job", true, all},
		{"batch", "v1", "cronjobs", "CronJob", true, all},
		{"apiextensions.k8s.io", "v1", "customresourcedefinitions", "CustomResourceDefinition", false, all},
	} {
		f.resources = append(f.resources, r)
	}
	for _, g := range []string{"apps", "batch", "apiextensions.k8s.io"} {
		f.groups = append(f.groups, metav1.APIGroup{Name: g, Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: g + "/v1", Version: "v1"}},
			PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: g + "/v1", Version: "v1"}})
	}
	return f
}

// crd serves kind (plural) in group at version; preferred sets the group's preferred
// version (the last call wins). The CustomResourceDefinition object is listed under
// apiextensions.k8s.io, as a cluster has it.
func (f *fakeAPI) crd(group, version, kind, plural, preferred string) {
	f.resources = append(f.resources, fakeResource{group, version, plural, kind, true, []string{"get", "list", "watch"}})
	f.resources = append(f.resources, fakeResource{group, version, plural + "/status", kind, true, []string{"get"}})
	listed := false
	for _, u := range f.objects {
		listed = listed || (u.GetKind() == "CustomResourceDefinition" && u.GetName() == plural+"."+group)
	}
	if !listed {
		f.objects = append(f.objects, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
			"metadata": map[string]any{"name": plural + "." + group, "uid": "crd-" + plural},
			"spec": map[string]any{"group": group, "scope": "Namespaced",
				"names": map[string]any{"kind": kind, "plural": plural}},
		}})
	}
	for i := range f.groups {
		if f.groups[i].Name == group {
			g := &f.groups[i]
			found := false
			for _, v := range g.Versions {
				found = found || v.Version == version
			}
			if !found {
				g.Versions = append(g.Versions, metav1.GroupVersionForDiscovery{GroupVersion: group + "/" + version, Version: version})
			}
			g.PreferredVersion = metav1.GroupVersionForDiscovery{GroupVersion: group + "/" + preferred, Version: preferred}
			return
		}
	}
	f.groups = append(f.groups, metav1.APIGroup{Name: group,
		Versions:         []metav1.GroupVersionForDiscovery{{GroupVersion: group + "/" + version, Version: version}},
		PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: group + "/" + preferred, Version: preferred}})
}

func (f *fakeAPI) add(objs ...*unstructured.Unstructured) *fakeAPI {
	f.objects = append(f.objects, objs...)
	return f
}

// obj is one object of the namespace (a Node is cluster-scoped).
func obj(apiVersion, kind, name, uid string, refs ...metav1.OwnerReference) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetName(name)
	if kind != "Node" {
		u.SetNamespace(testNS)
	}
	u.SetUID(types.UID(uid))
	if len(refs) > 0 {
		u.SetOwnerReferences(refs)
	}
	return u
}

func ref(apiVersion, kind, name, uid string, controller bool) metav1.OwnerReference {
	r := metav1.OwnerReference{APIVersion: apiVersion, Kind: kind, Name: name, UID: types.UID(uid)}
	if controller {
		t := true
		r.Controller = &t
	}
	return r
}

func ctl(apiVersion, kind, name, uid string) metav1.OwnerReference {
	return ref(apiVersion, kind, name, uid, true)
}

func (f *fakeAPI) open(ctx context.Context) (Reader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	if f.stopAfter > 0 && f.opens >= f.stopAfter {
		return nil, ErrStopped
	}
	return &fakeReader{f: f, id: f.opens}, nil
}

func (f *fakeAPI) build(t *testing.T) (Answer, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), contextLimit)
	defer cancel()
	return Build(ctx, f.open, testNS)
}

// fakeReader is the client of one operation.
type fakeReader struct {
	f    *fakeAPI
	id   int
	used int
}

func (r *fakeReader) record(req string) {
	r.f.requests = append(r.f.requests, req)
	r.used++
	if r.used > 1 {
		r.f.reused = append(r.f.reused, fmt.Sprintf("client %d: %s", r.id, req))
	}
}

func response(status int, v any) *http.Response {
	var body []byte
	if v != nil {
		body, _ = json.Marshal(v)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func (r *fakeReader) Get(ctx context.Context, path string) (*http.Response, error) {
	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	r.record("GET " + path)
	if path == "/apis" {
		if f.apis != nil {
			if f.apis.noAnswer {
				return nil, f.apis.err()
			}
			return response(f.apis.status, nil), nil
		}
		return response(200, metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}, Groups: f.groups}), nil
	}
	var group, version string
	switch {
	case strings.HasPrefix(path, "/api/"):
		version = strings.TrimPrefix(path, "/api/")
	case strings.HasPrefix(path, "/apis/"):
		group, version, _ = strings.Cut(strings.TrimPrefix(path, "/apis/"), "/")
	}
	if a, ok := f.docs[group+"/"+version]; ok {
		if a.noAnswer {
			return nil, a.err()
		}
		return response(a.status, nil), nil
	}
	list := metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"}, GroupVersion: apiVersion(group, version)}
	for _, res := range f.resources {
		if res.group == group && res.version == version {
			list.APIResources = append(list.APIResources, metav1.APIResource{Name: res.name, Kind: res.kind, Namespaced: res.namespaced, Verbs: res.verbs})
		}
	}
	if len(list.APIResources) == 0 {
		return response(404, nil), nil
	}
	return response(200, list), nil
}

// kindOf is the kind of a served resource, or "" when the group version does not serve it.
func (f *fakeAPI) kindOf(res readonly.Resource) string {
	for _, r := range f.resources {
		if r.group == res.Group && r.version == res.Version && r.name == res.Resource {
			return r.kind
		}
	}
	return ""
}

func (f *fakeAPI) match(res readonly.Resource, kind string, u *unstructured.Unstructured) bool {
	g, _ := splitAPIVersion(u.GetAPIVersion())
	return g == res.Group && u.GetKind() == kind
}

func (r *fakeReader) List(ctx context.Context, res readonly.Resource, namespace string) (*unstructured.UnstructuredList, error) {
	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	r.record(fmt.Sprintf("LIST %s/%s/%s %s", res.Group, res.Version, res.Resource, namespace))
	if a, ok := f.lists[res.Group+"/"+res.Resource]; ok {
		return nil, a.err()
	}
	kind := f.kindOf(res)
	if kind == "" {
		return nil, fail{status: 404}.err()
	}
	out := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": apiVersion(res.Group, res.Version), "kind": kind + "List"}}
	for _, u := range f.objects {
		if f.match(res, kind, u) && u.GetNamespace() == namespace {
			c := u.DeepCopy()
			c.SetAPIVersion(apiVersion(res.Group, res.Version))
			out.Items = append(out.Items, *c)
		}
	}
	if f.reversed {
		for i, j := 0, len(out.Items)-1; i < j; i, j = i+1, j-1 {
			out.Items[i], out.Items[j] = out.Items[j], out.Items[i]
		}
	}
	return out, nil
}

func (r *fakeReader) Object(ctx context.Context, res readonly.Resource, namespace, name string) (*unstructured.Unstructured, error) {
	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	r.record(fmt.Sprintf("GET %s/%s/%s %s/%s", res.Group, res.Version, res.Resource, namespace, name))
	kind := f.kindOf(res)
	if a, ok := f.gets[kind+"/"+name]; ok {
		return nil, a.err()
	}
	for _, u := range f.objects {
		if kind != "" && f.match(res, kind, u) && u.GetNamespace() == namespace && u.GetName() == name {
			c := u.DeepCopy()
			c.SetAPIVersion(apiVersion(res.Group, res.Version))
			if v, ok := f.ownerVersion[kind+"/"+name]; ok {
				c.SetAPIVersion(v)
			}
			return c, nil
		}
	}
	return nil, fail{status: 404}.err()
}

// count is the number of recorded requests that start with prefix.
func (f *fakeAPI) count(prefix string) int {
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}
