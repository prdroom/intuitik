// Command dco checks the standard DCO sign-off of every commit in base..head.
//
//	go run ./tools/verifymap/dco -base <40hex> -head <40hex>
//
// Output: one `DCO SKIP <sha> merge-commit` line per merge commit, one
// `DCO FAIL <sha> <reason>` line per failing commit, then `DCO RESULT checked=<n>
// failed=<k>`. Exit 0 when k = 0 and n > 0; 1 when k > 0; 2 for an argument format
// error, a git error, an empty range, or a range with no commit to judge.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dco", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base commit SHA (40 hex), excluded from the range")
	head := fs.String("head", "", "head commit SHA (40 hex)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stdout, "DCO ERROR unexpected arguments %q\n", fs.Args())
		return 2
	}
	if !validSHA(*base) || !validSHA(*head) {
		fmt.Fprintf(stdout, "DCO ERROR -base and -head must be 40 hex characters (base=%q head=%q)\n", *base, *head)
		return 2
	}
	fmt.Fprintf(stdout, "DCO range %s..%s\n", *base, *head)

	shas, err := commitRange(*base, *head)
	if err != nil {
		fmt.Fprintf(stdout, "DCO ERROR %v\n", err)
		return 2
	}
	if len(shas) == 0 {
		fmt.Fprintln(stdout, "DCO ERROR empty range")
		return 2
	}

	checked, failed := 0, 0
	for _, sha := range shas {
		c, err := readCommit(sha)
		if err != nil {
			fmt.Fprintf(stdout, "DCO ERROR %s: %v\n", sha, err)
			return 2
		}
		if c.Parents >= 2 {
			fmt.Fprintf(stdout, "DCO SKIP %s merge-commit\n", sha)
			continue
		}
		checked++
		if ok, reason := Judge(c.AuthorEmail, c.Message); !ok {
			failed++
			fmt.Fprintf(stdout, "DCO FAIL %s %s\n", sha, reason)
		}
	}
	if checked == 0 {
		fmt.Fprintln(stdout, "DCO ERROR no commit to judge in the range (merge commits only)")
		fmt.Fprintf(stdout, "DCO RESULT checked=%d failed=%d\n", checked, failed)
		return 2
	}
	fmt.Fprintf(stdout, "DCO RESULT checked=%d failed=%d\n", checked, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
