package readonly

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/discovery/cached/disk"
	"k8s.io/klog/v2"
)

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// TestR8_CanarySearchPositiveControl (packet M1-T4 R8): the canary search finds
// deliberately leaked canaries (the raw value inside a large blob, its base64 at each
// alignment inside a large base64 blob, a Helm release Secret value, a gzip-compressed
// response) and finds 0 on the normal path, the converted Secret.
func TestR8_CanarySearchPositiveControl(t *testing.T) {
	canary := randomValue(t, "r8canary")
	filler := bytes.Repeat([]byte("intuitik-filler-"), 4096)
	helmRelease := mustJSON(t, map[string]any{"name": "rel", "manifest": "apiVersion: v1\nkind: Secret\nstringData:\n  password: " + canary + "\n"})
	helmValue := base64.StdEncoding.EncodeToString(gzipBytes(t, helmRelease)) // Helm stores base64(gzip(release))
	leaked := map[string][]byte{
		"raw value in a large blob": append(append(append([]byte{}, filler...), canary...), filler...),
		"gzip-compressed response":  gzipBytes(t, mustJSON(t, map[string]any{"data": map[string]any{"k": canary}})),
		"Helm release Secret value": mustJSON(t, map[string]any{"kind": "Secret", "apiVersion": "v1", "data": map[string]any{"release": base64.StdEncoding.EncodeToString([]byte(helmValue))}}),
	}
	for shift := 0; shift < 3; shift++ {
		blob := append(append(append([]byte{}, filler[:1000+shift]...), canary...), filler[:999]...)
		leaked[fmt.Sprintf("base64 of a large blob, alignment %d", shift)] = []byte(base64.StdEncoding.EncodeToString(blob))
	}
	names := make([]string, 0, len(leaked))
	for n := range leaked {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := r8Count(leaked[n], canary)
		if c < 1 {
			t.Errorf("R8 leaked canary fixture %q: %d findings, expected at least 1", n, c)
		}
		t.Logf("R8 leaked canary fixture %q: findings=%d", n, c)
	}

	// The normal path: a Secret holding the canary after the conversion.
	secret := mustJSON(t, map[string]any{"kind": "List", "apiVersion": "v1", "items": []any{secretObject("a", "s", canary, []string{"k1", "k2"}, []string{"k3"})}})
	before := r8Count(secret, canary)
	out, err := convertDocument(secret, false)
	if err != nil {
		t.Fatal(err)
	}
	after := r8Count(out, canary)
	if before < 1 || after != 0 {
		t.Errorf("R8 normal path: findings before conversion=%d (expected at least 1), after=%d (expected 0)", before, after)
	}
	t.Logf("R8 normal path: findings before conversion=%d after conversion=%d", before, after)
}

// fileState is the size and sha256 of every file under the watched roots.
type fileState map[string]string

func snapshot(t *testing.T, roots ...string) fileState {
	t.Helper()
	s := fileState{}
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				s[p] = "unreadable: " + err.Error()
				return nil
			}
			sum := sha256.Sum256(b)
			s[p] = fmt.Sprintf("size=%d sha256=%s", len(b), hex.EncodeToString(sum[:]))
			return nil
		})
	}
	return s
}

