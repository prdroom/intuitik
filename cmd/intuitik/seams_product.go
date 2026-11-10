//go:build !e2etest

package main

import "github.com/prdroom/intuitik/internal/server"

// testSeams is empty in the product build: no environment variable or flag changes
// the clock, the ports, the opener or the responses (TestSeams_ProductBuildHasNone).
func testSeams() (server.Seams, error) {
	return server.Seams{}, nil
}
