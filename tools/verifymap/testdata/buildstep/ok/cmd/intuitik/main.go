// Fixture for the build step test: a program that builds on every target.
package main

import "fmt"

var (
	version = "unknown"
	commit  = "unknown"
)

func main() { fmt.Println(version, commit) }
