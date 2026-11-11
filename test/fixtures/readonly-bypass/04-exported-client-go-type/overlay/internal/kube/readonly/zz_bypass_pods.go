package readonly

import corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

// Pods is bypass fixture 4 (packet M1-T4 R3): an exported name that returns a client-go
// interface, so that a caller outside could call Delete.
func Pods() corev1client.PodInterface {
	return nil
}
