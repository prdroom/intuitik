package readonly

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// Bypass fixture 2 (packet M1-T4 R4): a write call through a type alias and a wrapper
// that returns the method value.
type resourceAlias = dynamic.ResourceInterface

func removeVia(r resourceAlias) func(context.Context, string, metav1.DeleteOptions, ...string) error {
	return r.Delete
}
