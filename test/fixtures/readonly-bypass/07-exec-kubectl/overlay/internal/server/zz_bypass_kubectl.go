package server

import "os/exec"

// Bypass fixture 7 (packet M1-T4 R4): os/exec running kubectl in a product package
// outside the allowed one.
func runKubectl() error {
	return exec.Command("kubectl", "get", "pods").Run()
}
