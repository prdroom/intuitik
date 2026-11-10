// Package server is a negative fixture of TestK_ClusterRouteOnlyThroughGate.
package server

import (
	"context"
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

// Defect: a method value of the client stored in a package variable.
var clusterGet func(ctx context.Context, path string) (*http.Response, error)

func gate(w http.ResponseWriter, r *http.Request, o readonly.Outcome, h clusterHandler) {
	c, err := readonly.NewClient(o)
	if err != nil {
		return
	}
	clusterGet = c.Get
	h(w, r, c)
}
