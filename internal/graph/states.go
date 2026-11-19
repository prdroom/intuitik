package graph

import (
	"context"
	"net/http"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// configKinds are the two kinds a call reads as metadata only (FA2-T7a 6.2, 6.3 (6)).
var configKinds = []listedKind{
	{readonly.Resource{Version: "v1", Resource: "configmaps"}, "ConfigMap"},
	{readonly.Resource{Version: "v1", Resource: "secrets"}, "Secret"},
}

// volumeKinds are the cluster-wide kinds a call lists when the namespace has a claim
// (6.3 (7)).
var volumeKinds = []listedKind{
	{readonly.Resource{Version: "v1", Resource: "persistentvolumes"}, "PersistentVolume"},
	{readonly.Resource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}, "StorageClass"},
}

// namespaceResource is the resource of the Namespace object a call reads (6.3 (8)).
var namespaceResource = readonly.Resource{Version: "v1", Resource: "namespaces"}

// rootCA is the ConfigMap the cluster puts in every namespace; it is not a user resource
// (FA2-T7a 3.4).
const rootCA = "kube-root-ca.crt"

// listedAs records how the namespaced LIST of k ended.
func (c *call) listedAs(k listedKind, listed bool) {
	c.lists[k.res.Group+"/"+k.kind] = listed
	c.nsLists++
	if !listed {
		c.nsRefused++
	}
}

// refusedKind reports a namespaced LIST of the call that was refused (403).
func (c *call) refusedKind(group, kind string) bool {
	listed, known := c.lists[group+"/"+kind]
	return known && !listed
}

// readStates sends the requests of 6.3 (6) to (8): the ConfigMaps and Secrets of the
// namespace as metadata only, the PersistentVolumes and StorageClasses when the namespace
// has a claim, and the Namespace object. A 403 on a LIST adds the GA-4 entry and the call
// goes on; any other LIST failure fails the call with the kind named. The Namespace GET
// gives the namespace state: any HTTP status is kept, any other end fails the call.
func (c *call) readStates(ctx context.Context, refused *[]Refusal) error {
	for _, k := range configKinds {
		items, err := c.listConfig(ctx, k)
		if err != nil {
			if code, ok := statusCode(err); ok && code == http.StatusForbidden {
				*refused = append(*refused, Refusal{Kind: k.kind, Status: code, Reason: strconv.Itoa(code), Text: KindText(k.kind, code)})
				c.listedAs(k, false)
				continue
			}
			return c.listFail(ctx, err, k.kind)
		}
		c.listedAs(k, true)
		for _, m := range items {
			u := &unstructured.Unstructured{Object: map[string]any{}}
			u.SetAPIVersion("v1")
			u.SetKind(k.kind)
			u.SetName(m.Name)
			u.SetNamespace(m.Namespace)
			u.SetUID(types.UID(m.UID))
			e := c.add(u, k)
			e.state, e.parent, e.ref, e.text, e.placed = StateNone, "", "", "", true
		}
	}
	if c.hasClaims() {
		for _, k := range volumeKinds {
			list, err := c.listAt(ctx, k.res, "")
			if err != nil {
				if code, ok := statusCode(err); ok && code == http.StatusForbidden {
					*refused = append(*refused, Refusal{Kind: k.kind, Status: code, Reason: strconv.Itoa(code), Text: KindText(k.kind, code)})
					if k.kind == "PersistentVolume" {
						c.pvRefused = true
					} else {
						c.classRefused = true
					}
					continue
				}
				return c.listFail(ctx, err, k.kind)
			}
			for i := range list.Items {
				if k.kind == "PersistentVolume" {
					c.pvs[list.Items[i].GetName()] = &list.Items[i]
				} else {
					c.classes[list.Items[i].GetName()] = &list.Items[i]
				}
			}
		}
	}
	r, err := c.reader(ctx)
	if err != nil {
		return c.fail(err)
	}
	if _, err := r.Object(ctx, namespaceResource, "", c.ns); err != nil {
		code, ok := statusCode(err)
		if !ok {
			return c.fail(err)
		}
		c.nsStatus = code
		return nil
	}
	c.nsStatus = http.StatusOK
	return nil
}

// listConfig reads the ConfigMaps or Secrets of the namespace as metadata only: no value
// reaches the call (6.2).
func (c *call) listConfig(ctx context.Context, k listedKind) ([]readonly.ObjectMeta, error) {
	r, err := c.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.ListMetadata(ctx, k.res, c.ns)
}

// hasClaims reports a PersistentVolumeClaim among the nodes.
func (c *call) hasClaims() bool {
	for _, e := range c.entries {
		if e.kind == "PersistentVolumeClaim" && e.apiVersion == "v1" {
			return true
		}
	}
	return false
}

// clusterNodes adds the PersistentVolumes that a claim of the answer names and the
// StorageClasses that those claims or volumes name, as nodes with no namespace (3.3).
func (c *call) clusterNodes() {
	classes := map[string]bool{}
	for _, e := range c.sorted() {
		if e.kind != "PersistentVolumeClaim" || e.apiVersion != "v1" || e.obj == nil {
			continue
		}
		var spec struct {
			VolumeName       string `json:"volumeName"`
			StorageClassName string `json:"storageClassName"`
		}
		field(e, "spec", &spec)
		if spec.StorageClassName != "" {
			classes[spec.StorageClassName] = true
		}
		if pv := c.pvs[spec.VolumeName]; pv != nil {
			v := c.add(pv, volumeKinds[0])
			v.cluster, v.state, v.placed = true, StateNone, true
			var pvSpec struct {
				StorageClassName string `json:"storageClassName"`
			}
			field(v, "spec", &pvSpec)
			if pvSpec.StorageClassName != "" {
				classes[pvSpec.StorageClassName] = true
			}
		}
	}
	for name := range classes {
		if sc := c.classes[name]; sc != nil {
			v := c.add(sc, volumeKinds[1])
			v.cluster, v.state, v.placed = true, StateNone, true
		}
	}
}

// namespaceState is the state of the namespace of the answer (3.4, 6.5), in this order: the
// Namespace object is not found; every namespaced LIST was refused; every LIST succeeded
// and the only node, if any, is the ConfigMap kube-root-ca.crt; otherwise ok.
func (c *call) namespaceState(a *Answer) NamespaceState {
	st := NamespaceState{State: NamespaceOK, ExistenceChecked: c.nsStatus == http.StatusOK || c.nsStatus == http.StatusNotFound}
	switch {
	case c.nsStatus == http.StatusNotFound:
		st.State = NamespaceNotFound
	case c.nsLists > 0 && c.nsRefused == c.nsLists:
		st.State = NamespaceNoPermission
	case len(a.Refused) == 0 && onlyRootCA(a.Nodes):
		st.State = NamespaceEmpty
	}
	return st
}

// onlyRootCA reports that the nodes hold nothing but the ConfigMap kube-root-ca.crt.
func onlyRootCA(nodes []Node) bool {
	for _, n := range nodes {
		if !(n.Kind == "ConfigMap" && n.APIVersion == "v1" && n.Name == rootCA) {
			return false
		}
	}
	return true
}
