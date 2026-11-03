package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options drive one end-to-end run of a feature (verify/README.md).
type Options struct {
	Binary       string // the executable under test
	Sums         string // SHA256SUMS written by the build step
	ExpectCommit string // `git rev-parse HEAD` of the checkout used for the build
	EvidenceDir  string // evidence files are written here and kept
	Feature      Feature
	ReadyTimeout time.Duration
	PollInterval time.Duration
	ExtraEnv     []string // added to the server environment (tests use it for the fake server)

	afterCleanup func(evidence []string) // test hook, runs right after Cleanup
}

var (
	listenLine = regexp.MustCompile(`^listening on (http://127\.0\.0\.1:[0-9]+)$`)
	fullSHA    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Variables of the parent environment that could carry state into the run; they are
// removed so the server starts from a new empty HOME and no KUBECONFIG.
var stateEnv = []string{"HOME", "KUBECONFIG", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"}

// Run executes Launch, Doctor, Drive, Evidence, Cleanup and Second read for one
// feature, then the two exact comparisons. Cleanup runs whenever Launch created
// anything. It returns 0 when every step and comparison passed, 1 otherwise.
func Run(o Options, out io.Writer) int {
	r := &run{o: o, out: out}
	r.execute()
	if r.failed {
		fmt.Fprintf(out, "Run: FAIL feature=%s\n", o.Feature.ID)
		return 1
	}
	fmt.Fprintf(out, "Run: PASS feature=%s\n", o.Feature.ID)
	return 0
}

type run struct {
	o      Options
	out    io.Writer
	failed bool

	home        string
	cmd         *exec.Cmd
	exited      chan *os.ProcessState
	stderr      lockedBuffer
	addr        string // http://127.0.0.1:<port>
	commit      string
	driveRecord string
	evidence    []evidenceFile
}

type evidenceFile struct {
	path string
	sum  string
}

func (r *run) printf(format string, args ...any) { fmt.Fprintf(r.out, format+"\n", args...) }

func (r *run) fail(step, format string, args ...any) {
	r.failed = true
	r.printf("%s: FAIL %s", step, fmt.Sprintf(format, args...))
}

func (r *run) execute() {
	r.printf("Run: feature=%s drive=%s binary=%s", r.o.Feature.ID, r.o.Feature.Drive.Harness, r.o.Binary)
	launched := r.launch()
	if launched {
		r.doctor()
		r.drive()
		r.writeEvidence()
	}
	r.cleanup()
	if !launched {
		return
	}
	if r.o.afterCleanup != nil {
		paths := make([]string, len(r.evidence))
		for i, e := range r.evidence {
			paths[i] = e.path
		}
		r.o.afterCleanup(paths)
	}
	r.secondRead()
	r.compareCommit()
	r.compareSHA256()
}

// launch starts the binary from a new empty HOME and waits until it prints its listen
// address on one stdout line and that address answers HTTP 200.
func (r *run) launch() bool {
	start := time.Now()
	deadline := start.Add(r.o.ReadyTimeout)
	home, err := os.MkdirTemp("", "verifymap-home-")
	if err != nil {
		r.fail("Launch", "creating HOME: %v", err)
		return false
	}
	r.home = home
	r.printf("Launch: new empty HOME %s; KUBECONFIG unset", home)

	r.cmd = exec.Command(r.o.Binary)
	r.cmd.Env = serverEnv(home, r.o.ExtraEnv)
	r.cmd.Stderr = &r.stderr
	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		r.fail("Launch", "%v", err)
		r.cmd = nil
		return false
	}
	if err := r.cmd.Start(); err != nil {
		r.fail("Launch", "starting %s: %v", r.o.Binary, err)
		r.cmd = nil
		return false
	}
	r.printf("Launch: started pid=%d", r.cmd.Process.Pid)

	addrs := make(chan string, 1)
	r.exited = make(chan *os.ProcessState, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sent := false
		for sc.Scan() {
			if m := listenLine.FindStringSubmatch(sc.Text()); m != nil && !sent {
				addrs <- m[1]
				sent = true
			}
		}
		io.Copy(io.Discard, stdout)
		r.cmd.Wait()
		r.exited <- r.cmd.ProcessState
	}()

	select {
	case r.addr = <-addrs:
		r.printf("Launch: listen address line %q", "listening on "+r.addr)
	case st := <-r.exited:
		r.exited <- st
		r.fail("Launch", "process exited (%v) before printing its listen address", st)
		return false
	case <-time.After(time.Until(deadline)):
		r.fail("Launch", "no listen address line within %s", r.o.ReadyTimeout)
		return false
	}

	client := &http.Client{}
	lastStatus := "no response"
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			r.fail("Launch", "%s did not answer HTTP 200 within %s (last: %s)", r.addr, r.o.ReadyTimeout, lastStatus)
			return false
		}
		client.Timeout = min(remaining, time.Second)
		resp, err := client.Get(r.addr + "/")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastStatus = fmt.Sprintf("status %d", resp.StatusCode)
			if resp.StatusCode == http.StatusOK {
				r.printf("Launch: ready, %s answered 200 after %d ms (poll %s, limit %s)",
					r.addr, time.Since(start).Milliseconds(), r.o.PollInterval, r.o.ReadyTimeout)
				return true
			}
		} else {
			lastStatus = err.Error()
		}
		time.Sleep(min(r.o.PollInterval, max(time.Until(deadline), 0)))
	}
}