// diffStates lists every file added, removed or changed between two snapshots.
func diffStates(before, after fileState) []string {
	var d []string
	for p, v := range before {
		w, ok := after[p]
		switch {
		case !ok:
			d = append(d, "removed: "+p)
		case w != v:
			d = append(d, "sha256 or size changed: "+p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			d = append(d, "added: "+p)
		}
	}
	sort.Strings(d)
	return d
}

// testHome points HOME and the user cache directory at a new directory, seeds the
// two cache locations with existing files and returns the seeded discovery cache
// directory of server host and the roots to watch.
func testHome(t *testing.T, host string) (discoveryDir string, roots []string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", filepath.Join(home, "AppData", "Local"))
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	kubeCache := filepath.Join(home, ".kube", "cache")
	discoveryDir = filepath.Join(kubeCache, "discovery", strings.ReplaceAll(host, ":", "_"))
	old := time.Now().Add(-48 * time.Hour)
	for p, content := range map[string]string{
		filepath.Join(discoveryDir, "servergroups.json"):            `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"stale.r9.example","versions":[]}]}`,
		filepath.Join(discoveryDir, "v1", "serverresources.json"):   `{"kind":"APIResourceList","groupVersion":"v1","resources":[]}`,
		filepath.Join(kubeCache, "http", "seeded"):                  "seeded http cache entry",
		filepath.Join(userCache, "intuitik-r9-seed", "seeded.json"): "{}",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, old, old)
	}
	return discoveryDir, []string{kubeCache, userCache}
}

