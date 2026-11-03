// Command verifymap lints and runs the verification map under verify/.
//
//	verifymap lint [-root .]
//	verifymap run [-root .] -binary <path> -sums <SHA256SUMS> -expect-commit <40hex>
//	              -evidence <dir> [-ready-timeout 30s] [-poll 100ms] <feature-id>
//
// Flags come before the feature id.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var featureID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

func dispatch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: verifymap lint|run [flags]")
		return 2
	}
	switch args[0] {
	case "lint":
		fs := flag.NewFlagSet("lint", flag.ContinueOnError)
		fs.SetOutput(stderr)
		root := fs.String("root", ".", "repository root")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: verifymap lint [-root .]")
			return 2
		}
		return Lint(*root, stdout)
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.SetOutput(stderr)
		root := fs.String("root", ".", "repository root")
		binary := fs.String("binary", "", "executable under test")
		sums := fs.String("sums", "", "SHA256SUMS written by the build step")
		commit := fs.String("expect-commit", "", "git rev-parse HEAD of the checkout used for the build")
		evidence := fs.String("evidence", "", "directory for evidence files, kept after the run")
		ready := fs.Duration("ready-timeout", 30*time.Second, "readiness limit")
		poll := fs.Duration("poll", 100*time.Millisecond, "readiness polling interval")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 1 || !featureID.MatchString(fs.Arg(0)) || *binary == "" || *sums == "" || *commit == "" || *evidence == "" {
			fmt.Fprintln(stderr, "usage: verifymap run -binary <path> -sums <file> -expect-commit <40hex> -evidence <dir> <feature-id>")
			return 2
		}
		feature, err := ParseFeature(filepath.Join(*root, "verify", "features", fs.Arg(0)+".md"))
		if err != nil {
			fmt.Fprintf(stdout, "Run: FAIL feature %s: %v\n", fs.Arg(0), err)
			return 1
		}
		return Run(Options{
			Binary:       *binary,
			Sums:         *sums,
			ExpectCommit: *commit,
			EvidenceDir:  *evidence,
			Feature:      feature,
			ReadyTimeout: *ready,
			PollInterval: *poll,
		}, stdout)
	default:
		fmt.Fprintf(stderr, "verifymap: unknown subcommand %q\n", args[0])
		return 2
	}
}
