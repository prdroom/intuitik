//go:build kind

// Package connect holds the cluster connection tests of packet M1-T3a sections 4 and 6.
// They run in the e2e-kind job against the product binary (INTUITIK_E2E_BINARY), the
// e2etest build (INTUITIK_E2E_TEST_BINARY) and the kind cluster whose kubeconfig is
// INTUITIK_E2E_KIND_KUBECONFIG. Every fixture kubeconfig is written at run time, so no
// credential is committed.
package connect

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Product texts of packet section 3.3, written here independently of the product.
const (
	textOK  = "Connected to cluster %s (context %s)."
	textNK  = "No kubeconfig found. intuitik looked in: %s. Set KUBECONFIG to the path of your kubeconfig file, or run your cloud's login command to create one."
	textIV1 = "Cannot read the kubeconfig %s: %s. Fix the file, or set KUBECONFIG to a valid kubeconfig file."
	textIV2 = "The kubeconfig has no current context. Choose a context in the intuitik tab, or set one with: kubectl config use-context %s"
	textIV3 = "Context %s refers to a cluster or user that is not in the kubeconfig. Fix the kubeconfig, or choose another context."
	textIV4 = "The kubeconfig %s has no contexts. Run your cloud's login command to add one, or set KUBECONFIG to another kubeconfig file."
	textAU  = "The cluster %s rejected the credentials of context %s (HTTP 401). They may have expired. Log in again with your cloud's login command, then retry from the intuitik tab or restart intuitik."
	textNC  = "Context %s has no credentials for cluster %s. intuitik reads a cluster only with the credentials of your kubeconfig. Log in with your cloud's login command, then retry from the intuitik tab, or choose another context."
	textEX  = "Context %s signs in with the credential program %s, which this build of intuitik does not run. Choose another context."
	textAP  = `Context %s uses the auth provider "%s", which intuitik does not support. Update the kubeconfig with your cloud's current login command.`
	textTL  = "The API server %s of context %s presented a certificate that the kubeconfig does not trust: %s. Check that the kubeconfig belongs to this cluster."
	textUR  = "Cannot reach the API server %s of context %s: %s. Check your VPN or network connection, then retry from the intuitik tab or restart intuitik."
	textHS  = "The API server %s of context %s answered HTTP %d instead of the Kubernetes API. Check the server address in the kubeconfig, or retry from the intuitik tab if the server is busy."
	textUC  = "There is no context named %s in the kubeconfig."

	textTimeout = "no answer within 10 s"
	w1          = 10 * time.Second
)

var listenLine = regexp.MustCompile(`^listening on http://127\.0\.0\.1:([0-9]+)`)

// kindInfo is the kind cluster of the job, read once from its kubeconfig.
type kindInfo struct {
	kubeconfig string // INTUITIK_E2E_KIND_KUBECONFIG
	server     string
	caData     string // certificate-authority-data (base64 PEM)
	certData   string // client-certificate-data of the admin user
	keyData    string // client-key-data of the admin user (the client-key canary)
}

var (
	kindOnce sync.Once
	kind     kindInfo
	kindErr  error
)

func kindCluster(t *testing.T) kindInfo {
	t.Helper()
	kindOnce.Do(func() {
		kind.kubeconfig = os.Getenv("INTUITIK_E2E_KIND_KUBECONFIG")
		if kind.kubeconfig == "" {
			kindErr = errors.New("INTUITIK_E2E_KIND_KUBECONFIG is not set")
			return
		}
		out, err := exec.Command("kubectl", "config", "view", "--raw", "-o", "json", "--kubeconfig", kind.kubeconfig).Output()
		if err != nil {
			kindErr = fmt.Errorf("kubectl config view: %v", err)
			return
		}
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
		if err := json.Unmarshal(out, &v); err != nil || len(v.Clusters) != 1 || len(v.Users) != 1 {
			kindErr = fmt.Errorf("the kind kubeconfig is not one cluster and one user (%v)", err)
			return
		}
		kind.server, kind.caData = v.Clusters[0].Cluster.Server, v.Clusters[0].Cluster.CA
		kind.certData, kind.keyData = v.Users[0].User.Cert, v.Users[0].User.Key
	})
	if kindErr != nil {
		t.Fatalf("kind cluster: %v", kindErr)
	}
	return kind
}

