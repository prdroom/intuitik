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

// readHandler is the handler of a cluster route that makes several cluster operations in
// one call (packet FA2-T5 D.3). open makes the client of one operation; current reports
// that the call's generation is still the cluster's and still connected.
type readHandler func(w http.ResponseWriter, r *http.Request, open graph.Open, current func() bool)

// readRoute registers a cluster route whose handler reads through readGate. Like every
// cluster route, it answers 503 with the cluster object while the state is not connected.
func (s *server) readRoute(method, path string, h readHandler) {
	s.routes = append(s.routes, Route{
		Method: method,
		Path:   path,
		Auth:   authSession,
		Kind:   routeCluster,
		Handle: func(s *server, w http.ResponseWriter, r *http.Request) { s.readGate(w, r, h) },
	})
}

// readGate binds one call to the generation that is connected when the call starts
// (CLIENT-PER-REQ-T5). Each call of open first checks that this generation is still the
// cluster's and still connected, then makes one new client on it with the constructor of
// the gate; it never takes a new snapshot, and no lock is held while the client is made.
// A failed check or constructor ends the call with graph.ErrStopped.
func (s *server) readGate(w http.ResponseWriter, r *http.Request, h readHandler) {
	o, obj, gen := s.cluster.admit()
	if o.State != readonly.StateConnected || obj.State != string(readonly.StateConnected) {
		writeJSONStatus(w, http.StatusServiceUnavailable, obj)
		return
	}
	current := func() bool { return s.cluster.current(gen) }
	open := func(ctx context.Context) (graph.Reader, error) {
		if !current() {
			return nil, graph.ErrStopped
		}
		if h := s.cluster.afterAdmit; h != nil {
			h()
		}
		c, err := readonly.NewClient(o)
		if err != nil {
			return nil, graph.ErrStopped
		}
		return c, nil
	}
	h(w, r, open, current)
}
