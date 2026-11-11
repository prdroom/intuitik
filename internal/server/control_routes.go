package server

import "fmt"

// controlAllowlist is the fixed list of control routes (packet M1-T3a section 6.2):
// T2's login exchange and session routes and T3's cluster connection routes. T2's
// static assets are files of the embedded build, served by serveStatic and never
// registered as routes. A change to this list is a change to the packet.
var controlAllowlist = []struct{ method, path string }{
	{"POST", "/api/session"},
	{"GET", "/api/session"},
	{"POST", "/api/ping"},
	{"GET", "/api/ws"},
	{"GET", "/api/cluster"},
	{"POST", "/api/cluster/context"},
	{"POST", "/api/cluster/retry"},
}

func controlAllowed(method, path string) bool {
	for _, a := range controlAllowlist {
		if a.method == method && a.path == path {
			return true
		}
	}
	return false
}

// controlRoute registers a route that carries no cluster data. A route that is not on
// the allowlist panics at start.
func (s *server) controlRoute(r Route) {
	if !controlAllowed(r.Method, r.Path) {
		panic(fmt.Sprintf("controlRoute: %s %s is not on the control allowlist", r.Method, r.Path))
	}
	r.Kind = routeControl
	s.routes = append(s.routes, r)
}

// registerRoutes registers every route of the server. Routes are registered only with
// controlRoute and clusterRoute; the e2etest build appends its negative fixtures.
func (s *server) registerRoutes() {
	for _, r := range routeTable {
		s.controlRoute(r)
	}
	registerFixtureRoutes(s)
	for _, r := range s.o.Seams.Fixture.ExtraRoutes {
		r.Kind = routeFixture
		s.routes = append(s.routes, r)
	}
}
