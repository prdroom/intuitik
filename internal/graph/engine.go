package graph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// R2 of REDSPLIT-T5 (decision 974302a8): the composite stub engine of packet FA2-T5
// section 5, read through the graph route. Each rule is the stub that section 5 names
// for one criterion:
//   - one client for the whole call (CLIENT-PER-REQ-T5 tests);
//   - custom kinds only from the CRD list, each read at its group's preferred version
//     (C17b (1), C17c); every failed group document reported as 404 (C17b (2)), and
//     never a failure of the call (C17b (3));
//   - a failed LIST left out silently (C17, refused stays empty);
//   - the last ownerReference decides (C12);
//   - an owner of kind Secret skipped (C14b);
//   - the owner looked up by name only, at a plural made from its kind unless the CRD list
//     names it (C6 to C8: a missing owner drops the child; G3: no discovery);
//   - found with the reference's UID: no parent and state none (C1 to C4, C13, C14);
//   - found with another UID: the child inside that object (C7, G2);
//   - 403 and every other lookup error: owner-missing (C9, C10, C11, G4);
//   - an object without owner inside the first object of the answer (C5);
//   - no sorting (G6).

// builtInGroups are the groups whose documents the stub does not read.
var builtInGroups = map[string]bool{
	"": true, "admissionregistration.k8s.io": true, "apiextensions.k8s.io": true, "apiregistration.k8s.io": true,
	"apps": true, "authentication.k8s.io": true, "authorization.k8s.io": true, "autoscaling": true, "batch": true,
	"certificates.k8s.io": true, "coordination.k8s.io": true, "discovery.k8s.io": true, "events.k8s.io": true,
	"flowcontrol.apiserver.k8s.io": true, "internal.apiserver.k8s.io": true, "lifecycle.k8s.io": true,
	"networking.k8s.io": true, "node.k8s.io": true, "policy": true, "rbac.authorization.k8s.io": true,
	"resource.k8s.io": true, "scheduling.k8s.io": true, "storage.k8s.io": true, "storagemigration.k8s.io": true,
	"metrics.k8s.io": true, "custom.metrics.k8s.io": true, "external.metrics.k8s.io": true,
}

var builtInKinds = []readonly.Resource{
	{Version: "v1", Resource: "pods"},
	{Group: "apps", Version: "v1", Resource: "replicasets"},
	{Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "apps", Version: "v1", Resource: "statefulsets"},
	{Group: "apps", Version: "v1", Resource: "daemonsets"},
	{Group: "batch", Version: "v1", Resource: "jobs"},
	{Group: "batch", Version: "v1", Resource: "cronjobs"},
}

// Build is the composite stub engine of R2.
func Build(ctx context.Context, open Open, namespace string) (Answer, error) {
	r, err := open(ctx)
	if err != nil {
		return Answer{}, err
	}
	a := Answer{Namespace: namespace, Nodes: []Node{}, Refused: []Refusal{}}
	preferred := map[string]string{}
	var groups metav1.APIGroupList
	if status, body, err := get(ctx, r, "/apis"); err == nil && status == http.StatusOK {
		json.Unmarshal(body, &groups)
	}
	for _, g := range groups.Groups {
		preferred[g.Name] = g.PreferredVersion.Version
		if builtInGroups[g.Name] {
			continue
		}
		for _, v := range g.Versions {
			if status, _, err := get(ctx, r, "/apis/"+g.Name+"/"+v.Version); err != nil || status != http.StatusOK {
				a.Refused = append(a.Refused, Refusal{Group: g.Name, Version: v.Version, Status: 404, Reason: "404", Text: GroupText(g.Name, v.Version, "404")})
			}
		}
	}
	plurals := map[string]string{} // "<group>/<kind>" -> plural
	kinds := append([]readonly.Resource{}, builtInKinds...)
	if list, err := r.List(ctx, readonly.Resource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}, ""); err == nil {
		for _, item := range list.Items {
			group, _, _ := unstructured.NestedString(item.Object, "spec", "group")
			plural, _, _ := unstructured.NestedString(item.Object, "spec", "names", "plural")
			kind, _, _ := unstructured.NestedString(item.Object, "spec", "names", "kind")
			scope, _, _ := unstructured.NestedString(item.Object, "spec", "scope")
			plurals[group+"/"+kind] = plural
			if scope == "Namespaced" {
				kinds = append(kinds, readonly.Resource{Group: group, Version: preferred[group], Resource: plural})
			}
		}
	}
	var objs []unstructured.Unstructured
	for _, k := range kinds {
		if list, err := r.List(ctx, k, namespace); err == nil {
			objs = append(objs, list.Items...)
		}
	}
	seen := map[string]bool{}
	var orphans []int
	for i := range objs {
		u := &objs[i]
		n := Node{UID: string(u.GetUID()), APIVersion: u.GetAPIVersion(), Kind: u.GetKind(), Name: u.GetName(), Owner: Owner{State: StateNone}}
		refs := u.GetOwnerReferences()
		if len(refs) == 0 {
			orphans = append(orphans, len(a.Nodes))
			a.Nodes = append(a.Nodes, n)
			seen[n.UID] = true
			continue
		}
		ref := refs[len(refs)-1]
		if ref.Kind != "Secret" {
			group, version := splitAPIVersion(ref.APIVersion)
			resource, ok := plurals[group+"/"+ref.Kind]
			if !ok {
				resource = strings.ToLower(ref.Kind) + "s"
			}
			ns := namespace
			if ref.Kind == "Node" {
				ns = ""
			}
			owner, err := r.Object(ctx, readonly.Resource{Group: group, Version: version, Resource: resource}, ns, ref.Name)
			switch {
			case err != nil && statusCode(err) == http.StatusNotFound:
				continue
			case err != nil:
				n.Owner = Owner{State: StateMissing, Reference: ref.Kind + "/" + ref.Name, Text: OwnerText(StateMissing, ref.Kind, ref.Name)}
			case string(owner.GetUID()) != string(ref.UID):
				p := string(owner.GetUID())
				n.Parent, n.Owner = &p, Owner{State: StateContained}
				if !seen[p] {
					seen[p] = true
					a.Nodes = append(a.Nodes, Node{UID: p, APIVersion: owner.GetAPIVersion(), Kind: owner.GetKind(), Name: owner.GetName(), Owner: Owner{State: StateNone}})
				}
			}
		}
		if !seen[n.UID] {
			seen[n.UID] = true
			a.Nodes = append(a.Nodes, n)
		}
	}
	for _, i := range orphans {
		for j := range a.Nodes {
			if j != i {
				p := a.Nodes[j].UID
				a.Nodes[i].Parent = &p
				break
			}
		}
	}
	return a, nil
}

// get sends one GET of an API path and reads its body.
func get(ctx context.Context, r Reader, path string) (int, []byte, error) {
	resp, err := r.Get(ctx, path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return resp.StatusCode, body, err
}

// statusCode is the HTTP status of an API error, or 0.
func statusCode(err error) int {
	var s apierrors.APIStatus
	if errors.As(err, &s) {
		return int(s.Status().Code)
	}
	return 0
}
