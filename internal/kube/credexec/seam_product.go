//go:build !e2etest

package credexec

// seams is empty in the product build: no environment variable changes the helper.
func seams() helperSeams {
	return helperSeams{}
}
