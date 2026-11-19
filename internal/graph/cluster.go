package graph

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// ClusterAnswer is the response of GET /api/graph/cluster (packet FA2-T7a 3.4, 6.5): every
// Namespace and the cluster band.
type ClusterAnswer struct {
	Namespaces NamespaceList `json:"namespaces"`
	Band       Band          `json:"band"`
	Refused    []Refusal     `json:"refused"`
}

// NamespaceList is the Namespaces of the cluster. When the LIST was refused, Listed is
// false and Items holds the namespace of the kubeconfig context (From "kubeconfig"), or
// "default" (From "default") when the context names none.
type NamespaceList struct {
	Listed bool     `json:"listed"`
	Items  []string `json:"items"`
	From   string   `json:"from"`
}

// Band is the cluster band. A part whose LIST was refused is absent, never zero.
type Band struct {
	Nodes             *NodeCount      `json:"nodes,omitempty"`
	StorageClasses    *[]StorageClass `json:"storageClasses,omitempty"`
	PersistentVolumes *PVCount        `json:"persistentVolumes,omitempty"`
}

// NodeCount is how many Nodes there are and how many are Ready.
type NodeCount struct {
	Total int `json:"total"`
	Ready int `json:"ready"`
}

// StorageClass is one StorageClass and whether it is the default one.
type StorageClass struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

// PVCount is how many PersistentVolumes there are.
type PVCount struct {
	Total int `json:"total"`
}

// The cluster-wide kinds of the cluster answer, in the order of 6.5.
var clusterKinds = []listedKind{
	{readonly.Resource{Version: "v1", Resource: "namespaces"}, "Namespace"},
	{readonly.Resource{Version: "v1", Resource: "nodes"}, "Node"},
	{readonly.Resource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}, "StorageClass"},
	{readonly.Resource{Version: "v1", Resource: "persistentvolumes"}, "PersistentVolume"},
}

// defaultClassAnnotations mark the default StorageClass (GA and beta forms).
var defaultClassAnnotations = []string{"storageclass.kubernetes.io/is-default-class", "storageclass.beta.kubernetes.io/is-default-class"}

// BuildCluster reads the cluster answer through open: one LIST of each kind of 6.5, each
// on its own client. contextNamespace is the namespace of the kubeconfig context ("" when
// it names none). A 403 adds a GA-4 refused entry and leaves that part out; any other
// failure fails the call with a *LoadError that names the kind; ErrStopped stays as it is.
func BuildCluster(ctx context.Context, open Open, contextNamespace string) (ClusterAnswer, error) {
	c := &call{open: open}
	a := ClusterAnswer{Refused: []Refusal{}}
	for _, k := range clusterKinds {
		list, err := c.list(ctx, k.res)
		if err != nil {
			if code, ok := statusCode(err); ok && code == http.StatusForbidden {
				a.Refused = append(a.Refused, Refusal{Kind: k.kind, Status: code, Reason: strconv.Itoa(code), Text: KindText(k.kind, code)})
				if k.kind == "Namespace" {
					a.Namespaces = NamespaceList{Listed: false, Items: []string{contextNamespace}, From: "kubeconfig"}
					if contextNamespace == "" {
						a.Namespaces = NamespaceList{Listed: false, Items: []string{"default"}, From: "default"}
					}
				}
				continue
			}
			if errors.Is(err, ErrStopped) {
				return ClusterAnswer{}, err
			}
			return ClusterAnswer{}, &LoadError{Kind: k.kind, Reason: reasonOf(err)}
		}
		switch k.kind {
		case "Namespace":
			names := []string{}
			for _, it := range list.Items {
				names = append(names, it.GetName())
			}
			sort.Strings(names)
			a.Namespaces = NamespaceList{Listed: true, Items: names, From: ""}
		case "Node":
			n := &NodeCount{Total: len(list.Items)}
			for _, it := range list.Items {
				var status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				}
				decode(it.Object["status"], &status)
				for _, cond := range status.Conditions {
					if cond.Type == "Ready" && cond.Status == "True" {
						n.Ready++
					}
				}
			}
			a.Band.Nodes = n
		case "StorageClass":
			classes := []StorageClass{}
			for _, it := range list.Items {
				def := false
				for _, an := range defaultClassAnnotations {
					def = def || it.GetAnnotations()[an] == "true"
				}
				classes = append(classes, StorageClass{Name: it.GetName(), Default: def})
			}
			sort.Slice(classes, func(i, j int) bool { return classes[i].Name < classes[j].Name })
			a.Band.StorageClasses = &classes
		case "PersistentVolume":
			a.Band.PersistentVolumes = &PVCount{Total: len(list.Items)}
		}
	}
	return a, nil
}
