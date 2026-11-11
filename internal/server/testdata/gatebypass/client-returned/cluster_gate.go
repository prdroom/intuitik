// Package server is a negative fixture of TestK_ClusterRouteOnlyThroughGate.
package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

func gate(w http.ResponseWriter, r *http.Request, o readonly.Outcome, h clusterHandler) {
	c, err := clientFor(o)
	if err != nil {
		return
	}
	h(w, r, c)
}

// Defect: the gate hands the client out as a return value.
func clientFor(o readonly.Outcome) (*readonly.Client, error) {
	return readonly.NewClient(o)
}
