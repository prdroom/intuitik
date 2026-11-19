//go:build kind

// Package appendixa holds the kind test of packet FA2-T7b (job appendix-a): the six
// Appendix A faults, the negative stage and the healthy baseline A-0 built on the job's own
// kind cluster (INTUITIK_E2E_KIND_KUBECONFIG, kind's default configuration), read through
// the graph routes of the release binary (INTUITIK_E2E_BINARY) and judged by
// tools/hk2judge. The harness is the pattern of test/e2e-api/graph/harness_kind_test.go,
// copied because test files of another package cannot be imported. Every kubeconfig is
// made at run time, so no credential is committed.
package appendixa

import (
	"bytes"
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

var listenLine = regexp.MustCompile(`^listening on http://127\.0\.0\.1:([0-9]+)`)

// kindInfo is the kind cluster of the job, read once from its kubeconfig.
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
		t.Fatalf("HK2 verdict=ERROR kind cluster: %v", kindErr)
	}
	return kind
}

// kubectl runs kubectl with the kind admin kubeconfig; an error is a preparation failure.
func kubectl(t *testing.T, args ...string) []byte {
	t.Helper()
	kindCluster(t)
	out, err := kubectlErr(args...)
	if err != nil {
		t.Fatalf("HK2 verdict=ERROR preparation: kubectl %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func kubectlErr(args ...string) ([]byte, error) {
	cmd := exec.Command("kubectl", append([]string{"--kubeconfig", kind.kubeconfig}, args...)...)
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

// panel is one running release binary, logged in with one session.
type panel struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdout *lines
	stderr *lines
	origin string
	sess   string
}

// startPanel starts the release binary on kubeconfig and logs in.
func startPanel(t *testing.T, kubeconfig string) *panel {
	t.Helper()
	bin := os.Getenv("INTUITIK_E2E_BINARY")
	if bin == "" {
		t.Fatal("HK2 verdict=ERROR INTUITIK_E2E_BINARY is not set")
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
	p := &panel{t: t, stdout: &lines{}, stderr: &lines{}}
	p.cmd = exec.Command(bin)
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.stdout, p.stderr
	p.cmd.WaitDelay = 5 * time.Second
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("HK2 verdict=ERROR starting %s: %v", bin, err)
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
			t.Fatalf("HK2 verdict=ERROR no listen line within 30 s; stdout %q, stderr %q", p.stdout.list(), p.stderr.list())
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
			t.Fatal("HK2 verdict=ERROR the opener received no login address")
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]string{"t": token})
	status, raw := p.request("POST", "/api/session", body, false)
	var v struct{ Session string }
	if status != 200 || json.Unmarshal(raw, &v) != nil || v.Session == "" {
		t.Fatalf("HK2 verdict=ERROR login exchange: status %d", status)
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
		p.t.Fatalf("HK2 verdict=ERROR %s %s: %v", method, path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, b
}

// settle polls GET /api/cluster until the state is not connecting (W1 10 s + 5 s).
func (p *panel) settle() string {
	p.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		status, raw := p.request("GET", "/api/cluster", nil, true)
		var o struct {
			State string `json:"state"`
		}
		if status == 200 && json.Unmarshal(raw, &o) == nil && o.State != "connecting" && o.State != "" {
			return o.State
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("HK2 verdict=ERROR the state stayed %q; stderr %q", o.State, p.stderr.list())
		}
		time.Sleep(200 * time.Millisecond)
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

// adminPanel starts the release binary as the kind admin and waits for connected.
func adminPanel(t *testing.T) *panel {
	t.Helper()
	ki := kindCluster(t)
	kc := kubeconfig(t, "kind-appendix", ki.server, ki.caData, map[string]any{"client-certificate-data": ki.certData, "client-key-data": ki.keyData})
	p := startPanel(t, kc)
	if s := p.settle(); s != "connected" {
		t.Fatalf("HK2 verdict=ERROR preparation: the admin context did not connect: %s", s)
	}
	return p
}
