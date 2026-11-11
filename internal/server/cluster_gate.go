package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// clusterHandler is the handler of a route that carries cluster data. Its only way to
// the cluster is the read-only client that the gate passes in (section 6.2).
type clusterHandler func(w http.ResponseWriter, r *http.Request, c *readonly.Client)

// clusterRoute registers a route that carries cluster data. While the state is not
// connected, the route answers 503 with the cluster object and its handler is never
// called (D1). Every route besides the control allowlist is a cluster route.
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

// gate creates the read-only client of the active context for one request.
func (s *server) gate(w http.ResponseWriter, r *http.Request, h clusterHandler) {
	o, obj := s.cluster.snapshot()
	if o.State != readonly.StateConnected || obj.State != string(readonly.StateConnected) {
		writeJSONStatus(w, http.StatusServiceUnavailable, obj)
		return
	}
	c, err := readonly.NewClient(o)
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, obj)
		return
	}
	h(w, r, c)
}
