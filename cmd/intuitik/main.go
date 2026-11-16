// Command intuitik serves the embedded web page on 127.0.0.1 behind a one-time
// login (internal/server).
//
//	intuitik            start the server on a random 127.0.0.1 port, print its listen
//	                    address and open the login address in the browser (BROWSER)
//	intuitik --version  print version= and commit= lines
//
// The first argument __credexec-run is the helper entry of credential programs on
// Windows (internal/kube/credexec); its exact grammar is checked there.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/prdroom/intuitik/internal/kube/credexec"
	"github.com/prdroom/intuitik/internal/server"
)

// Injected only through -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "unknown"
	commit  = "unknown"
)

// dist is the frontend build output (web/, `npm run build`).
//
//go:embed all:dist
var dist embed.FS

func main() {
	discardLibraryLogs()
	// The helper entry branches before the normal flag handling, and only as the first
	// argument (E12). RED-STUB E12: only on Windows; elsewhere the arguments reach the
	// normal flag handling.
	if runtime.GOOS == "windows" && len(os.Args) > 1 && os.Args[1] == credexec.EntryName {
		os.Exit(credexec.HelperMain(os.Args[2:], os.Stdout))
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// discardLibraryLogs makes klog, the log of the client library, write nothing to stderr,
// stdout or a file (E13).
func discardLibraryLogs() {
	// RED-STUB E13: klog is not configured.
	_ = klog.SetOutput
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("intuitik", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print version and commit, then exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "intuitik: unexpected arguments %q\n", flags.Args())
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "version=%s\ncommit=%s\n", version, commit)
		return 0
	}

	page, err := fs.Sub(dist, "dist")
	if err != nil {
		fmt.Fprintln(stderr, "intuitik:", err)
		return 1
	}
	seams, err := testSeams()
	if err != nil {
		fmt.Fprintln(stderr, "intuitik:", err)
		return 1
	}
	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopSignal)
	return server.Run(server.Options{
		Assets:        page,
		Stdin:         os.Stdin,
		StdinTerminal: server.IsTerminal(os.Stdin),
		Stdout:        stdout,
		Stderr:        stderr,
		Browser:       os.Getenv("BROWSER"),
		Stop:          stopSignal,
		Seams:         seams,
	})
}
