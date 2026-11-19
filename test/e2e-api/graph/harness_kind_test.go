//go:build kind

// Package graph holds the kind tests of packet FA2-T5 (job e2e-graph): the containment
// answer of GET /api/graph/containment on the release binary (INTUITIK_E2E_BINARY)
// against the graph kind cluster of the job (INTUITIK_E2E_KIND_KUBECONFIG), whose garbage
// collector controller is off and whose API server writes an audit log
// (INTUITIK_E2E_GRAPH_AUDIT_LOG inside the node container INTUITIK_E2E_GRAPH_NODE). Every
// fixture and kubeconfig is made at run time, so no credential is committed.
package graph

import (
	"bufio"
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
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

// Product texts, written here independently of the product (packets M1-T3a 3.3, FA2-T5
// section 3).
const (
	textAU = "The cluster %s rejected the credentials of context %s (HTTP 401). They may have expired. Log in again with your cloud's login command, then retry from the intuitik tab or restart intuitik."
	textEP = "The credential program %s of context %s failed (%s). Run it in a terminal to see why, or log in again with your cloud's login command."
	textUR = "Cannot reach the API server %s of context %s: %s. Check your VPN or network connection, then retry from the intuitik tab or restart intuitik."

	textMissing       = "Owner %s/%s not found"
	textForbidden     = "Owner not readable \u2014 no permission"
	textClusterScoped = "Owner %s/%s is cluster-scoped"
	textKinds         = "Couldn't load %s (%d)"

	w1 = 10 * time.Second
	// callK is the least number of requests of one graph call on this cluster: /apis, the
	// seven LISTs, the four LISTs of the bridge kinds (D.3, FA2-T6 E.3), the ConfigMaps and
	// Secrets and the Namespace object (FA2-T7a 6.3). G7 fails a window with fewer matched
	// requests.
	callK = 15
	// productAgent marks every request that intuitik's read-only client sends (M1-T4 R5).
	productAgent = "intuitik-readonly"
)

var listenLine = regexp.MustCompile(`^listening on http://127\.0\.0\.1:([0-9]+)`)

// kindInfo is the graph kind cluster of the job, read once from its kubeconfig.
type kindInfo struct {
	kubeconfig, server, caData, certData, keyData string
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

// kubectl runs kubectl with the kind admin kubeconfig and stops the test on an error.
func kubectl(t *testing.T, stdin []byte, args ...string) []byte {
	t.Helper()
	kindCluster(t)
	out, err := kubectlErr(stdin, args...)
	if err != nil {
		t.Fatalf("kubectl %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func kubectlErr(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("kubectl", append([]string{"--kubeconfig", kind.kubeconfig}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("%v: %s", err, ee.Stderr)
		}
		return out, err
	}
	return out, nil
}

// apply applies one object given as a Go value (JSON) and returns it as the API server
// stored it.
func apply(t *testing.T, obj map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(obj)
	out := kubectl(t, raw, "create", "-f", "-", "-o", "json")
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("kubectl create: %v", err)
	}
	return v
}

// uidOf reads the uid of one object.
func uidOf(t *testing.T, ns, resource, name string) string {
	t.Helper()
	args := []string{"get", resource, name, "-o", "jsonpath={.metadata.uid}"}
	if ns != "" {
		args = append(args, "-n", ns)
	}
	uid := strings.TrimSpace(string(kubectl(t, nil, args...)))
	if uid == "" {
		t.Fatalf("no uid for %s %s/%s", resource, ns, name)
	}
	return uid
}

// owner is one ownerReference of a fixture.
func owner(apiVersion, kind, name, uid string) []any {
	return []any{map[string]any{"apiVersion": apiVersion, "kind": kind, "name": name, "uid": uid, "controller": true}}
}

// meta is the metadata of a fixture object.
func meta(ns, name string, owners []any) map[string]any {
	m := map[string]any{"name": name, "namespace": ns}
	if owners != nil {
		m["ownerReferences"] = owners
	}
	return m
}

// pendingSpec is a pod spec that no node matches, so its pods stay Pending and stable.
func pendingSpec() map[string]any {
	return map[string]any{
		"nodeSelector":                  map[string]any{"intuitik.example/none": "none"},
		"terminationGracePeriodSeconds": 0,
		"containers":                    []any{map[string]any{"name": "c", "image": "registry.k8s.io/pause:3.10"}},
	}
}

// pod is a bare Pending pod with the given owners.
func pod(ns, name string, owners []any) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": meta(ns, name, owners), "spec": pendingSpec()}
}

