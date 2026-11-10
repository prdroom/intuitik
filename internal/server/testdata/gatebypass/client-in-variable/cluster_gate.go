// Package server is a negative fixture of TestK_ClusterRouteOnlyThroughGate.
package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

// Defect: the gate hands the client out in a package variable.
var lastClient *readonly.Client

func gate(w http.ResponseWriter, r *http.Request, o readonly.Outcome, h clusterHandler) {
	c, err := readonly.NewClient(o)
	if err != nil {
		return
	}
	lastClient = c
	h(w, r, c)
}
