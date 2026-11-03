package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as the fake server: with VERIFYMAP_FAKE_MODE set it serves
// instead of running the tests. Modes:
//   - ok: 200 text/html with the marker at every path
//   - never-ready: prints its address, answers 503 at every path
//   - drive-404: 200 text/html at "/", 404 at every other path
const (
	fakeModeEnv   = "VERIFYMAP_FAKE_MODE"
	fakeCommitEnv = "VERIFYMAP_FAKE_COMMIT"
	fakeMarker    = "fake-embed-marker"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeModeEnv); mode != "" {
		os.Exit(fakeServer(mode))
	}
	os.Exit(m.Run())
}

func fakeServer(mode string) int {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("version=fake\ncommit=%s\n", os.Getenv(fakeCommitEnv))
		return 0
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case mode == "never-ready":
			w.WriteHeader(http.StatusServiceUnavailable)
		case mode == "drive-404" && r.URL.Path != "/":
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<html><body>%s</body></html>", fakeMarker)
		}
	})
	srv := &http.Server{Handler: handler}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	fmt.Printf("listening on http://%s\n", ln.Addr())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return 1
	}
	return 0
}

const fakeCommit = "0123456789abcdef0123456789abcdef01234567"

// fakeOptions runs the test binary as the server under test in the given mode.
func fakeOptions(t *testing.T, mode string) Options {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	sums := filepath.Join(t.TempDir(), "SHA256SUMS")
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), filepath.Base(self))
	if err := os.WriteFile(sums, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{
		Binary:       self,
		Sums:         sums,
		ExpectCommit: fakeCommit,
		EvidenceDir:  filepath.Join(t.TempDir(), "evidence"),
		ReadyTimeout: 2 * time.Second,
		PollInterval: 100 * time.Millisecond,
		ExtraEnv:     []string{fakeModeEnv + "=" + mode, fakeCommitEnv + "=" + fakeCommit},
		Feature: Feature{
			ID: "fake-page",
			Drive: Drive{
				Harness:      "http-get",
				Path:         "/",
				Status:       200,
				ContentType:  "text/html",
				BodyContains: fakeMarker,
			},
		},
	}
}

func runAndLog(t *testing.T, opts Options) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := Run(opts, &out)
	t.Logf("run %s -> exit %d\n%s", opts.Feature.ID, code, out.String())
	return code, out.String()
}

// Positive control: the same harness passes against a correct server.
func TestRunPassesAgainstCorrectServer(t *testing.T) {
	code, out := runAndLog(t, fakeOptions(t, "ok"))
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{"Launch:", "Doctor:", "Drive:", "Evidence:", "Cleanup:", "Second read:", "Compare commit:", "Compare sha256:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	assertOrder(t, out, "Launch:", "Doctor:", "Drive:", "Evidence:", "Cleanup:", "Second read:", "Compare commit:", "Compare sha256:")
}

// Criterion 7: the server never becomes ready.
func TestRunLaunchFailsWhenServerNeverReady(t *testing.T) {
	code, out := runAndLog(t, fakeOptions(t, "never-ready"))
	if code == 0 {
		t.Fatal("exit 0, want non-zero")
	}
	if !hasLineContaining(out, "Launch: FAIL") {
		t.Fatal(`no "Launch: FAIL" line with a reason`)
	}
	if !hasLineContaining(out, "Cleanup:") {
		t.Fatal("Cleanup did not run after the Launch failure")
	}
	assertOrder(t, out, "Launch: FAIL", "Cleanup:")
	if hasLineContaining(out, "Drive:") {
		t.Fatal("Drive ran although Launch failed")
	}
}

// Criterion 7: the Drive expectation does not match (the fake server answers 404).
func TestRunDriveFailsOn404(t *testing.T) {
	opts := fakeOptions(t, "drive-404")
	opts.Feature.Drive.Path = "/page"
	code, out := runAndLog(t, opts)
	if code == 0 {
		t.Fatal("exit 0, want non-zero")
	}
	if !hasLineContaining(out, "Drive: FAIL") {
		t.Fatal(`no "Drive: FAIL" line`)
	}
	if !hasLineContaining(out, "Cleanup:") {
		t.Fatal("Cleanup did not run after the Drive failure")
	}
}

// Criterion 7: an evidence file is missing after cleanup.
func TestRunFailsWhenEvidenceMissingAfterCleanup(t *testing.T) {
	opts := fakeOptions(t, "ok")
	opts.afterCleanup = func(evidence []string) {
		if len(evidence) == 0 {
			t.Error("no evidence file was written")
			return
		}
		if err := os.Remove(evidence[0]); err != nil {
			t.Error(err)
		}
	}
	code, out := runAndLog(t, opts)
	if code == 0 {
		t.Fatal("exit 0, want non-zero")
	}
	if !hasLineContaining(out, "Second read: FAIL") {
		t.Fatal(`no "Second read: FAIL" line`)
	}
}

func TestRunFailsOnCommitMismatch(t *testing.T) {
	opts := fakeOptions(t, "ok")
	opts.ExpectCommit = strings.Repeat("f", 40)
	code, out := runAndLog(t, opts)
	if code == 0 {
		t.Fatal("exit 0, want non-zero")
	}
	if !hasLineContaining(out, "Compare commit: FAIL") {
		t.Fatal(`no "Compare commit: FAIL" line`)
	}
}

func TestRunFailsOnBinarySHA256Mismatch(t *testing.T) {
	opts := fakeOptions(t, "ok")
	self := filepath.Base(opts.Binary)
	line := fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), self)
	if err := os.WriteFile(opts.Sums, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runAndLog(t, opts)
	if code == 0 {
		t.Fatal("exit 0, want non-zero")
	}
	if !hasLineContaining(out, "Compare sha256: FAIL") {
		t.Fatal(`no "Compare sha256: FAIL" line`)
	}
}

func assertOrder(t *testing.T, out string, marks ...string) {
	t.Helper()
	pos := -1
	for _, m := range marks {
		i := strings.Index(out, m)
		if i < 0 {
			t.Fatalf("output lacks %q", m)
		}
		if i < pos {
			t.Fatalf("%q appears out of order", m)
		}
		pos = i
	}
}