// waitFor polls cond every 500 ms for at most limit and stops the test with what when it
// does not hold.
func waitFor(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("preparation: %s not reached within %s", what, limit)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// object is the part of a cluster object these tests read.
type object struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name            string            `json:"name"`
		UID             string            `json:"uid"`
		Annotations     map[string]string `json:"annotations"`
		OwnerReferences []struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Name       string `json:"name"`
			UID        string `json:"uid"`
			Controller *bool  `json:"controller"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
}

// objects lists the objects of one resource in ns through kubectl (admin).
func objects(t *testing.T, ns, resource string) []object {
	t.Helper()
	var v struct {
		Items []object `json:"items"`
	}
	if err := json.Unmarshal(kubectl(t, nil, "get", resource, "-n", ns, "-o", "json"), &v); err != nil {
		t.Fatalf("kubectl get %s: %v", resource, err)
	}
	return v.Items
}

// settled waits until the uid set of the namespace's pods and ReplicaSets is the same in
// two reads 2 s apart, so the route sees what the expectations were read from.
func settled(t *testing.T, ns string) {
	t.Helper()
	snap := func() string {
		var uids []string
		for _, r := range []string{"pods", "replicasets", "jobs"} {
			for _, o := range objects(t, ns, r) {
				uids = append(uids, o.Metadata.UID)
			}
		}
		sort.Strings(uids)
		return strings.Join(uids, ",")
	}
	prev := snap()
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)
		cur := snap()
		if cur == prev {
			return
		}
		prev = cur
	}
	t.Fatalf("preparation: the objects of %s did not settle", ns)
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
	for _, s := range strings.Split(string(l.bytes()), "\n") {
		if s = strings.TrimRight(s, "\r"); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// responses are the raw graph answers of this run, for the canary search of C14b.
var responses = struct {
	mu  sync.Mutex
	raw [][]byte
}{}

// panel is one running release binary, logged in with one session.
type panel struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdout *lines
	stderr *lines
	origin string
	sess   string
	user   string // the user name intuitik's requests carry in the audit log
}

// startPanel starts the release binary on kubeconfig and logs in. user is the expected
// user of its requests (G7).
func startPanel(t *testing.T, kubeconfig, user string) *panel {
	t.Helper()
	bin := os.Getenv("INTUITIK_E2E_BINARY")
	if bin == "" {
		t.Fatal("INTUITIK_E2E_BINARY is not set")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	openLog := filepath.Join(dir, "opened.txt")
	recorder := filepath.Join(dir, "record-open")
	if err := os.WriteFile(recorder, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+openLog+"'\n"), 0o700); err != nil {
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
	env = append(env, "HOME="+home, "BROWSER="+recorder, "KUBECONFIG="+kubeconfig)
	p := &panel{t: t, stdout: &lines{}, stderr: &lines{}, user: user}
	p.cmd = exec.Command(bin)
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.stdout, p.stderr
	p.cmd.WaitDelay = 5 * time.Second
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", bin, err)
	}
	t.Cleanup(func() {
		stdin.Close()
		p.cmd.Process.Kill()
		p.cmd.Wait()
	})
	deadline := time.Now().Add(30 * time.Second)
	for p.origin == "" {
		for _, s := range p.stdout.list() {
			if m := listenLine.FindStringSubmatch(s); m != nil {
				p.origin = "http://127.0.0.1:" + m[1]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no listen line within 30 s; stdout %q, stderr %q", p.stdout.list(), p.stderr.list())
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
			t.Fatal("the opener received no login address")
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]string{"t": token})
	status, raw := p.request("POST", "/api/session", body, false)
	var v struct{ Session string }
	if status != 200 || json.Unmarshal(raw, &v) != nil || v.Session == "" {
		t.Fatalf("login exchange: status %d", status)
	}
	p.sess = v.Session
	return p
}

var apiClient = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

// request sends one request to the panel and returns its status and body.
func (p *panel) request(method, path string, body []byte, auth bool) (int, []byte) {
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
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := httputil.DumpResponse(resp, true)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.HasPrefix(path, "/api/graph/") {
		responses.mu.Lock()
		responses.raw = append(responses.raw, raw)
		responses.mu.Unlock()
	}
	return resp.StatusCode, b
}

// clusterObj is the part of the cluster object these tests read.
type clusterObj struct {
	State   string  `json:"state"`
	TextID  *string `json:"text_id"`
	Message *string `json:"message"`
}

func str(s *string) string {
	if s == nil {
		return "<null>"
	}
	return *s
}

// settle polls GET /api/cluster until the state is not connecting (W1 + 5 s).
func (p *panel) settle() clusterObj {
	p.t.Helper()
	deadline := time.Now().Add(w1 + 5*time.Second)
	for {
		status, raw := p.request("GET", "/api/cluster", nil, true)
		var o clusterObj
		if status == 200 && json.Unmarshal(raw, &o) == nil && o.State != "connecting" && o.State != "" {
			return o
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the state stayed %q; stderr %q", o.State, p.stderr.list())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// answer is the graph answer (packet D.1).
type answer struct {
	Namespace string `json:"namespace"`
	Nodes     []node `json:"nodes"`
	Refused   []struct {
		Kind    string `json:"kind"`
		Group   string `json:"group"`
		Version string `json:"version"`
		Status  int    `json:"status"`
		Reason  string `json:"reason"`
		Text    string `json:"text"`
	} `json:"refused"`
}

type node struct {
	UID        string  `json:"uid"`
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	Parent     *string `json:"parent"`
	Owner      struct {
		State     string `json:"state"`
		Reference string `json:"reference"`
		Text      string `json:"text"`
	} `json:"owner"`
}

// audit is the request window of one graph call in the API server audit log (G7).
type audit struct {
	node, path string
	offset     int64
}

// auditStart marks the start of a window: the audit log size now.
func auditStart(t *testing.T) audit {
	t.Helper()
	a := audit{node: os.Getenv("INTUITIK_E2E_GRAPH_NODE"), path: os.Getenv("INTUITIK_E2E_GRAPH_AUDIT_LOG")}
	if a.node == "" || a.path == "" {
		t.Fatal("INTUITIK_E2E_GRAPH_NODE or INTUITIK_E2E_GRAPH_AUDIT_LOG is not set")
	}
	out, err := exec.Command("docker", "exec", a.node, "stat", "-c", "%s", a.path).Output()
	if err != nil {
		t.Fatalf("audit log size: %v", err)
	}
	a.offset, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return a
}

// auditEvent is the part of one audit event that G7 reads.
type auditEvent struct {
	Stage      string `json:"stage"`
	Verb       string `json:"verb"`
	RequestURI string `json:"requestURI"`
	UserAgent  string `json:"userAgent"`
	User       struct {
		Username string `json:"username"`
	} `json:"user"`
	ObjectRef *struct {
		Resource    string `json:"resource"`
		Subresource string `json:"subresource"`
		APIGroup    string `json:"apiGroup"`
		Name        string `json:"name"`
	} `json:"objectRef"`
	ResponseStatus *struct {
		Code int `json:"code"`
	} `json:"responseStatus"`
	Received time.Time `json:"requestReceivedTimestamp"`
}

// events reads the product's completed requests of the window [start, end] from the audit
// log after the window's offset.
func (a audit) events(t *testing.T, start, end time.Time) []auditEvent {
	t.Helper()
	out, err := exec.Command("docker", "exec", a.node, "tail", "-c", "+"+strconv.FormatInt(a.offset+1, 10), a.path).Output()
	if err != nil {
		t.Fatalf("audit log read: %v", err)
	}
	var evs []auditEvent
	s := bufio.NewScanner(bytes.NewReader(out))
	s.Buffer(make([]byte, 1<<20), 16<<20)
	for s.Scan() {
		var e auditEvent
		if json.Unmarshal(s.Bytes(), &e) != nil || e.Stage != "ResponseComplete" || !strings.Contains(e.UserAgent, productAgent) {
			continue
		}
		if e.Received.Before(start) || e.Received.After(end) {
			continue
		}
		evs = append(evs, e)
	}
	return evs
}

// g7 checks the window of one graph call (G7): the product identity's requests, at least
// callK of them, verbs get and list only, a list of core secrets or configmaps only at the
// collection path of the request namespace ns and at most once each (FA2-T7a 8 edit 2), a
// get of a core Secret only for a name in secrets and a get of a core ConfigMap only for a
// name in configMaps (an owner reference that names it; FA2-T6 11b-E1 item 4, T6-E1-CM). A
// name in one set never allows a get of the other kind. It prints the matched user and the
// count per verb and resource.
func g7(t *testing.T, label string, p *panel, a audit, start, end time.Time, ns string, configMaps []string, secrets ...string) {
	t.Helper()
	var evs []auditEvent
	for i := 0; i < 15; i++ {
		evs = a.events(t, start, end)
		if len(evs) >= callK {
			time.Sleep(200 * time.Millisecond)
			if again := a.events(t, start, end); len(again) == len(evs) {
				break
			}
			continue
		}
		time.Sleep(200 * time.Millisecond)
	}
	users, verbs, per := map[string]int{}, map[string]int{}, map[string]int{}
	var problems []string
	allowed := map[string]bool{}
	for _, s := range secrets {
		allowed[s] = true
	}
	var secretGets []string
	allowedCM := map[string]bool{}
	for _, s := range configMaps {
		allowedCM[s] = true
	}
	var configMapGets []string
	lists := map[string]int{}
	// collectionList checks one list of core resource: only at the request namespace's
	// collection path, at most once per call.
	collectionList := func(e auditEvent, resource string) {
		path, _, _ := strings.Cut(e.RequestURI, "?")
		if path != "/api/v1/namespaces/"+ns+"/"+resource {
			problems = append(problems, "list of "+resource+" at "+path)
			return
		}
		if lists[resource]++; lists[resource] > 1 {
			problems = append(problems, fmt.Sprintf("%d lists of %s", lists[resource], resource))
		}
	}
	for _, e := range evs {
		users[e.User.Username]++
		verbs[e.Verb]++
		res := "nonresource " + e.RequestURI
		if e.ObjectRef != nil {
			res = e.ObjectRef.APIGroup + "/" + e.ObjectRef.Resource
			if e.ObjectRef.Subresource != "" {
				res += "/" + e.ObjectRef.Subresource
				problems = append(problems, "subresource read "+res)
			}
		}
		code := 0
		if e.ResponseStatus != nil {
			code = e.ResponseStatus.Code
		}
		per[fmt.Sprintf("%s %s %d", e.Verb, res, code)]++
		if e.Verb != "get" && e.Verb != "list" {
			problems = append(problems, "verb "+e.Verb+" on "+res)
		}
		if e.ObjectRef != nil && e.ObjectRef.APIGroup == "" && e.ObjectRef.Resource == "secrets" {
			switch {
			case e.Verb == "list":
				collectionList(e, "secrets")
			case e.Verb == "get":
				secretGets = append(secretGets, e.ObjectRef.Name)
				if !allowed[e.ObjectRef.Name] {
					problems = append(problems, "get of secret "+e.ObjectRef.Name)
				}
			}
		}
		if e.ObjectRef != nil && e.ObjectRef.APIGroup == "" && e.ObjectRef.Resource == "configmaps" {
			switch {
			case e.Verb == "list":
				collectionList(e, "configmaps")
			case e.Verb == "get":
				configMapGets = append(configMapGets, e.ObjectRef.Name)
				if !allowedCM[e.ObjectRef.Name] {
					problems = append(problems, "get of configmap "+e.ObjectRef.Name)
				}
			}
		}
	}
	if len(evs) < callK {
		problems = append(problems, fmt.Sprintf("%d matched requests, fewer than K = %d", len(evs), callK))
	}
	for u := range users {
		if u != p.user {
			problems = append(problems, "user "+u)
		}
	}
	result := "PASS"
	if len(problems) > 0 {
		result = "FAIL"
	}
	t.Logf("FA2-T5 G7 input=%s user-filter=%s window=%s..%s matched=%d (K=%d) users=%v verbs=%v per-verb-resource=%v secret-gets=%v configmap-gets=%v result=%s",
		label, productAgent, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), len(evs), callK, users, verbs, per, secretGets, configMapGets, result)
	if len(problems) > 0 {
		t.Errorf("G7 %s: %s", label, strings.Join(problems, "; "))
	}
}

// graph sends one graph call for ns, checks its audit window (G7) and returns the status,
// the answer and the raw body.
func (p *panel) graph(t *testing.T, label, ns string, secrets ...string) (int, answer, []byte) {
	t.Helper()
	a := auditStart(t)
	start := time.Now()
	status, raw := p.request("GET", "/api/graph/containment?namespace="+ns, nil, true)
	end := time.Now()
	g7(t, label, p, a, start, end, ns, nil, secrets...)
	var ans answer
	json.Unmarshal(raw, &ans)
	t.Logf("FA2-T5 call input=%s namespace=%s status=%d nodes=%d refused=%d", label, ns, status, len(ans.Nodes), len(ans.Refused))
	return status, ans, raw
}

// graphOwners does what graph does, with the names of the ConfigMaps that an owner
// reference of the namespace names (T6-E1-CM): G7 allows a get of those ConfigMaps only.
func (p *panel) graphOwners(t *testing.T, label, ns string, configMaps []string, secrets ...string) (int, answer, []byte) {
	t.Helper()
	a := auditStart(t)
	start := time.Now()
	status, raw := p.request("GET", "/api/graph/containment?namespace="+ns, nil, true)
	end := time.Now()
	g7(t, label, p, a, start, end, ns, configMaps, secrets...)
	var ans answer
	json.Unmarshal(raw, &ans)
	t.Logf("FA2-T5 call input=%s namespace=%s status=%d nodes=%d refused=%d", label, ns, status, len(ans.Nodes), len(ans.Refused))
	return status, ans, raw
}

// want is the expected place of one object; parent is "<kind>/<name>" or "-".
type want struct {
	kind, name, parent, state, text string
}

// printable writes U+2014 as its escape, so the log stays ASCII.
func printable(s string) string { return strings.ReplaceAll(s, "\u2014", `\u2014`) }

// check compares each expected object with the answer and prints its line:
// FA2-T5 <criterion> input=<fixture> expected=<kind/name parent state> actual=<same> result=<PASS|FAIL>.
func check(t *testing.T, criterion, input string, ans answer, ws ...want) {
	t.Helper()
	byUID := map[string]node{}
	for _, n := range ans.Nodes {
		byUID[n.UID] = n
	}
	for _, w := range ws {
		actual, text, found := "absent", "", false
		for _, n := range ans.Nodes {
			if n.Kind != w.kind || n.Name != w.name {
				continue
			}
			parent := "-"
			if n.Parent != nil {
				parent = "uid:" + *n.Parent
				if pn, ok := byUID[*n.Parent]; ok {
					parent = pn.Kind + "/" + pn.Name
				}
			}
			actual, text, found = fmt.Sprintf("%s/%s %s %s", w.kind, w.name, parent, n.Owner.State), n.Owner.Text, true
		}
		expected := fmt.Sprintf("%s/%s %s %s", w.kind, w.name, w.parent, w.state)
		pass := found && actual == expected && text == w.text
		line := fmt.Sprintf("FA2-T5 %s input=%s expected=%s actual=%s", criterion, input, expected, actual)
		if w.text != "" || text != "" {
			line += fmt.Sprintf(" expected-text=%s actual-text=%s", printable(w.text), printable(text))
		}
		result := "PASS"
		if !pass {
			result = "FAIL"
		}
		t.Logf("%s result=%s", line, result)
		if !pass {
			t.Errorf("FA2-T5 %s %s: got %s text %q", criterion, expected, actual, text)
		}
	}
}

// kubeconfig writes a kubeconfig for the kind cluster with one context and user.
func kubeconfig(t *testing.T, ctxName, server, caData string, user map[string]any) string {
	t.Helper()
	cluster := map[string]any{"server": server}
	if caData != "" {
		cluster["certificate-authority-data"] = caData
	}
	doc := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": ctxName,
		"clusters": []any{map[string]any{"name": ctxName + "-cluster", "cluster": cluster}},
		"users":    []any{map[string]any{"name": ctxName + "-user", "user": user}},
		"contexts": []any{map[string]any{"name": ctxName, "context": map[string]any{"cluster": ctxName + "-cluster", "user": ctxName + "-user"}}}}
	raw, _ := json.Marshal(doc)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// adminUser is the user name of the kind admin: the common name of its client
// certificate, which the API server's audit log records.
func adminUser(t *testing.T) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(kindCluster(t).certData)
	block, _ := pem.Decode(raw)
	if err != nil || block == nil {
		t.Fatalf("the admin client certificate cannot be read (%v)", err)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("the admin client certificate cannot be parsed: %v", err)
	}
	return cert.Subject.CommonName
}

// adminPanel starts the release binary as the kind admin and waits for connected.
func adminPanel(t *testing.T) *panel {
	t.Helper()
	ki := kindCluster(t)
	kc := kubeconfig(t, "kind-graph", ki.server, ki.caData, map[string]any{"client-certificate-data": ki.certData, "client-key-data": ki.keyData})
	p := startPanel(t, kc, adminUser(t))
	if o := p.settle(); o.State != "connected" {
		t.Fatalf("preparation: the admin context did not connect: %s %q", o.State, str(o.Message))
	}
	return p
}

// saPanel starts the release binary as ServiceAccount sa of namespace saNS and waits for
// connected.
func saPanel(t *testing.T, saNS, sa string) *panel {
	t.Helper()
	ki := kindCluster(t)
	token := strings.TrimSpace(string(kubectl(t, nil, "create", "token", sa, "-n", saNS, "--duration", "1h")))
	kc := kubeconfig(t, "sa-"+sa, ki.server, ki.caData, map[string]any{"token": token})
	p := startPanel(t, kc, "system:serviceaccount:"+saNS+":"+sa)
	if o := p.settle(); o.State != "connected" {
		t.Fatalf("preparation: the ServiceAccount %s did not connect: %s %q", sa, o.State, str(o.Message))
	}
	return p
}

// expectedOwner is the expected parent of an object from its controller reference, as
// kubectl reads it.
func expectedOwner(o object) string {
	for _, r := range o.Metadata.OwnerReferences {
		if r.Controller != nil && *r.Controller {
			return r.Kind + "/" + r.Name
		}
	}
	if len(o.Metadata.OwnerReferences) > 0 {
		r := o.Metadata.OwnerReferences[0]
		return r.Kind + "/" + r.Name
	}
	return "-"
}
