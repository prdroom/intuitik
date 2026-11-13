// Command readonlygate is the static read-only gate of packet M1-T4 (rules R1 to R5 and
// R7). The readonly-gate CI job runs it in the gate environment of section 6.1:
//
//	readonlygate self-test -report <file> -targets "<goos/goarch ...>" -tagsets "<release e2etest>" <fixtures dir>
//	readonlygate check -report <file> -tagset <release|e2etest> <module dir>
//
// check runs every rule on one combination: GOOS, GOARCH and CGO_ENABLED come from the
// environment, the tag set from -tagset (release is the build job's command, no
// -tags). check also compares the module graph of the head with the graph of the base
// (moduleGraphCheck) and reports its problems under the third-party rule. self-test
// runs check on each bypass fixture and fails unless every fixture is caught by its
// rule, and runs the module graph comparison on generated graphs. Each rule appends one go test -json style event, pass or fail,
// named "readonly-gate: <rule>", to the report file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: readonlygate self-test|check ...")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	report := fs.String("report", "", "file that receives one JSON event per rule")
	tagset := fs.String("tagset", "release", "check: the tag set (release or a -tags value)")
	targets := fs.String("targets", "", "self-test: the GOOS/GOARCH targets")
	tagsets := fs.String("tagsets", "release", "self-test: the tag sets")
	fs.Parse(os.Args[2:])
	if fs.NArg() != 1 || *report == "" {
		fmt.Fprintln(os.Stderr, "readonlygate: one directory and -report are required")
		os.Exit(2)
	}
	var ok bool
	switch os.Args[1] {
	case "check":
		before := byteCheck("before")
		results := check(fs.Arg(0), os.Environ(), *tagset)
		env := os.Environ()
		graph := moduleGraphCheck(fs.Arg(0), env, getenv(env, "GOOS") == "linux" && getenv(env, "GOARCH") == "amd64" && *tagset == "release")
		after := byteCheck("after")
		ok = true
		for _, r := range results {
			if !before || !after {
				r.fail("byte check: the root go.mod or go.sum differs from HEAD")
			}
			if r.rule == ruleThirdParty {
				for _, p := range graph {
					r.fail("%s", p)
				}
			}
			fmt.Printf("readonly-gate: %s %s %s\n", r.rule, combo(os.Environ(), *tagset), r.line())
			emit(*report, r.rule, r.ok())
			ok = ok && r.ok()
		}
	case "self-test":
		before := byteCheck("before")
		ok = selfTest(fs.Arg(0), strings.Fields(*targets), strings.Fields(*tagsets))
		ok = moduleGraphSelfTest() && ok
		after := byteCheck("after")
		ok = ok && before && after
		emit(*report, "self-test", ok)
	default:
		fmt.Fprintln(os.Stderr, "readonlygate: unknown command "+os.Args[1])
		os.Exit(2)
	}
	if !ok {
		os.Exit(1)
	}
}

// emit appends one test event to the report file.
func emit(report, rule string, ok bool) {
	action := "pass"
	if !ok {
		action = "fail"
	}
	b, _ := json.Marshal(map[string]string{"Action": action, "Test": "readonly-gate: " + rule})
	f, err := os.OpenFile(report, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "readonlygate: report:", err)
		os.Exit(2)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "readonlygate: report:", err)
		os.Exit(2)
	}
}

// combo names a combination for the output lines.
func combo(env []string, tagset string) string {
	return fmt.Sprintf("GOOS=%s GOARCH=%s CGO_ENABLED=%s tagset=%s", getenv(env, "GOOS"), getenv(env, "GOARCH"), getenv(env, "CGO_ENABLED"), tagset)
}

// getenv returns the last value of key in env.
func getenv(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			v = kv[len(key)+1:]
		}
	}
	return v
}

// byteCheck compares git hash-object --no-filters of the root go.mod and go.sum (the
// working directory) with their blobs at HEAD and prints both values (exception
// 3e37ae69). It runs before the first and after the last Go command of an invocation,
// so a Go command that writes checksums into the work tree fails the gate.
func byteCheck(stage string) bool {
	ok := true
	for _, f := range []string{"go.mod", "go.sum"} {
		wt, err1 := gitOut("hash-object", "--no-filters", f)
		head, err2 := gitOut("rev-parse", "HEAD:"+f)
		verdict := "equal"
		if err1 != nil || err2 != nil || wt != head {
			verdict = fmt.Sprintf("DIFFERENT (errors: %v, %v)", err1, err2)
			ok = false
		}
		fmt.Printf("readonly-gate: byte check %s %s work tree %s HEAD %s %s\n", stage, f, wt, head, verdict)
	}
	return ok
}

func gitOut(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}
