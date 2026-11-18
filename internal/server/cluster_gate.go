package server

import (
	"context"
	"net/http"

	"github.com/prdroom/intuitik/internal/graph"
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
		// A failed credential program run may have changed the state just now.
		_, obj = s.cluster.snapshot()
		writeJSONStatus(w, http.StatusServiceUnavailable, obj)
		return
	}
	h(w, r, c)
}

// oneClientRead is R2 of REDSPLIT-T5 (decision 974302a8): it runs h with one client made
// from the current snapshot, without the gate's 503 answer and without a generation
// check, so every operation of the call uses that one client. When no client can be made
// the answer has no node.
func (s *server) oneClientRead(w http.ResponseWriter, r *http.Request, h func(w http.ResponseWriter, r *http.Request, open graph.Open)) {
	o, _ := s.cluster.snapshot()
	c, err := readonly.NewClient(o)
	if err != nil {
		writeJSON(w, graph.Answer{Namespace: r.URL.Query().Get("namespace"), Nodes: []graph.Node{}, Refused: []graph.Refusal{}})
		return
	}
	h(w, r, func(context.Context) (graph.Reader, error) { return c, nil })
}