// Canaries of section 6.4, made once per run.
var (
	canaryOnce  sync.Once
	canaryToken string // a bearer token the kind API server rejects
	canaryArg   string // an exec argument
	canaryEnv   string // an exec environment value
)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func canaries() (token, arg, env string) {
	canaryOnce.Do(func() {
		canaryToken = strings.Join([]string{"intuitik", "canary", randomHex(16)}, "-")
		canaryArg = "--intuitik-canary-arg=" + randomHex(16)
		canaryEnv = "intuitik-canary-env-" + randomHex(16)
	})
	return canaryToken, canaryArg, canaryEnv
}

// kube builds a kubeconfig file as JSON (a JSON document is valid YAML).
type kube struct {
	current  string
	clusters []any
	users    []any
	contexts []any
}

func (k *kube) cluster(name, server, caData string) *kube {
	c := map[string]any{"server": server}
	if caData != "" {
		c["certificate-authority-data"] = caData
	}
	k.clusters = append(k.clusters, map[string]any{"name": name, "cluster": c})
	return k
}

func (k *kube) user(name string, u map[string]any) *kube {
	k.users = append(k.users, map[string]any{"name": name, "user": u})
	return k
}

func (k *kube) context(name, cluster, user string) *kube {
	k.contexts = append(k.contexts, map[string]any{"name": name, "context": map[string]any{"cluster": cluster, "user": user}})
	return k
}

func (k *kube) bytes() []byte {
	doc := map[string]any{"apiVersion": "v1", "kind": "Config", "clusters": orEmpty(k.clusters), "users": orEmpty(k.users), "contexts": orEmpty(k.contexts)}
	if k.current != "" {
		doc["current-context"] = k.current
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic(err)
	}
	return raw
}

func orEmpty(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

// withKind adds the kind cluster, its admin user and the context kind-ok.
func (k *kube) withKind(t *testing.T) *kube {
	ki := kindCluster(t)
	return k.cluster("kind-c", ki.server, ki.caData).
		user("kind-admin", map[string]any{"client-certificate-data": ki.certData, "client-key-data": ki.keyData}).
		context("kind-ok", "kind-c", "kind-admin")
}

func tokenUser(token string) map[string]any { return map[string]any{"token": token} }

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// collected is the output of every panel of this run and every raw T3 API response
// (section 6.4).
type collected struct {
	mu        sync.Mutex
	output    [][]byte
	responses [][]byte
}

var allOutput = &collected{}

func (c *collected) add(out ...[]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.output = append(c.output, out...)
}

func (c *collected) addResponse(raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = append(c.responses, raw)
}

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

func (l *lines) bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...)
}