func serverEnv(home string, extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, s := range stateEnv {
			if strings.EqualFold(name, s) {
				drop = true
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home)
	return append(env, extra...)
}

// doctor reads, without changing anything: the commit field of --version, the PID,
// the listen address and the response status.
func (r *run) doctor() {
	cmd := exec.Command(r.o.Binary, "--version")
	cmd.Env = r.cmd.Env
	raw, err := cmd.Output()
	if err != nil {
		r.fail("Doctor", "%s --version: %v", r.o.Binary, err)
		return
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if v, ok := strings.CutPrefix(line, "commit="); ok {
			r.commit = v
		}
	}
	status := "error"
	resp, err := http.Get(r.addr + "/")
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		status = fmt.Sprint(resp.StatusCode)
	}
	r.printf("Doctor: commit=%s pid=%d addr=%s status=%s", r.commit, r.cmd.Process.Pid, r.addr, status)
	if r.commit == "" {
		r.fail("Doctor", "--version printed no commit= line")
	}
}

// drive sends the feature's request and checks status, media type and body.
func (r *run) drive() {
	d := r.o.Feature.Drive
	target := r.addr + d.Path
	r.printf("Drive: GET %s", target)
	resp, err := http.Get(target)
	if err != nil {
		r.fail("Drive", "GET %s: %v", target, err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		r.fail("Drive", "reading the body: %v", err)
		return
	}
	r.recordDrive(target, resp, body)

	if resp.StatusCode == d.Status {
		r.printf("Drive: status=%d expected=%d ok", resp.StatusCode, d.Status)
	} else {
		r.fail("Drive", "status=%d expected=%d", resp.StatusCode, d.Status)
	}
	contentType := resp.Header.Get("Content-Type")
	media, _, err := mime.ParseMediaType(contentType)
	if err == nil && media == d.ContentType {
		r.printf("Drive: content-type=%q media=%s expected=%s ok", contentType, media, d.ContentType)
	} else {
		r.fail("Drive", "content-type=%q expected media type %s", contentType, d.ContentType)
	}
	if bytes.Contains(body, []byte(d.BodyContains)) {
		r.printf("Drive: body contains %q ok (body %d bytes)", d.BodyContains, len(body))
	} else {
		r.fail("Drive", "body (%d bytes) lacks %q", len(body), d.BodyContains)
	}
}

// recordDrive keeps the request and response for the Evidence step.
func (r *run) recordDrive(target string, resp *http.Response, body []byte) {
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s\n%s %s\n", target, resp.Proto, resp.Status)
	resp.Header.Write(&b)
	b.WriteString("\n")
	b.Write(body)
	r.driveRecord = b.String()
}

func (r *run) writeEvidence() {
	if err := os.MkdirAll(r.o.EvidenceDir, 0o755); err != nil {
		r.fail("Evidence", "%v", err)
		return
	}
	files := []struct{ name, content string }{
		{r.o.Feature.ID + "-doctor.txt", fmt.Sprintf("commit=%s\npid=%d\naddr=%s\n", r.commit, r.cmd.Process.Pid, r.addr)},
	}
	if r.driveRecord != "" {
		files = append(files, struct{ name, content string }{r.o.Feature.ID + "-drive.txt", r.driveRecord})
	}
	for _, f := range files {
		path := filepath.Join(r.o.EvidenceDir, f.name)
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			r.fail("Evidence", "writing %s: %v", path, err)
			continue
		}
		sum := sha256.Sum256([]byte(f.content))
		e := evidenceFile{path: path, sum: hex.EncodeToString(sum[:])}
		r.evidence = append(r.evidence, e)
		r.printf("Evidence: wrote %s sha256=%s bytes=%d", e.path, e.sum, len(f.content))
	}
}

