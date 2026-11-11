// Package server is a negative fixture of TestK_ClusterRouteOnlyThroughGate.
package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

// Defect: the client constructor passed as a function value to another function.
func gate(w http.ResponseWriter, r *http.Request, o readonly.Outcome, h clusterHandler) {
	build(w, r, o, h, readonly.NewClient)
}

func build(w http.ResponseWriter, r *http.Request, o readonly.Outcome, h clusterHandler, newClient func(readonly.Outcome) (*readonly.Client, error)) {
	c, err := newClient(o)
	if err != nil {
		return
	}
	h(w, r, c)
}
