package server

import (
	"net/http"

	"github.com/prdroom/intuitik/internal/graph"
)

// containmentPath is the graph route of packet FA2-T5 D.1.
const containmentPath = "/api/graph/containment"

// registerClusterRoutes registers the product's cluster routes. R1 of REDSPLIT-T5
// (decision 974302a8): the graph route reads nothing; it is registered outside the
// cluster gate and answers 200 with no node and nothing refused.
func (s *server) registerClusterRoutes() {
	s.routes = append(s.routes, Route{
		Method: http.MethodGet,
		Path:   containmentPath,
		Auth:   authSession,
		Kind:   routeCluster,
		Handle: func(s *server, w http.ResponseWriter, r *http.Request) {
			writeJSON(w, graph.Answer{Namespace: r.URL.Query().Get("namespace"), Nodes: []graph.Node{}, Refused: []graph.Refusal{}})
		},
	})
}
