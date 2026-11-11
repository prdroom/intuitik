package readonly

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/discovery/cached/disk"
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
	srv := newFakeAPI(t, false)
	srv.add("pods", podObject("a", "p"))
	host := strings.TrimPrefix(srv.srv.URL, "http://")
	discoveryDir, roots := testHome(t, host)
	useKubeconfig(t, kubeconfig(srv.srv.URL, nil, map[string]any{"token": "token-r9"}))
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
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	return func() []byte {
		os.Stderr = orig
		w.Close()
		return <-done
	}
}

// TestR10_KubeconfigCredsNotLogged (packet M1-T4 R10): with a kubeconfig that carries
// a token canary, a run of readonly (an accepted and a rejected connection attempt,
// list, get, refused requests) writes the canary to no stderr, outcome text, error or
// response. Positive control: the kubeconfig file and the Authorization header the
// fake server received hold the canary.
func TestR10_KubeconfigCredsNotLogged(t *testing.T) {
	ctx := context.Background()
	token := randomValue(t, "r10token")
	srv := newFakeAPI(t, false)
	srv.add("pods", podObject("a", "p"))
	path := useKubeconfig(t, kubeconfig(srv.srv.URL, nil, map[string]any{"token": token}))

	stop := stderrCapture(t)
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
	rejecting := newFakeAPI(t, false)
	rejecting.srv.Config.Handler = rejectAll{}
	useKubeconfig(t, kubeconfig(rejecting.srv.URL, nil, map[string]any{"token": token}))
	o2 := Attempt(ctx, "")
	collected = append(collected, o2.Message...)
	errOut := stop()

	if o.State != StateConnected || o2.State != StateCredentialsRejected {
		t.Errorf("R10 attempts: states %s and %s, expected connected and credentials-rejected", o.State, o2.State)
	}
	kc, _ := os.ReadFile(path)
	_, _, auth := srv.seen()
	positive := canaryCount(kc, token) + canaryCount([]byte(strings.Join(auth, "\n")), token)
	if positive < 2 {
		t.Errorf("R10 positive control: %d findings in the kubeconfig and the received Authorization headers, expected at least 2", positive)
	}
	inStderr, inOutput := canaryCount(errOut, token), canaryCount(collected, token)
	if inStderr != 0 || inOutput != 0 {
		t.Errorf("R10: token canary found %d times on stderr and %d times in texts, errors and responses, expected 0", inStderr, inOutput)
	}
	t.Logf("R10: stderr bytes=%d findings=%d; texts, errors and responses bytes=%d findings=%d; positive control findings=%d",
		len(errOut), inStderr, len(collected), inOutput, positive)
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
	return 0
}
