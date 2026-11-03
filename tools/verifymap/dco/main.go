// Command dco. Red commit: signatures only, every function reports failure.
package main

import (
	"errors"
	"fmt"
	"os"
)

type Commit struct {
	AuthorEmail string
	Parents     int
	Message     string
}

func Judge(authorEmail, message string) (bool, string) { return false, "red-commit" }

func ParseCommit(raw []byte) (Commit, error) { return Commit{}, errors.New("red commit") }

func validSHA(s string) bool { return false }

func main() {
	fmt.Println("DCO ERROR red commit")
	os.Exit(3)
}
