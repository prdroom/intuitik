// Command intuitik. Red commit: the server is not written yet; every call fails.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "intuitik: not built in the red commit")
	os.Exit(1)
}
