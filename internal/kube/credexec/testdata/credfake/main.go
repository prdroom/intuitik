// Command credfake is the test credential program of packet M1-T3b (section 6.1, "Fake
// programs"). The tests build it with go build; it is not part of the product.
//
// Every run appends "start <pid> <parent pid> <unix nanoseconds>" to $CREDFAKE_DIR/starts
// first, then writes $KUBERNETES_EXEC_INFO to $CREDFAKE_DIR/info-<n>.json, where n is the
// run's number counted from 1. It then follows the plan of that run: $CREDFAKE_RUN<n> if
// set, otherwise $CREDFAKE_RUN. A plan is a list of steps separated by ";":
//
//	stderr=<text>       write <text> and a line end to stderr
//	child=detached      start a descendant (a copy of this program at $CREDFAKE_CHILD) that
//	                    does not hold stdout; its PID goes to $CREDFAKE_DIR/descendants
//	child=holdpipe      the same, with stdout inherited, so the descendant holds the pipe
//	stdin               read stdin to its end; record "eof <bytes>" in stdin-<n>.txt
//	tty                 open the controlling terminal (/dev/tty) or console (CONIN$) and
//	                    read from it; exit 3 at once when it cannot be opened
//	jobcheck            Windows: record in jobhandles-<n>.txt how many job handles this
//	                    process holds
//	sleep=<duration>    sleep, for example 9s
//	hang                sleep for 10 minutes
//	exit=<code>         exit with <code> at once
//	token=<value>       the token of the credential; tokenfile=<path> reads it from a file
//	certfile=<path>     the client certificate (PEM) and keyfile=<path> its key
//	expiry=<seconds>    expirationTimestamp that many seconds from now
//	version=<apiVersion> apiVersion of the output; default: that of KUBERNETES_EXEC_INFO
//	raw=<path>          print the file instead of a credential
//	bytes=<n>           print n bytes of "x" instead of a credential
//
// Without raw or bytes it prints an ExecCredential with the collected status and exits 0.
// The first argument --credfake-descendant <dir> makes it a descendant: it records its
// PID and sleeps for 10 minutes.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--credfake-descendant" {
		appendLine(filepath.Join(os.Args[2], "descendants"), strconv.Itoa(os.Getpid()))
		time.Sleep(10 * time.Minute)
		return
	}
	dir := os.Getenv("CREDFAKE_DIR")
	n := appendLine(filepath.Join(dir, "starts"), fmt.Sprintf("start %d %d %d", os.Getpid(), os.Getppid(), time.Now().UnixNano()))
	info := os.Getenv("KUBERNETES_EXEC_INFO")
	os.WriteFile(filepath.Join(dir, fmt.Sprintf("info-%d.json", n)), []byte(info), 0o600)
	plan := os.Getenv(fmt.Sprintf("CREDFAKE_RUN%d", n))
	if plan == "" {
		plan = os.Getenv("CREDFAKE_RUN")
	}
	version := ""
	var in struct {
		APIVersion string `json:"apiVersion"`
	}
	if json.Unmarshal([]byte(info), &in) == nil {
		version = in.APIVersion
	}
	status := map[string]any{}
	for _, step := range strings.Split(plan, ";") {
		key, value, _ := strings.Cut(strings.TrimSpace(step), "=")
		switch key {
		case "":
		case "stderr":
			fmt.Fprintln(os.Stderr, value)
		case "child":
			startChild(dir, value == "holdpipe")
		case "stdin":
			b := make([]byte, 4096)
			total := 0
			for {
				k, err := os.Stdin.Read(b)
				total += k
				if err != nil {
					break
				}
			}
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("stdin-%d.txt", n)), []byte(fmt.Sprintf("eof %d", total)), 0o600)
		case "tty":
			readTerminal()
		case "jobcheck":
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("jobhandles-%d.txt", n)), []byte(strconv.Itoa(jobHandles())), 0o600)
		case "sleep":
			d, _ := time.ParseDuration(value)
			time.Sleep(d)
		case "hang":
			time.Sleep(10 * time.Minute)
		case "exit":
			code, _ := strconv.Atoi(value)
			os.Exit(code)
		case "token":
			status["token"] = value
		case "tokenfile":
			raw, _ := os.ReadFile(value)
			status["token"] = strings.TrimSpace(string(raw))
		case "certfile":
			raw, _ := os.ReadFile(value)
			status["clientCertificateData"] = string(raw)
		case "keyfile":
			raw, _ := os.ReadFile(value)
			status["clientKeyData"] = string(raw)
		case "expiry":
			s, _ := strconv.Atoi(value)
			status["expirationTimestamp"] = time.Now().Add(time.Duration(s) * time.Second).UTC().Format(time.RFC3339)
		case "version":
			version = value
		case "raw":
			raw, _ := os.ReadFile(value)
			os.Stdout.Write(raw)
			return
		case "bytes":
			k, _ := strconv.Atoi(value)
			chunk := []byte(strings.Repeat("x", 64*1024))
			for k > 0 {
				w := min(k, len(chunk))
				if _, err := os.Stdout.Write(chunk[:w]); err != nil {
					return
				}
				k -= w
			}
			return
		}
	}
	out, _ := json.Marshal(map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": status})
	os.Stdout.Write(out)
}

// appendLine appends line to path and returns the number of lines in the file after it.
func appendLine(path, line string) int {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	}
	raw, _ := os.ReadFile(path)
	return strings.Count(string(raw), "\n")
}

// startChild starts a descendant of a different executable, and waits until it recorded
// its PID.
func startChild(dir string, holdPipe bool) {
	child := os.Getenv("CREDFAKE_CHILD")
	before := countLines(filepath.Join(dir, "descendants"))
	cmd := exec.Command(child, "--credfake-descendant", dir)
	if holdPipe {
		cmd.Stdout = os.Stdout
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "credfake: descendant:", err)
		os.Exit(4)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if countLines(filepath.Join(dir, "descendants")) > before {
			return
		}
	}
}

func countLines(path string) int {
	raw, _ := os.ReadFile(path)
	return strings.Count(string(raw), "\n")
}
