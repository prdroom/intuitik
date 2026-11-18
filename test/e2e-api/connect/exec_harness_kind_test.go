//go:build kind

package connect

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Harness of the credential program tests (packet M1-T3b): the fake program, a TLS fake
// API server that answers by path and records the method, path and credential of each
// request, and the read after connected: one call of the product's graph route (packet
// FA2-T5 D.4, G8).

// readNamespace is the namespace of every graph call of these tests, readPath its route
// and callK the number of requests of one call on the empty fake cluster: /apis and the
// seven LISTs (D.3). It is the expected value of this harness, not a measurement.
const (
	readNamespace = "default"
	readPath      = "/api/graph/containment?namespace=" + readNamespace
	callK         = 8
)

// callPaths is the request sequence of one graph call on the fake server (D.3).
func callPaths() []string {
	ns := readNamespace
	return []string{"/apis",
		"/api/v1/namespaces/" + ns + "/pods", "/apis/apps/v1/namespaces/" + ns + "/replicasets",
		"/apis/apps/v1/namespaces/" + ns + "/deployments", "/apis/apps/v1/namespaces/" + ns + "/statefulsets",
		"/apis/apps/v1/namespaces/" + ns + "/daemonsets", "/apis/batch/v1/namespaces/" + ns + "/jobs",
		"/apis/batch/v1/namespaces/" + ns + "/cronjobs"}
}

// repeat is n copies of s.
func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

var (
	fakeOnce  sync.Once
	fakeProg  string
	fakeChild string
	fakeErr   error
)

// fakeProgram is internal/kube/credexec/testdata/credfake, built once per run, and a copy
// under another name as the descendant executable.
func fakeProgram(t *testing.T) (program, child string) {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "credfake-")
		if err != nil {
			fakeErr = err
			return
		}
		fakeProg = filepath.Join(dir, "credfake")
		out, err := exec.Command("go", "build", "-o", fakeProg, "../../../internal/kube/credexec/testdata/credfake").CombinedOutput()
		if err != nil {
			fakeErr = fmt.Errorf("go build credfake: %v: %s", err, out)
			return
		}
		raw, err := os.ReadFile(fakeProg)
		if err == nil {
			fakeChild = filepath.Join(dir, "credchild")
			err = os.WriteFile(fakeChild, raw, 0o700)
		}
		fakeErr = err
	})
	if fakeErr != nil {
		t.Fatalf("fake program: %v", fakeErr)
	}
	return fakeProg, fakeChild
}

// program is one fake program setup: its record directory and its kubeconfig user.
type program struct {
	dir  string
	path string
}

// newProgram prepares the fake program with one plan per run (the last one repeats).
// mode is the exec entry's interactiveMode. The exec entry carries the exec canaries as
// an argument and an environment value.
func newProgram(t *testing.T, mode string, plans ...string) (*program, map[string]any) {
	t.Helper()
	prog, child := fakeProgram(t)
	_, arg, envCanary := canaries()
	dir := t.TempDir()
	env := []any{
		map[string]any{"name": "INTUITIK_CANARY_ENV", "value": envCanary},
		map[string]any{"name": "CREDFAKE_DIR", "value": dir},
		map[string]any{"name": "CREDFAKE_CHILD", "value": child},
	}
	for i, p := range plans {
		env = append(env, map[string]any{"name": "CREDFAKE_RUN" + strconv.Itoa(i+1), "value": p})
	}
	if len(plans) > 0 {
		env = append(env, map[string]any{"name": "CREDFAKE_RUN", "value": plans[len(plans)-1]})
	}
	return &program{dir: dir, path: prog}, map[string]any{"exec": map[string]any{
		"apiVersion":      "client.authentication.k8s.io/v1",
		"command":         prog,
		"args":            []any{arg},
		"env":             env,
		"interactiveMode": mode,
	}}
}

