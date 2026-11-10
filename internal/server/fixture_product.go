//go:build !e2etest

package server

// registerFixtureRoutes registers no route in the product build; the e2etest build
// adds the probe route of TestK_ClusterRouteGate (fixture_e2etest.go).
func registerFixtureRoutes(s *server) {}
