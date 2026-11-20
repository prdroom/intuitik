//go:build kind

// Package server holds the kind test of packet FA2-T12 (job perf-server): the server
// performance harness. TestPerfServer creates its own kind cluster (kind-config.yaml,
// audit-policy.yaml), runs kwok out of cluster for the fake nodes, builds the synthetic
// cluster of one size (package gen), measures the release binary (INTUITIK_E2E_BINARY)
// and calls tools/perfjudge with what it recorded. It holds no judge logic: every count,
// class and verdict comes from tools/perfjudge. Requests go through net/http, so no
// client-go import leaves the read-only layer (readonly-gate R1). Every kubeconfig and
// token is made at run time, so no credential is committed.
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	clusterName = "intuitik-perf"
	nodeName    = "intuitik-perf-control-plane"
	auditDir    = "/tmp/intuitik-perf-audit" // kind-config.yaml extraMounts hostPath
	auditLog    = "/var/log/kubernetes/audit/audit.log"
	perfNS      = "intuitik-perf"
	panelUser   = "system:serviceaccount:intuitik-perf:panel"
	controlUser = "system:serviceaccount:intuitik-perf:control"
)

var started = time.Now()

// say prints one harness line to the test output, which the job prints as it is.
func say(format string, a ...any) { fmt.Printf(format+"\n", a...) }

// phase prints the elapsed time of a phase since the test started (packet 7).
func phase(name string, since time.Time) {
	say("PERF-PHASE %s seconds=%.1f elapsed_s=%.1f", name, time.Since(since).Seconds(), time.Since(started).Seconds())
}

// env reads one required variable of the job.
func env(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("PERF verdict=ERROR %s is not set", name)
	}
	return v
}

