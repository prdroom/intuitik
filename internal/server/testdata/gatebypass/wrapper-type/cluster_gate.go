// Package server is a negative fixture of TestK_ClusterRouteOnlyThroughGate. This file
// is the legitimate gate; the other file of the fixture holds the defect.
package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

func gate(w http.ResponseWriter, r *http.Request, o readonly.Outcome, h clusterHandler) {
	c, err := readonly.NewClient(o)
	if err != nil {
		return
	}
	h(w, r, c)
}
