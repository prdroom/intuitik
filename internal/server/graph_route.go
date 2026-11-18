package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/graph"
)

// containmentPath is the graph route of packet FA2-T5 D.1.
const containmentPath = "/api/graph/containment"

// registerClusterRoutes registers the product's cluster routes. R2 of REDSPLIT-T5
// (decision 974302a8): the graph route is still registered outside the cluster gate; it
// reads through the composite stub engine with one client for the whole call and no
// generation check (oneClientRead), and answers 200 with what the stub gives.
func (s *server) registerClusterRoutes() {
	s.routes = append(s.routes, Route{
		Method: http.MethodGet,
		Path:   containmentPath,
		Auth:   authSession,
		Kind:   routeCluster,
		Handle: func(s *server, w http.ResponseWriter, r *http.Request) { s.oneClientRead(w, r, s.handleContainment) },
	})
}

// handleContainment answers what the engine gives for the namespace of the request.
func (s *server) handleContainment(w http.ResponseWriter, r *http.Request, open graph.Open) {
	ns := r.URL.Query().Get("namespace")
	a, err := graph.Build(r.Context(), open, ns)
	if err != nil || a.Nodes == nil {
		a = graph.Answer{Namespace: ns, Nodes: []graph.Node{}, Refused: []graph.Refusal{}}
	}
	writeJSON(w, a)
}