// TestR9_NoDiskCache (packet M1-T4 R9): a run of readonly (connection attempt,
// discovery, list, get, watch) leaves ~/.kube/cache and os.UserCacheDir() as they
// were: the same files with the same size and sha256. Negative fixture: the client-go
// disk discovery cache, built on the same guarded configuration, rewrites the seeded
// servergroups.json, and the comparison reports its changed sha256.
func TestR9_NoDiskCache(t *testing.T) {
	ctx := context.Background()
	srv := newTLSFakeAPI(t, false)
	srv.add("pods", podObject("a", "p"))
	host := strings.TrimPrefix(srv.srv.URL, "https://")
	discoveryDir, roots := testHome(t, host)
	useKubeconfig(t, kubeconfig(srv.srv.URL, srv.caPEM(), map[string]any{"token": "token-r9"}))
	before := snapshot(t, roots...)

	o := Attempt(ctx, "")
	if o.State != StateConnected {
		t.Fatalf("R9 attempt: state %s %s", o.State, o.Message)
	}
	c, err := NewClient(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resources(); err != nil {
		t.Errorf("R9 discovery: %v", err)
	}
	if _, err := c.List(ctx, Resource{Version: "v1", Resource: "pods"}, "a"); err != nil {
		t.Errorf("R9 list: %v", err)
	}
	if _, err := c.Object(ctx, Resource{Version: "v1", Resource: "pods"}, "a", "p"); err != nil {
		t.Errorf("R9 get: %v", err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if w, err := c.Watch(wctx, Resource{Version: "v1", Resource: "pods"}, "a"); err == nil {
		<-w.ResultChan()
		w.Stop()
	}
	cancel()
	after := snapshot(t, roots...)
	d := diffStates(before, after)
	if len(d) != 0 {
		t.Errorf("R9 readonly run: the cache locations changed: %v", d)
	}
	t.Logf("R9 readonly run: %d files watched before, %d after, differences=%d", len(before), len(after), len(d))

	// Negative fixture: the client-go disk discovery cache turned on.
	cached, err := disk.NewCachedDiscoveryClientForConfig(o.config, discoveryDir, filepath.Join(filepath.Dir(filepath.Dir(discoveryDir)), "http"), 0)
	if err != nil {
		t.Fatalf("R9 negative fixture: %v", err)
	}
	if _, _, err := cached.ServerGroupsAndResources(); err != nil {
		t.Fatalf("R9 negative fixture discovery: %v", err)
	}
	seeded := filepath.Join(discoveryDir, "servergroups.json")
	neg := diffStates(after, snapshot(t, roots...))
	caught := false
	for _, line := range neg {
		caught = caught || line == "sha256 or size changed: "+seeded
	}
	if !caught {
		t.Errorf("R9 negative fixture: the disk cache did not change the seeded %s; differences %v", seeded, neg)
	}
	t.Logf("R9 negative fixture (client-go disk discovery cache): seeded servergroups.json changed=%v; differences=%v", caught, neg)
}

// stderrCapture replaces os.Stderr with a pipe until stop returns what was written.
func stderrCapture(t *testing.T) (stop func() []byte) {
	t.Helper()
	return fileCapture(t, &os.Stderr)
}

// fileCapture replaces *f (os.Stdout or os.Stderr) with a pipe until stop returns what
// was written.
func fileCapture(t *testing.T, f **os.File) (stop func() []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := *f
	*f = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	return func() []byte {
		*f = orig
		w.Close()
		return <-done
	}
}

// lockedBuffer is the klog output of R10; klog and the client's goroutines write to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// r10Verbosity is the klog verbosity of the R10 run: 9 is the highest level of the
// client library's request logging (URL and timing from 6, request and response headers
// from 7 and 8, the request as a curl command at 9).
const r10Verbosity = "9"

// raiseKlogVerbosity sets the klog verbosity to r10Verbosity and sends klog's output to
// out instead of the log files and stderr. t.Cleanup puts the verbosity, the flags and
// the output target back. The test that calls it must not run in parallel with other
// tests: klog's settings are global.
func raiseKlogVerbosity(t *testing.T, out io.Writer) {
	t.Helper()
	fs := flag.NewFlagSet("klog-r10", flag.ContinueOnError)
	klog.InitFlags(fs)
	set := map[string]string{"v": r10Verbosity, "logtostderr": "false", "alsologtostderr": "false"}
	saved := map[string]string{}
	for name, value := range set {
		f := fs.Lookup(name)
		if f == nil {
			t.Fatalf("R10: klog has no flag %s", name)
		}
		saved[name] = f.Value.String()
		if err := fs.Set(name, value); err != nil {
			t.Fatalf("R10: klog flag %s=%s: %v", name, value, err)
		}
	}
	klog.SetOutput(out)
	t.Cleanup(func() {
		klog.Flush()
		for name, value := range saved {
			if err := fs.Set(name, value); err != nil {
				t.Errorf("R10: restoring klog flag %s=%s: %v", name, value, err)
			}
		}
		klog.SetOutput(os.Stderr)
		t.Logf("R10: klog restored: v=%s logtostderr=%s alsologtostderr=%s, output os.Stderr; V(%s) enabled=%v",
			fs.Lookup("v").Value.String(), fs.Lookup("logtostderr").Value.String(), fs.Lookup("alsologtostderr").Value.String(), r10Verbosity, klog.V(9).Enabled())
	})
}

// TestR10_KubeconfigCredsNotLogged (packet M1-T4 R10): with a kubeconfig that carries
// a token canary and the client library's request logging raised to klog verbosity 9
// for the whole run, a run of readonly (an accepted and a rejected connection attempt,
// list, get, refused requests) writes the canary to no klog output, stdout, stderr,
// outcome text, error or response. Positive controls: the klog output holds the verbose
// lines of the run's requests, and the kubeconfig file and the Authorization header the
// fake server received hold the canary. This test does not run in parallel: it changes
// klog's global settings, which t.Cleanup restores.
func TestR10_KubeconfigCredsNotLogged(t *testing.T) {
	ctx := context.Background()
	token := randomValue(t, "r10token")
	srv := newTLSFakeAPI(t, false)
	srv.add("pods", podObject("a", "p"))
	path := useKubeconfig(t, kubeconfig(srv.srv.URL, srv.caPEM(), map[string]any{"token": token}))
	rejecting := newTLSFakeAPI(t, false)
	rejecting.srv.Config.Handler = rejectAll{}

	klogOut := &lockedBuffer{}
	raiseKlogVerbosity(t, klogOut)
	atStart := klog.V(9).Enabled()
	stopErr := stderrCapture(t)
	stopOut := fileCapture(t, &os.Stdout)
	var collected []byte
	o := Attempt(ctx, "")
	collected = append(collected, o.Message...)
	if c, err := NewClient(o); err == nil {
		l, err := c.List(ctx, Resource{Version: "v1", Resource: "pods"}, "a")
		collected = append(collected, mustMarshal(l)...)
		collected = append(collected, fmt.Sprint(err)...)
		g, err := c.Object(ctx, Resource{Version: "v1", Resource: "pods"}, "a", "p")
		collected = append(collected, mustMarshal(g)...)
		collected = append(collected, fmt.Sprint(err)...)
		_, err = c.Get(ctx, "/api/v1/namespaces/a/pods/p/exec")
		collected = append(collected, fmt.Sprint(err)...)
	} else {
		collected = append(collected, err.Error()...)
	}
	// A rejected attempt: the server answers 401.
	useKubeconfig(t, kubeconfig(rejecting.srv.URL, rejecting.caPEM(), map[string]any{"token": token}))
	o2 := Attempt(ctx, "")
	collected = append(collected, o2.Message...)
	klog.Flush()
	atEnd := klog.V(9).Enabled()
	outOut := stopOut()
	errOut := stopErr()
	logged := klogOut.Bytes()

	if o.State != StateConnected || o2.State != StateCredentialsRejected {
		t.Errorf("R10 attempts: states %s and %s, expected connected and credentials-rejected", o.State, o2.State)
	}
	if !atStart || !atEnd {
		t.Errorf("R10: klog V(%s) enabled at the start=%v and at the end=%v of the run, expected true and true", r10Verbosity, atStart, atEnd)
	}
	t.Logf("R10: klog verbosity v=%s for the whole run: V(%s) enabled at the start=%v, at the end=%v", r10Verbosity, r10Verbosity, atStart, atEnd)

	// Positive control of the raised verbosity: lines that the client library logged for
	// the requests of this run.
	var requestLines, listLines, rejectedLines, headerLines int
	for _, line := range strings.Split(string(logged), "\n") {
		if strings.Contains(line, srv.srv.URL) || strings.Contains(line, rejecting.srv.URL) {
			requestLines++
		}
		if strings.Contains(line, srv.srv.URL+"/api/v1/namespaces/a/pods") {
			listLines++
		}
		if strings.Contains(line, rejecting.srv.URL+"/api") {
			rejectedLines++
		}
		if strings.Contains(strings.ToLower(line), "authorization") {
			headerLines++
		}
	}
	if listLines < 1 || rejectedLines < 1 {
		t.Errorf("R10 verbosity positive control: klog lines naming the list or get request URL=%d and the rejected attempt's URL=%d, expected at least 1 each", listLines, rejectedLines)
	}
	t.Logf("R10 verbosity positive control: klog output bytes=%d, lines naming a fake server URL=%d (list and get requests %d, rejected attempt %d), lines naming the Authorization header=%d",
		len(logged), requestLines, listLines, rejectedLines, headerLines)

	kc, _ := os.ReadFile(path)
	_, _, auth := srv.seen()
	positive := canaryCount(kc, token) + canaryCount([]byte(strings.Join(auth, "\n")), token)
	if positive < 2 {
		t.Errorf("R10 positive control: %d findings in the kubeconfig and the received Authorization headers, expected at least 2", positive)
	}
	inKlog, inStdout, inStderr, inOutput := canaryCount(logged, token), canaryCount(outOut, token), canaryCount(errOut, token), canaryCount(collected, token)
	if inKlog != 0 || inStdout != 0 || inStderr != 0 || inOutput != 0 {
		t.Errorf("R10: token canary found %d times in the klog output, %d on stdout, %d on stderr and %d in texts, errors and responses, expected 0",
			inKlog, inStdout, inStderr, inOutput)
	}
	t.Logf("R10: klog output bytes=%d findings=%d; stdout bytes=%d findings=%d; stderr bytes=%d findings=%d; texts, errors and responses bytes=%d findings=%d; positive control findings=%d",
		len(logged), inKlog, len(outOut), inStdout, len(errOut), inStderr, len(collected), inOutput, positive)
}

type rejectAll struct{}

func (rejectAll) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","code":401}`))
}

// r8Count is the canary search under test in R8: the T2 searcher with the decoding
// layers of canaryCount.
func r8Count(data []byte, canary string) int {
	return canaryCount(data, canary)
}
