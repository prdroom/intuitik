package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// clusterHandler is the handler of a route that carries cluster data.
type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

// clusterRoute registers a route that carries cluster data.
func (s *server) clusterRoute(method, path string, h clusterHandler) {
	auth := authSession
	if method != http.MethodGet && method != http.MethodHead {
		auth = authStateful
	}
	s.routes = append(s.routes, Route{
		Method: method,
		Path:   path,
		Auth:   auth,
		Kind:   routeCluster,
		Handle: func(s *server, w http.ResponseWriter, r *http.Request) { s.gate(w, r, h) },
	})
}

// gate is a RED STUB (packet section 5): it calls the handler in every state.
func (s *server) gate(w http.ResponseWriter, r *http.Request, h clusterHandler) {
	o, _ := s.cluster.snapshot()
	h(w, r, clientFor(o))
}
