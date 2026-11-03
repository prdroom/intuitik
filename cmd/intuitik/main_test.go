package main

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	testVersion = "1.2.3-test"
	testCommit  = "0123456789abcdef0123456789abcdef01234567"
	embedMarker = "intuitik-embedded-page"
)

// intuitikBinary is built once by TestMain with the version values injected
// through -ldflags, the same way the build job injects them.
var intuitikBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "intuitik-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "intuitik"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	intuitikBinary = filepath.Join(dir, name)
	ldflags := fmt.Sprintf("-X main.version=%s -X main.commit=%s", testVersion, testCommit)
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", intuitikBinary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building intuitik:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestVersionPrintsInjectedValues(t *testing.T) {
	out, err := exec.Command(intuitikBinary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "version=" + testVersion + "\ncommit=" + testCommit + "\n"
	if string(out) != want {
		t.Fatalf("--version printed %q, want %q", out, want)
	}
}

var listenLine = regexp.MustCompile(`^listening on (http://127\.0\.0\.1:[0-9]+)$`)

// Criterion 4: run the binary, send GET / to the address it prints.
func TestServesEmbeddedPage(t *testing.T) {
	cmd := exec.Command(intuitikBinary)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
		io.Copy(io.Discard, stdout)
	}()
	var first string
	select {
	case first = <-lines:
	case <-time.After(10 * time.Second):
		t.Fatal("no stdout line within 10 s")
	}
	m := listenLine.FindStringSubmatch(first)
	if m == nil {
		t.Fatalf("first stdout line %q is not the listen address line", first)
	}
	addr, err := url.Parse(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if addr.Hostname() != "127.0.0.1" {
		t.Fatalf("listens on %q, want 127.0.0.1 only", addr.Hostname())
	}

	resp, err := http.Get(m[1] + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status %d, want 200", resp.StatusCode)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "text/html" {
		t.Fatalf("GET / Content-Type %q, want text/html", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), embedMarker) {
		t.Fatalf("GET / body lacks the embed marker %q", embedMarker)
	}
}