func (p *program) lines(name string) []string {
	raw, _ := os.ReadFile(filepath.Join(p.dir, name))
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// starts is the number of recorded program starts.
func (p *program) starts() int { return len(p.lines("starts")) }

// startTimes are the recorded start times of the runs.
func (p *program) startTimes() []time.Time {
	var out []time.Time
	for _, l := range p.lines("starts") {
		if f := strings.Fields(l); len(f) == 4 {
			n, _ := strconv.ParseInt(f[3], 10, 64)
			out = append(out, time.Unix(0, n))
		}
	}
	return out
}

// pids are the PIDs of the runs from run (counted from 1) on, and every descendant.
func (p *program) pids(from int) []int {
	var out []int
	for i, l := range p.lines("starts") {
		if f := strings.Fields(l); len(f) >= 2 && i+1 >= from {
			n, _ := strconv.Atoi(f[1])
			out = append(out, n)
		}
	}
	for _, l := range p.lines("descendants") {
		n, _ := strconv.Atoi(l)
		out = append(out, n)
	}
	return out
}

// waitStarts waits until the program recorded n starts, for at most limit.
func (p *program) waitStarts(n int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for p.starts() < n {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// waitDescendants waits until the program recorded n descendants.
func (p *program) waitDescendants(n int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for len(p.lines("descendants")) < n {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// info is KUBERNETES_EXEC_INFO as run n received it.
func (p *program) info(n int) map[string]any {
	raw, _ := os.ReadFile(filepath.Join(p.dir, fmt.Sprintf("info-%d.json", n)))
	var v map[string]any
	json.Unmarshal(raw, &v)
	return v
}

// gone waits until none of pids is alive, for at most limit after since, and returns the
// PIDs alive then and the time from since.
func gone(pids []int, since time.Time, limit time.Duration) ([]int, time.Duration) {
	for {
		var alive []int
		for _, pid := range pids {
			if processAlive(pid) {
				alive = append(alive, pid)
			}
		}
		if len(alive) == 0 || time.Since(since) > limit {
			return alive, time.Since(since)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// writeToken writes a token to a file for the plan step tokenfile=.
func writeToken(t *testing.T, token string) string {
	t.Helper()
	return writeFile(t, filepath.Join(t.TempDir(), "token"), []byte(token))
}

// credRecord is one request the fake API server received.
type credRecord struct{ method, path, token string }

// credServer is a TLS fake API server that records the method, path and bearer token of
// every request. It answers 401 to a rejected token; otherwise it answers by path: the
// APIVersions document for /api, an APIGroupList with no group outside the built-in list
// for /apis, an empty list of the right kind for the LIST of each of the seven kinds in
// any namespace, and 404 for anything else.
type credServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	records  []credRecord
	rejected map[string]bool
}

// listKinds are the list kinds of the seven LISTs, by API prefix and resource.
var listKinds = map[string]string{
	"/api/v1/pods": "PodList", "/apis/apps/v1/replicasets": "ReplicaSetList", "/apis/apps/v1/deployments": "DeploymentList",
	"/apis/apps/v1/statefulsets": "StatefulSetList", "/apis/apps/v1/daemonsets": "DaemonSetList",
	"/apis/batch/v1/jobs": "JobList", "/apis/batch/v1/cronjobs": "CronJobList",
}

// listKind is the list kind of a namespaced LIST path of the seven kinds, or "".
func listKind(path string) string {
	prefix, rest, ok := strings.Cut(path, "/namespaces/")
	if !ok {
		return ""
	}
	_, resource, ok := strings.Cut(rest, "/")
	if !ok || strings.Contains(resource, "/") {
		return ""
	}
	return listKinds[prefix+"/"+resource]
}

func newCredServer(t *testing.T) *credServer {
	t.Helper()
	s := &credServer{rejected: map[string]bool{}}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		s.records = append(s.records, credRecord{r.Method, r.URL.Path, token})
		reject := s.rejected[token]
		s.mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api":
			w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case r.URL.Path == "/apis":
			w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
		case listKind(r.URL.Path) != "":
			fmt.Fprintf(w, `{"kind":%q,"apiVersion":"v1","metadata":{"resourceVersion":"1"},"items":[]}`, listKind(r.URL.Path))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","code":404,"reason":"NotFound"}`))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *credServer) reject(tokens ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range tokens {
		s.rejected[k] = true
	}
}

// received are the tokens of the requests from index from, as names the test gave them.
func (s *credServer) received(from int, names map[string]string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.records[from:] {
		if n, ok := names[r.token]; ok {
			out = append(out, n)
		} else {
			out = append(out, "other")
		}
	}
	return out
}

// paths are the paths of the requests from index from, in arrival order.
func (s *credServer) paths(from int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.records[from:] {
		out = append(out, r.path)
	}
	return out
}

// pairs is the (method path, token name) record from index from, for the log.
func (s *credServer) pairs(from int, names map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.records[from:] {
		n, ok := names[r.token]
		if !ok {
			n = "other"
		}
		out = append(out, fmt.Sprintf("(%s %s, %s)", r.method, r.path, n))
	}
	return "[" + strings.Join(out, " ") + "]"
}

func (s *credServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// withServer adds the fake server as cluster name with its CA.
func (k *kube) withServer(name string, s *credServer) *kube {
	return k.cluster(name, s.srv.URL, caOf(s.srv))
}

// readOnce sends one call of the product's graph route (GET /api/graph/containment, packet
// FA2-T5 G8) and returns the status it answered (200, or 503 from the gate for the state
// then). The raw response (status line, headers and body) joins the collected API
// responses.
func (p *panel) readOnce(timeout time.Duration) int {
	p.t.Helper()
	req, _ := http.NewRequest("GET", p.origin+readPath, nil)
	req.Header.Set("Authorization", "Bearer "+p.sess)
	c := &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := c.Do(req)
	if err != nil {
		return 0
	}
	raw, _ := httputil.DumpResponse(resp, true)
	resp.Body.Close()
	allOutput.addResponse(raw)
	return resp.StatusCode
}

// window returns the lines that appeared on the stream after before, after waiting d.
func window(l *lines, before int, d time.Duration) []string {
	time.Sleep(d)
	all := l.list()
	if before > len(all) {
		return nil
	}
	return all[before:]
}

// binaryInfo names the binary of a panel for the report: path and sha256.
func binaryInfo(t *testing.T, p *panel) string {
	t.Helper()
	return fmt.Sprintf("%s (%s)", filepath.Base(p.bin), fileStamp(t, p.bin))
}
