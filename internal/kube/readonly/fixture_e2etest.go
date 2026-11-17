//go:build e2etest

package readonly

import (
	"fmt"
	"os"
)

// The negative fixtures of packet M1-T3b, selected by the same name variable as every
// negative fixture (INTUITIK_TEST_FIXTURE, see cmd/intuitik/seams_e2etest.go and
// internal/server/fixture_e2etest.go). They exist only in the e2etest build.
//   - refresh-output-leak (NEGFIX-T3b): a refresh run's output goes into its K-EP text.
//   - kubeconfig-write-after-exec (KWFIX-T3b): after an exec run gave a credential, the
//     product process appends a YAML comment to the context's kubeconfig file.
func init() {
	switch os.Getenv("INTUITIK_TEST_FIXTURE") {
	case "refresh-output-leak":
		refreshOutputText = func(stdout []byte) string { return ": " + string(stdout) }
	case "kubeconfig-write-after-exec":
		afterExecRun = writeAfterExec
	}
}

// writeAfterExec appends a YAML comment to path (the parsed kubeconfig stays the same)
// and reports the write on stderr.
func writeAfterExec(path string) {
	err := appendComment(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fixture kubeconfig-write-after-exec: write failed %s: %v\n", path, err)
		return
	}
	fmt.Fprintf(os.Stderr, "fixture kubeconfig-write-after-exec: wrote %s\n", path)
}

func appendComment(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("\n# intuitik negative fixture kubeconfig-write-after-exec\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