// cleanup stops the process, checks that the port is closed and deletes HOME.
// Evidence files are kept.
func (r *run) cleanup() {
	if r.cmd != nil {
		r.stopProcess()
	}
	if r.addr != "" {
		u, err := url.Parse(r.addr)
		if err == nil {
			conn, err := net.DialTimeout("tcp", u.Host, time.Second)
			if err == nil {
				conn.Close()
				r.fail("Cleanup", "port %s still accepts connections", u.Host)
			} else {
				r.printf("Cleanup: port %s closed (dial: %v)", u.Host, err)
			}
		}
	}
	if r.home != "" {
		if err := os.RemoveAll(r.home); err != nil {
			r.fail("Cleanup", "deleting HOME %s: %v", r.home, err)
		} else if _, err := os.Stat(r.home); !os.IsNotExist(err) {
			r.fail("Cleanup", "HOME %s still exists", r.home)
		} else {
			r.printf("Cleanup: HOME %s deleted", r.home)
		}
	}
	if len(r.evidence) > 0 {
		r.printf("Cleanup: evidence kept in %s", r.o.EvidenceDir)
	}
	if s := strings.TrimSpace(r.stderr.String()); s != "" {
		for _, line := range strings.Split(s, "\n") {
			r.printf("Cleanup: server stderr: %s", line)
		}
	}
}

// stopProcess asks the server to stop with SIGTERM and waits up to 10 s. Windows has
// no SIGTERM, so there the process is killed and its exit code is reported only.
func (r *run) stopProcess() {
	graceful := runtime.GOOS != "windows"
	if graceful {
		if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			r.printf("Cleanup: SIGTERM: %v", err)
		}
	} else {
		r.cmd.Process.Kill()
	}
	var st *os.ProcessState
	select {
	case st = <-r.exited:
	case <-time.After(10 * time.Second):
		r.cmd.Process.Kill()
		st = <-r.exited
		r.fail("Cleanup", "process did not exit within 10 s of SIGTERM; killed (exit code %d)", st.ExitCode())
		return
	}
	if graceful && st.ExitCode() != 0 {
		r.fail("Cleanup", "process exit code %d after SIGTERM, want 0", st.ExitCode())
		return
	}
	r.printf("Cleanup: process exit code %d", st.ExitCode())
}

// secondRead reads every evidence file again and compares its sha256 with the value
// recorded at write time.
func (r *run) secondRead() {
	if len(r.evidence) == 0 {
		r.fail("Second read", "no evidence file was written")
		return
	}
	for _, e := range r.evidence {
		raw, err := os.ReadFile(e.path)
		if err != nil {
			r.fail("Second read", "%s: %v", e.path, err)
			continue
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != e.sum {
			r.fail("Second read", "%s sha256=%s, recorded %s", e.path, got, e.sum)
			continue
		}
		r.printf("Second read: %s sha256=%s equal to the recorded value", e.path, got)
	}
}

// compareCommit compares the --version commit with the checkout commit: all 40 hex
// characters, exactly.
func (r *run) compareCommit() {
	equal := fullSHA.MatchString(r.commit) && r.commit == r.o.ExpectCommit
	line := fmt.Sprintf("version commit=%s checkout commit=%s equal=%t", r.commit, r.o.ExpectCommit, equal)
	if !equal {
		r.fail("Compare commit", "%s", line)
		return
	}
	r.printf("Compare commit: %s", line)
}

// compareSHA256 compares the sha256 of the executed binary with its line in the
// build's SHA256SUMS.
func (r *run) compareSHA256() {
	raw, err := os.ReadFile(r.o.Binary)
	if err != nil {
		r.fail("Compare sha256", "reading %s: %v", r.o.Binary, err)
		return
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	name := filepath.Base(r.o.Binary)
	want, line, err := sumsLine(r.o.Sums, name)
	if err != nil {
		r.fail("Compare sha256", "executed binary sha256=%s; %v", got, err)
		return
	}
	text := fmt.Sprintf("executed binary sha256=%s build line %q equal=%t", got, line, got == want)
	if got != want {
		r.fail("Compare sha256", "%s", text)
		return
	}
	r.printf("Compare sha256: %s", text)
}

// sumsLine finds the `<sha256>  <name>` line for name in a sha256sum output file.
func sumsLine(path, name string) (string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], line, nil
		}
	}
	return "", "", fmt.Errorf("%s has no line for %s", path, name)
}

// lockedBuffer collects the server's stderr, written from the process copier.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
