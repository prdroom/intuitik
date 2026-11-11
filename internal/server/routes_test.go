package server

import (
	"fmt"
	"testing"
)

// TestK_RoutesEnumerated (packet M1-T3a section 6.2): every route in the registry of
// the server is on the control allowlist or a cluster route, and controlRoute refuses a
// path off the allowlist at start.
func TestK_RoutesEnumerated(t *testing.T) {
	s := &server{}
	s.registerRoutes()
	var problems []string
	registered := map[string]bool{}
	for _, r := range s.routes {
		key := r.Method + " " + r.Path
		registered[key] = true
		switch {
		case r.Kind == routeControl && controlAllowed(r.Method, r.Path):
			t.Logf("K-ROUTE control %s: on the allowlist", key)
		case r.Kind == routeCluster:
			t.Logf("K-ROUTE cluster %s: behind the gate", key)
		default:
			problems = append(problems, fmt.Sprintf("%s (kind %d) is neither an allowlisted control route nor a cluster route", key, r.Kind))
		}
	}
	for _, a := range controlAllowlist {
		if !registered[a.method+" "+a.path] {
			problems = append(problems, fmt.Sprintf("allowlisted route %s %s is not registered", a.method, a.path))
		}
	}
	panicked := func() (p bool) {
		defer func() { p = recover() != nil }()
		(&server{}).controlRoute(Route{Method: "GET", Path: "/api/not-on-the-allowlist"})
		return false
	}()
	t.Logf("K-ROUTE controlRoute(GET /api/not-on-the-allowlist) panicked: %v (want true)", panicked)
	if !panicked {
		problems = append(problems, "controlRoute accepted a path off the allowlist")
	}
	for _, p := range problems {
		t.Error(p)
	}
	t.Logf("K-ROUTE %d routes registered, %d problem(s)", len(s.routes), len(problems))
}
