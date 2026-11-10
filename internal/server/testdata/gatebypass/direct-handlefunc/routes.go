package server

import "net/http"

// Defect: a route registered with HandleFunc directly, outside controlRoute and
// clusterRoute.
func registerExtra(mux *http.ServeMux) {
	mux.HandleFunc("/api/extra", func(w http.ResponseWriter, r *http.Request) {})
}
