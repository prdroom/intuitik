package server

import "net/http"

// Defect: a wrapper type around the cluster handler, which a control route could serve.
type wrapped struct {
	h clusterHandler
}

func (x wrapped) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.h(w, r, nil)
}