func (l *lines) list() []string {
	var out []string
	for _, s := range strings.Split(strings.ReplaceAll(string(l.bytes()), "\r\n", "\n"), "\n") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (l *lines) count(line string) int {
	n := 0
	for _, s := range l.list() {
		if s == line {
			n++
		}
	}
	return n
}

// panelOpts are the inputs of one intuitik process.
type panelOpts struct {
	kubeconfig *string // KUBECONFIG; nil leaves it unset
	home       string  // HOME; "" means a new empty directory
	test       bool    // run the e2etest build
	fixture    string  // INTUITIK_TEST_FIXTURE
}

func strPtr(s string) *string { return &s }

// panel is one running intuitik process, logged in with one session.
type panel struct {
	t       *testing.T
	cmd     *exec.Cmd
	stdout  *lines
	stderr  *lines
	done    chan struct{}
	origin  string
	sess    string
	csrf    string
	started time.Time // when the listen line was seen; the first attempt starts after it
}

func startPanel(t *testing.T, o panelOpts) *panel {
	t.Helper()
	dir := t.TempDir()
	name := "INTUITIK_E2E_BINARY"
	if o.test {
		name = "INTUITIK_E2E_TEST_BINARY"
	}
	bin := os.Getenv(name)
	if bin == "" {
		t.Fatalf("%s is not set", name)
	}
	home := o.home
	if home == "" {
		home = filepath.Join(dir, "home")
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	openLog := filepath.Join(dir, "opened.txt")
	recorder := writeFile(t, filepath.Join(dir, "record-open"), []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+openLog+"'\n"))
	if err := os.Chmod(recorder, 0o700); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "KUBECONFIG" || k == "HOME" || k == "BROWSER" || strings.HasPrefix(k, "INTUITIK_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "BROWSER="+recorder)
	if o.kubeconfig != nil {
		env = append(env, "KUBECONFIG="+*o.kubeconfig)
	}
	if o.fixture != "" {
		env = append(env, "INTUITIK_TEST_FIXTURE="+o.fixture)
	}
	p := &panel{t: t, stdout: &lines{}, stderr: &lines{}, done: make(chan struct{})}
	p.cmd = exec.Command(bin)
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.stdout, p.stderr
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", bin, err)
	}
	go func() {
		p.cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		stdin.Close()
		p.cmd.Process.Kill()
		<-p.done
		allOutput.add(p.stdout.bytes(), p.stderr.bytes())
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		var port string
		for _, s := range p.stdout.list() {
			if m := listenLine.FindStringSubmatch(s); m != nil {
				port = m[1]
			}
		}
		if port != "" {
			p.started = time.Now()
			p.origin = "http://127.0.0.1:" + port
			break
		}
		select {
		case <-p.done:
			t.Fatalf("intuitik exited before the listen line; stdout %q, stderr %q", p.stdout.list(), p.stderr.list())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no listen line within 30 s; stdout %q, stderr %q", p.stdout.list(), p.stderr.list())
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.login(openLog)
	return p
}

// login exchanges the one-time token that the opener received for a session.
func (p *panel) login(openLog string) {
	p.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var token string
	for token == "" {
		raw, _ := os.ReadFile(openLog)
		if _, t, ok := strings.Cut(strings.TrimSpace(string(raw)), "#t="); ok {
			token = t
			break
		}
		if time.Now().After(deadline) {
			p.t.Fatal("the opener received no login address within 10 s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]string{"t": token})
	resp := p.request("POST", "/api/session", body, false)
	var v struct{ Session, Csrf string }
	if resp.status != 200 || json.Unmarshal(resp.body, &v) != nil || v.Session == "" {
		p.t.Fatalf("login exchange: status %d body %q", resp.status, resp.body)
	}
	p.sess, p.csrf = v.Session, v.Csrf
}

// response is one HTTP answer of the panel with its raw form.
type response struct {
	status int
	body   []byte
	raw    []byte
}

var apiClient = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

// request sends one request to the panel; auth adds the session and, on POST, the
// state-change headers of T2 S6.
func (p *panel) request(method, path string, body []byte, auth bool) response {
	p.t.Helper()
	req, err := http.NewRequest(method, p.origin+path, bytes.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", p.origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+p.sess)
		if method == "POST" {
			req.Header.Set("X-CSRF-Token", p.csrf)
		}
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := httputil.DumpResponse(resp, true)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.HasPrefix(path, "/api/cluster") || strings.HasPrefix(path, "/api/fixture-cluster") {
		allOutput.addResponse(raw)
	}
	return response{status: resp.StatusCode, body: b, raw: raw}
}

// clusterObj is the cluster object of section 6.1.
type clusterObj struct {
	State           string    `json:"state"`
	TextID          *string   `json:"text_id"`
	Message         *string   `json:"message"`
	Context         *string   `json:"context"`
	Cluster         *string   `json:"cluster"`
	Server          *string   `json:"server"`
	KubeconfigPaths []string  `json:"kubeconfig_paths"`
	Contexts        []ctxItem `json:"contexts"`
}

type ctxItem struct {
	Name    string `json:"name"`
	Cluster string `json:"cluster"`
	Server  string `json:"server"`
	Current bool   `json:"current"`
	Active  bool   `json:"active"`
}

func str(s *string) string {
	if s == nil {
		return "<null>"
	}
	return *s
}

func (o clusterObj) names() []string {
	var n []string
	for _, c := range o.Contexts {
		n = append(n, c.Name)
	}
	return n
}

func (o clusterObj) flagged(current bool) string {
	for _, c := range o.Contexts {
		if (current && c.Current) || (!current && c.Active) {
			return c.Name
		}
	}
	return ""
}

func (p *panel) cluster() (clusterObj, response) {
	p.t.Helper()
	r := p.request("GET", "/api/cluster", nil, true)
	var o clusterObj
	if r.status != 200 || json.Unmarshal(r.body, &o) != nil {
		p.t.Fatalf("GET /api/cluster: status %d body %q", r.status, r.body)
	}
	return o, r
}

// settle polls GET /api/cluster every 100 ms until the state is not connecting, for at
// most limit. ok is false when the limit passed first.
func (p *panel) settle(limit time.Duration) (clusterObj, bool) {
	p.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		o, _ := p.cluster()
		if o.State != "connecting" && o.State != "" {
			return o, true
		}
		if time.Now().After(deadline) {
			return o, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// mustSettle is settle with the W1 limit plus 5 s; it stops the test on a timeout.
func (p *panel) mustSettle() clusterObj {
	p.t.Helper()
	o, ok := p.settle(w1 + 5*time.Second)
	if !ok {
		p.t.Fatalf("the state stayed %q for more than %s; stderr %q", o.State, w1+5*time.Second, p.stderr.list())
	}
	return o
}

func (p *panel) switchTo(name string) response {
	p.t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	return p.request("POST", "/api/cluster/context", body, true)
}

func (p *panel) retry() response {
	p.t.Helper()
	return p.request("POST", "/api/cluster/retry", []byte("{}"), true)
}

// waitCount waits up to 3 s for line to appear n times on the stream and returns the
// count it saw.
func waitCount(l *lines, line string, n int) int {
	deadline := time.Now().Add(3 * time.Second)
	for {
		c := l.count(line)
		if c >= n || time.Now().After(deadline) {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// running reports that the process is alive and GET / still answers 200.
func (p *panel) running() error {
	select {
	case <-p.done:
		return errors.New("the process exited")
	default:
	}
	r := p.request("GET", "/", nil, false)
	if r.status != 200 {
		return fmt.Errorf("GET / answered %d", r.status)
	}
	return nil
}

// fileStamp is the sha256 and modification time of a kubeconfig file (W4).
func fileStamp(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256=%s mtime=%s", hex.EncodeToString(sum[:]), st.ModTime().UTC().Format(time.RFC3339Nano))
}

// closedPort returns a 127.0.0.1 port that nothing listens on and the OS error that a
// connection to it gets, for example "connect: connection refused".
func closedPort(t *testing.T) (int, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err = net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	var op *net.OpError
	if !errors.As(err, &op) {
		t.Fatalf("dial of the closed port %d: %v", port, err)
	}
	return port, op.Err.Error()
}

// heldServer accepts TCP connections and never completes the TLS handshake.
type heldServer struct {
	ln       net.Listener
	mu       sync.Mutex
	arrivals int
	conns    []net.Conn
}

func newHeldServer(t *testing.T) *heldServer {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &heldServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h.mu.Lock()
			h.arrivals++
			h.conns = append(h.conns, c)
			h.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		h.mu.Lock()
		for _, c := range h.conns {
			c.Close()
		}
		h.mu.Unlock()
	})
	return h
}

func (h *heldServer) url() string { return "https://" + h.ln.Addr().String() }

func (h *heldServer) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.arrivals
}

// kindRequest sends GET path to the kind API server with the kind CA and an optional
// bearer token, and returns the status.
func kindRequest(t *testing.T, path, token string) int {
	t.Helper()
	ki := kindCluster(t)
	ca, err := base64.StdEncoding.DecodeString(ki.caData)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	req, _ := http.NewRequest("GET", ki.server+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s on the kind API server: %v", path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// kubectl runs kubectl with the kind admin kubeconfig.
func kubectl(t *testing.T, stdin []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--kubeconfig", kindCluster(t).kubeconfig}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("kubectl %s: %v: %s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("kubectl %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// waitStatus polls GET /api on the kind API server with token until it answers want,
// for at most 30 s (section 6.6, D4 setup).
func waitStatus(t *testing.T, token string, want int) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got := kindRequest(t, "/api", token)
		if got == want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// sortedKeys lists the keys of a JSON object.
func sortedKeys(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

// firstLines returns the first lines of a byte slice, for failure messages.
func firstLines(b []byte, n int) []string {
	var out []string
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() && len(out) < n {
		out = append(out, s.Text())
	}
	return out
}
