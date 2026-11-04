//go:build !e2etest

package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/prdroom/intuitik/internal/server"
)

// testSeams is empty in the product build: no environment variable or flag changes
// the clock, the ports, the opener or the responses (TestSeams_ProductBuildHasNone).
func testSeams() (server.Seams, error) {
	var s server.Seams
	for _, p := range strings.Split(os.Getenv("INTUITIK_TEST_PORTS"), ",") {
		if n, err := strconv.Atoi(p); err == nil {
			s.Ports = append(s.Ports, n)
		}
	}
	return s, nil
}