// run runs a command and returns its stdout; an error is a preparation failure.
func run(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("PERF verdict=ERROR %s %s: %v: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out
}

// cluster is the kind cluster of the test and its admin client.
type cluster struct {
	kubeconfig string
	server     string
	caData     string
	admin      *http.Client
}

// createCluster creates the kind cluster with the audit policy and reads its admin
// credentials; the cluster is deleted at the end of the test.
func createCluster(t *testing.T, work string) *cluster {
	t.Helper()
	t0 := time.Now()
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile("audit-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(auditDir, "audit-policy.yaml"), policy, 0o644); err != nil {
		t.Fatal(err)
	}
	c := &cluster{kubeconfig: filepath.Join(work, "kubeconfig")}
	image := env(t, "INTUITIK_PERF_KIND_IMAGE")
	say("PERF-CLUSTER kind node image %s", image)
	t.Cleanup(func() {
		out, err := exec.Command("kind", "delete", "cluster", "--name", clusterName, "--kubeconfig", c.kubeconfig).CombinedOutput()
		say("PERF-CLUSTER delete: %v %s", err, strings.TrimSpace(string(out)))
	})
	run(t, "kind", "create", "cluster", "--name", clusterName, "--image", image, "--config", "kind-config.yaml",
		"--kubeconfig", c.kubeconfig, "--wait", "180s")
	phase("cluster-create", t0)
	raw := run(t, "kubectl", "config", "view", "--raw", "-o", "json", "--kubeconfig", c.kubeconfig)
	var v struct {
		Clusters []struct {
			Cluster struct {
				Server string `json:"server"`
				CA     string `json:"certificate-authority-data"`
			} `json:"cluster"`
		} `json:"clusters"`
		Users []struct {
			User struct {
				Cert string `json:"client-certificate-data"`
				Key  string `json:"client-key-data"`
			} `json:"user"`
		} `json:"users"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Clusters) != 1 || len(v.Users) != 1 {
		t.Fatalf("PERF verdict=ERROR the kind kubeconfig is not one cluster and one user (%v)", err)
	}
	c.server, c.caData = v.Clusters[0].Cluster.Server, v.Clusters[0].Cluster.CA
	pool := x509.NewCertPool()
	ca, _ := base64.StdEncoding.DecodeString(c.caData)
	certPEM, _ := base64.StdEncoding.DecodeString(v.Users[0].User.Cert)
	keyPEM, _ := base64.StdEncoding.DecodeString(v.Users[0].User.Key)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if !pool.AppendCertsFromPEM(ca) || err != nil {
		t.Fatalf("PERF verdict=ERROR the kind credentials cannot be read (%v)", err)
	}
	c.admin = &http.Client{Timeout: 120 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}}, MaxIdleConnsPerHost: 64}}
	return c
}

// tokenClient is a client of the cluster that sends a bearer token.
func (c *cluster) tokenClient(token string) *http.Client {
	pool := x509.NewCertPool()
	ca, _ := base64.StdEncoding.DecodeString(c.caData)
	pool.AppendCertsFromPEM(ca)
	return &http.Client{Timeout: 120 * time.Second, Transport: &bearer{token: token, next: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b *bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// do sends one request with client and returns the status, body and Audit-Id header.
func (c *cluster) do(ctx context.Context, client *http.Client, method, path string, body any, accept string) (int, []byte, string, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, "", err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, rd)
	if err != nil {
		return 0, nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header.Get("Audit-Id"), err
}

// create posts one object as the admin, retrying a 429 or 5xx answer; any other answer
// than 201 is an error.
func (c *cluster) create(path string, obj any) error {
	for attempt := 1; ; attempt++ {
		status, b, _, err := c.do(context.Background(), c.admin, "POST", path, obj, "")
		if err == nil && status == http.StatusCreated {
			return nil
		}
		if attempt < 6 && (err != nil || status == http.StatusTooManyRequests || status >= 500) {
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		if err != nil {
			return fmt.Errorf("POST %s: %v", path, err)
		}
		return fmt.Errorf("POST %s: status %d: %.300s", path, status, b)
	}
}

// mustCreate creates objects with workers in parallel; an error is a preparation failure.
func (c *cluster) mustCreate(t *testing.T, what string, objs []object) {
	t.Helper()
	t0 := time.Now()
	jobs := make(chan object)
	var mu sync.Mutex
	var first error
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for o := range jobs {
				if err := c.create(o.path, o.body); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, o := range objs {
		jobs <- o
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		t.Fatalf("PERF verdict=ERROR preparation: creating %s: %v", what, first)
	}
	phase("create-"+what+fmt.Sprintf(" objects=%d", len(objs)), t0)
}

// object is one object to create.
type object struct {
	path string
	body any
}

// metadataAccept asks for a list as metadata only.
const metadataAccept = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"

// listItem is the part of a listed object the harness reads.
type listItem struct {
	Metadata struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		Labels          map[string]string `json:"labels"`
		ResourceVersion string            `json:"resourceVersion"`
	} `json:"metadata"`
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

// list reads every page of a list as the admin (limit 500 and continue).
func (c *cluster) list(t *testing.T, path, selector string, metadataOnly bool) []listItem {
	t.Helper()
	accept := ""
	if metadataOnly {
		accept = metadataAccept
	}
	var out []listItem
	cont := ""
	for {
		q := "?limit=500&labelSelector=" + selector
		if cont != "" {
			q += "&continue=" + cont
		}
		status, b, _, err := c.do(context.Background(), c.admin, "GET", path+q, nil, accept)
		if err != nil || status != 200 {
			t.Fatalf("PERF verdict=ERROR list %s: status %d %v: %.300s", path, status, err, b)
		}
		var l struct {
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
			Items []listItem `json:"items"`
		}
		if err := json.Unmarshal(b, &l); err != nil {
			t.Fatalf("PERF verdict=ERROR list %s does not decode: %v", path, err)
		}
		out = append(out, l.Items...)
		if l.Metadata.Continue == "" {
			return out
		}
		cont = l.Metadata.Continue
	}
}

// startKwok runs kwok out of cluster for the nodes annotated kwok.x-k8s.io/node=fake, with
// the checked stage file stage-fast.yaml (packet 6.2).
func startKwok(t *testing.T, c *cluster, work string) {
	t.Helper()
	logPath := filepath.Join(work, "kwok.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(env(t, "INTUITIK_PERF_KWOK"),
		"--kubeconfig="+c.kubeconfig,
		"--config="+env(t, "INTUITIK_PERF_KWOK_STAGES"),
		"--manage-all-nodes=false",
		"--manage-nodes-with-annotation-selector=kwok.x-k8s.io/node=fake",
		"--manage-nodes-with-label-selector=",
		"--disregard-status-with-annotation-selector=kwok.x-k8s.io/status=custom",
		"--disregard-status-with-label-selector=",
		"--cidr=10.0.0.1/16",
		"--node-ip=10.0.0.1",
		// stage-fast.yaml renews node status only every 10 to 20 minutes and relies on the
		// node lease for liveness; without leases the node lifecycle controller would mark
		// the fake nodes unknown and evict their Pods during the runs.
		"--node-lease-duration-seconds=40")
	cmd.Stdout, cmd.Stderr = f, f
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("PERF verdict=ERROR starting kwok: %v", err)
	}
	say("PERF-KWOK pid=%d args=%q", cmd.Process.Pid, cmd.Args[1:])
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		f.Close()
		if b, err := os.ReadFile(logPath); err == nil {
			ls := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(ls) > 20 {
				ls = ls[len(ls)-20:]
			}
			say("PERF-KWOK log tail (%d lines kept):\n%s", len(ls), strings.Join(ls, "\n"))
		}
	})
}

// rbac creates the namespace intuitik-perf, the ServiceAccounts panel and control, and the
// read-only ClusterRole of packet 6.3 (get, list and watch on the kinds the engine reads;
// no Secret get) bound to both, and returns a token of each.
func (c *cluster) rbac(t *testing.T) (panelToken, controlToken string) {
	t.Helper()
	read := []any{"get", "list", "watch"}
	rule := func(group string, verbs []any, resources ...string) map[string]any {
		rs := make([]any, len(resources))
		for i, r := range resources {
			rs[i] = r
		}
		return map[string]any{"apiGroups": []any{group}, "resources": rs, "verbs": verbs}
	}
	objs := []object{
		{"/api/v1/namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": perfNS}}},
		{"/apis/rbac.authorization.k8s.io/v1/clusterroles", map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole",
			"metadata": map[string]any{"name": "intuitik-perf-read"},
			"rules": []any{
				rule("", read, "pods", "services", "persistentvolumeclaims", "configmaps", "namespaces", "nodes", "persistentvolumes"),
				rule("", []any{"list", "watch"}, "secrets"),
				rule("apps", read, "replicasets", "deployments", "statefulsets", "daemonsets"),
				rule("batch", read, "jobs", "cronjobs"),
				rule("networking.k8s.io", read, "ingresses"),
				rule("autoscaling", read, "horizontalpodautoscalers"),
				rule("storage.k8s.io", read, "storageclasses"),
				rule("gateway.networking.k8s.io", read, "httproutes"),
			}}},
	}
	for _, o := range objs {
		if err := c.create(o.path, o.body); err != nil {
			t.Fatalf("PERF verdict=ERROR preparation: %v", err)
		}
	}
	tokens := map[string]string{}
	for _, sa := range []string{"panel", "control"} {
		if err := c.create("/api/v1/namespaces/"+perfNS+"/serviceaccounts", map[string]any{"apiVersion": "v1", "kind": "ServiceAccount",
			"metadata": map[string]any{"name": sa, "namespace": perfNS}}); err != nil {
			t.Fatalf("PERF verdict=ERROR preparation: %v", err)
		}
		if err := c.create("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind": "ClusterRoleBinding", "metadata": map[string]any{"name": "intuitik-perf-" + sa},
			"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "intuitik-perf-read"},
			"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": sa, "namespace": perfNS}}}); err != nil {
			t.Fatalf("PERF verdict=ERROR preparation: %v", err)
		}
		status, b, _, err := c.do(context.Background(), c.admin, "POST", "/api/v1/namespaces/"+perfNS+"/serviceaccounts/"+sa+"/token",
			map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": map[string]any{"expirationSeconds": 10800}}, "")
		var tr struct {
			Status struct {
				Token string `json:"token"`
			} `json:"status"`
		}
		if err != nil || status != http.StatusCreated || json.Unmarshal(b, &tr) != nil || tr.Status.Token == "" {
			t.Fatalf("PERF verdict=ERROR preparation: token of %s: status %d %v", sa, status, err)
		}
		tokens[sa] = tr.Status.Token
	}
	return tokens["panel"], tokens["control"]
}

// tokenKubeconfig writes a kubeconfig of the cluster for a bearer token.
func (c *cluster) tokenKubeconfig(t *testing.T, name, token string) string {
	t.Helper()
	doc := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": name,
		"clusters": []any{map[string]any{"name": name, "cluster": map[string]any{"server": c.server, "certificate-authority-data": c.caData}}},
		"users":    []any{map[string]any{"name": name, "user": map[string]any{"token": token}}},
		"contexts": []any{map[string]any{"name": name, "context": map[string]any{"cluster": name, "user": name}}}}
	raw, _ := json.Marshal(doc)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readAudit reads the whole audit log of the node.
func readAudit(t *testing.T) [][]byte {
	t.Helper()
	out := run(t, "docker", "exec", nodeName, "cat", auditLog)
	var lines [][]byte
	for _, l := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			lines = append(lines, l)
		}
	}
	return lines
}

var listenLine = regexp.MustCompile(`^listening on http://127\.0\.0\.1:([0-9]+)`)

// lines is a concurrency-safe capture of one output stream.
type lines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lines) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, s := range strings.Split(l.buf.String(), "\n") {
		if s = strings.TrimRight(s, "\r"); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// panel is one running release binary, logged in with one session (the pattern of
// test/e2e-api/appendix-a/harness_kind_test.go, copied because test files of another
// package cannot be imported).
type panel struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *lines
	stderr *lines
	origin string
	sess   string
}

var apiClient = &http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

// startPanel starts the release binary on kubeconfig, logs in and waits for connected.
func startPanel(t *testing.T, kubeconfig string) (*panel, error) {
	t.Helper()
	bin := env(t, "INTUITIK_E2E_BINARY")
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	openLog := filepath.Join(dir, "opened.txt")
	recorder := filepath.Join(dir, "record-open")
	if err := os.WriteFile(recorder, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+openLog+"'\n"), 0o700); err != nil {
		return nil, err
	}
	var envv []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "KUBECONFIG" || k == "HOME" || k == "BROWSER" || strings.HasPrefix(k, "INTUITIK_") {
			continue
		}
		envv = append(envv, kv)
	}
	envv = append(envv, "HOME="+home, "BROWSER="+recorder, "KUBECONFIG="+kubeconfig)
	p := &panel{stdout: &lines{}, stderr: &lines{}}
	p.cmd = exec.Command(bin)
	p.cmd.Env = envv
	p.cmd.Stdout, p.cmd.Stderr = p.stdout, p.stderr
	p.cmd.WaitDelay = 5 * time.Second
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	p.stdin = stdin
	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %v", bin, err)
	}
	t.Cleanup(p.stop)
	deadline := time.Now().Add(30 * time.Second)
	for p.origin == "" {
		for _, s := range p.stdout.list() {
			if m := listenLine.FindStringSubmatch(s); m != nil {
				p.origin = "http://127.0.0.1:" + m[1]
			}
		}
		if time.Now().After(deadline) {
			return p, fmt.Errorf("no listen line within 30 s; stdout %q, stderr %q", p.stdout.list(), p.stderr.list())
		}
		time.Sleep(10 * time.Millisecond)
	}
	var token string
	for token == "" {
		raw, _ := os.ReadFile(openLog)
		if _, tok, ok := strings.Cut(strings.TrimSpace(string(raw)), "#t="); ok {
			token = tok
		}
		if time.Now().After(deadline) {
			return p, errors.New("the opener received no login address")
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]string{"t": token})
	status, raw, err := p.request("POST", "/api/session", body, false)
	var v struct{ Session string }
	if err != nil || status != 200 || json.Unmarshal(raw, &v) != nil || v.Session == "" {
		return p, fmt.Errorf("login exchange: status %d %v", status, err)
	}
	p.sess = v.Session
	for {
		status, raw, err := p.request("GET", "/api/cluster", nil, true)
		var o struct {
			State string `json:"state"`
		}
		if err == nil && status == 200 && json.Unmarshal(raw, &o) == nil && o.State == "connected" {
			return p, nil
		}
		if err == nil && status == 200 && o.State != "connecting" && o.State != "" {
			return p, fmt.Errorf("the panel context did not connect: %s", o.State)
		}
		if time.Now().After(deadline.Add(15 * time.Second)) {
			return p, fmt.Errorf("the state stayed %q; stderr %q", o.State, p.stderr.list())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// request sends one request to the panel and returns its status and body.
func (p *panel) request(method, path string, body []byte, auth bool) (int, []byte, error) {
	req, err := http.NewRequest(method, p.origin+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", p.origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+p.sess)
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, b, err
}

// stop ends the binary.
func (p *panel) stop() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	p.stdin.Close()
	p.cmd.Process.Kill()
	p.cmd.Wait()
}
