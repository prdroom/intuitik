package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prdroom/intuitik/internal/graph"
)

// containmentPath is the graph route of packet FA2-T5 D.1.
const containmentPath = "/api/graph/containment"

// graphLimit bounds one graph call. A lookup still without an answer then fails the call
// with the reason "no answer" (GA-3).
const graphLimit = 30 * time.Second

// registerClusterRoutes registers the product's cluster routes.
func (s *server) registerClusterRoutes() {
	s.readRoute(http.MethodGet, containmentPath, s.handleContainment)
}

// handleContainment is GET /api/graph/containment?namespace=<name> (D.1): the containment
// answer of one namespace. A call that stopped because its generation is no longer the
// cluster's or no longer connected answers as the gate does for the state now (GA-2); a
// call that failed as a whole answers 502 with the GA-3 text and no nodes.
func (s *server) handleContainment(w http.ResponseWriter, r *http.Request, open graph.Open, current func() bool) {
	ns := r.URL.Query().Get("namespace")
	if !graph.ValidNamespace(ns) {
		writeJSONStatus(w, http.StatusBadRequest, struct{}{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), graphLimit)
	defer cancel()
	a, err := graph.Build(ctx, open, ns)
	if err == nil && current() {
		writeJSON(w, a)
		return
	}
	if err == nil || errors.Is(err, graph.ErrStopped) || !current() {
		_, obj := s.cluster.snapshot()
		writeJSONStatus(w, http.StatusServiceUnavailable, obj)
		return
	}
	var le *graph.LoadError
	if !errors.As(err, &le) {
		le = &graph.LoadError{Namespace: ns, Reason: graph.NoAnswer}
	}
	writeJSONStatus(w, http.StatusBadGateway, map[string]string{"namespace": ns, "text": le.Error()})
}
