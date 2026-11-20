package graph

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// podState is the node state of a core v1 Pod (FA2-T7b 3.1). Red stub of commit R (packet
// section 5, D-T7b-5): every core Pod is healthy and has no text.
func podState(obj *unstructured.Unstructured) NodeState {
	return NodeState{State: NodeHealthy}
}
