// Command verifymap. Red commit: signatures only, every function reports failure.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

type Drive struct {
	Harness      string
	Path         string
	Status       int
	ContentType  string
	BodyContains string
}

type Feature struct {
	ID    string
	Drive Drive
}

type Options struct {
	Binary       string
	Sums         string
	ExpectCommit string
	EvidenceDir  string
	Feature      Feature
	ReadyTimeout time.Duration
	PollInterval time.Duration
	ExtraEnv     []string

	afterCleanup func(evidence []string)
}

func Lint(root string, out io.Writer) int { return 1 }

func Run(o Options, out io.Writer) int { return 1 }

func ParseFeature(path string) (Feature, error) { return Feature{}, errors.New("red commit") }

func parseDriveBlock(harness, block string) (Drive, error) { return Drive{}, nil }

func main() {
	fmt.Println("verifymap: red commit")
	os.Exit(1)
}
