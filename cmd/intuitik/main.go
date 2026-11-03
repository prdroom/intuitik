// Command intuitik serves the embedded web page.
//
//	intuitik            start the server on 127.0.0.1 and print its listen address
//	intuitik --version  print version= and commit= lines
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
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
	// The T1 server binds to the loopback address only.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(stderr, "intuitik:", err)
		return 1
	}
	srv := &http.Server{
		Handler:           http.FileServer(http.FS(page)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	fmt.Fprintf(stdout, "listening on http://%s\n", ln.Addr())
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, "intuitik:", err)
		return 1
	}
	return 0
}
