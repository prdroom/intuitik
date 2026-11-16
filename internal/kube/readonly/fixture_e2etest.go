//go:build e2etest

package readonly

import "os"

// The negative fixture refresh-output-leak of packet M1-T3b NEGFIX-T3b, selected by the
// same name variable as every negative fixture (INTUITIK_TEST_FIXTURE, see
// cmd/intuitik/seams_e2etest.go and internal/server/fixture_e2etest.go). It exists only
// in the e2etest build: a refresh run's output goes into its K-EP text.
func init() {
	if os.Getenv("INTUITIK_TEST_FIXTURE") == "refresh-output-leak" {
		refreshOutputText = func(stdout []byte) string { return ": " + string(stdout) }
	}
}
