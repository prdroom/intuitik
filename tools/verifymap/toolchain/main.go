// Command toolchain. Red commit: signatures only, every function reports failure.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func parseNvmrc(s string) (string, error) { return "", errors.New("red commit") }

func parseToolVersion(s string) (string, error) { return "", errors.New("red commit") }

func npmForNode(index []byte, node string) (string, error) { return "", errors.New("red commit") }

func compareLine(out io.Writer, name, expected, actual, source string) bool { return false }

func main() {
	fmt.Println("toolchain: ERROR red commit")
	os.Exit(1)
}
