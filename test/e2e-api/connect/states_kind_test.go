//go:build kind

package connect

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixtureRun is one prepared fixture: the KUBECONFIG value, the expected text and how
// the panel then reaches the kind context.
type fixtureRun struct {
	kubeconfig *string
	message    string                // the exact expected text, or
	check      func(m string) error  // for K-IV1, whose <error> is the parser's message
	stdout     bool                  // K-OK goes to stdout
	recover    func(p *panel) string // brings the panel to the kind context; names how
}

// stateFixture is one fixture per state and text of section 3.2.
type stateFixture struct {
	name  string
	state string
	id    string
	setup func(t *testing.T, dir string) fixtureRun
}

func byRewrite(t *testing.T, path string) func(p *panel) string {
	return func(p *panel) string {
		writeFile(t, path, (&kube{current: "kind-ok"}).withKind(t).bytes())
		if r := p.retry(); r.status != 202 {
			t.Errorf("retry: status %d", r.status)
		}
		return "kubeconfig rewritten with the kind context, then retry"
	}
}

func bySwitch(t *testing.T) func(p *panel) string {
	return func(p *panel) string {
		if r := p.switchTo("kind-ok"); r.status != 202 {
			t.Errorf("switch to kind-ok: status %d body %q", r.status, r.body)
		}
		return "switch to kind-ok"
	}
}

// otherCA is a certificate authority made at run time that did not sign the kind
// API server's certificate.
func otherCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "intuitik-other-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// tlsMismatch is the TLS verification message that a client trusting only caData gets
// from the kind API server.
func tlsMismatch(t *testing.T, caData string) string {
	t.Helper()
	ca, _ := base64.StdEncoding.DecodeString(caData)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	host := strings.TrimPrefix(kindCluster(t).server, "https://")
	c, err := tls.Dial("tcp", host, &tls.Config{RootCAs: pool, ServerName: strings.Split(host, ":")[0]})
	if err == nil {
		c.Close()
		t.Fatal("the kind API server was trusted with the other CA")
	}
	var cve *tls.CertificateVerificationError
	if !errors.As(err, &cve) {
		t.Fatalf("TLS dial with the other CA: %v", err)
	}
	return cve.Err.Error()
}

// credentialProgram writes the exec credential program of the K-EX fixtures. Started,
// it would create the marker file.
func credentialProgram(t *testing.T, dir string) (program, marker string) {
	t.Helper()
	marker = filepath.Join(dir, "credential-program-ran")
	program = writeFile(t, filepath.Join(dir, "credential-program"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"+
		`printf '%s\n' '{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{}}'`+"\n"))
	if err := os.Chmod(program, 0o700); err != nil {
		t.Fatal(err)
	}
	return program, marker
}

func execUser(program string) map[string]any {
	_, arg, env := canaries()
	return map[string]any{"exec": map[string]any{
		"apiVersion":      "client.authentication.k8s.io/v1",
		"command":         program,
		"args":            []any{arg},
		"env":             []any{map[string]any{"name": "INTUITIK_CANARY_ENV", "value": env}},
		"interactiveMode": "Never",
	}}
}

// statusServer is a local TLS test server that answers every request with status.
func statusServer(t *testing.T, status int) (url, caData string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return srv.URL, base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

func stateFixtures() []stateFixture {
	single := func(t *testing.T, dir string, k *kube) *string {
		return strPtr(writeFile(t, filepath.Join(dir, "kubeconfig"), k.bytes()))
	}
	return []stateFixture{
		{"current context connects", "connected", "K-OK", func(t *testing.T, dir string) fixtureRun {
			kc := single(t, dir, (&kube{current: "kind-ok"}).withKind(t))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textOK, "kind-c", "kind-ok"), stdout: true}
		}},
		{"no kubeconfig", "no-kubeconfig", "K-NK", func(t *testing.T, dir string) fixtureRun {
			path := filepath.Join(dir, "missing", "kubeconfig")
			return fixtureRun{kubeconfig: strPtr(path), message: fmt.Sprintf(textNK, path), recover: byRewrite(t, path)}
		}},
		{"malformed kubeconfig", "kubeconfig-invalid", "K-IV1", func(t *testing.T, dir string) fixtureRun {
			path := writeFile(t, filepath.Join(dir, "kubeconfig"), []byte("apiVersion: v1\nkind: Config\ncontexts: [\n"))
			prefix, suffix := fmt.Sprintf("Cannot read the kubeconfig %s: ", path), ". Fix the file, or set KUBECONFIG to a valid kubeconfig file."
			return fixtureRun{kubeconfig: strPtr(path), check: func(m string) error {
				middle, ok := strings.CutPrefix(m, prefix)
				if ok {
					middle, ok = strings.CutSuffix(middle, suffix)
				}
				if !ok || middle == "" || strings.ContainsAny(middle, "\r\n") {
					return fmt.Errorf("message %q is not %q<parser error>%q", m, prefix, suffix)
				}
				return nil
			}, recover: byRewrite(t, path)}
		}},
		{"no current context", "kubeconfig-invalid", "K-IV2", func(t *testing.T, dir string) fixtureRun {
			kc := single(t, dir, (&kube{}).withKind(t))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textIV2, "kind-ok"), recover: bySwitch(t)}
		}},
		{"context names a missing cluster", "kubeconfig-invalid", "K-IV3", func(t *testing.T, dir string) fixtureRun {
			kc := single(t, dir, (&kube{current: "broken"}).withKind(t).context("broken", "no-such-cluster", "kind-admin"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textIV3, "broken"), recover: bySwitch(t)}
		}},
		{"kubeconfig with no context", "kubeconfig-invalid", "K-IV4", func(t *testing.T, dir string) fixtureRun {
			ki := kindCluster(t)
			path := writeFile(t, filepath.Join(dir, "kubeconfig"), (&kube{}).cluster("kind-c", ki.server, ki.caData).user("kind-admin", map[string]any{}).bytes())
			return fixtureRun{kubeconfig: strPtr(path), message: fmt.Sprintf(textIV4, path), recover: byRewrite(t, path)}
		}},
		{"rejected credentials", "credentials-rejected", "K-AU", func(t *testing.T, dir string) fixtureRun {
			token, _, _ := canaries()
			kc := single(t, dir, (&kube{current: "bad-token"}).withKind(t).user("bad", tokenUser(token)).context("bad-token", "kind-c", "bad"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textAU, "kind-c", "bad-token"), recover: bySwitch(t)}
		}},
		{"no credentials", "credentials-missing", "K-NC", func(t *testing.T, dir string) fixtureRun {
			kc := single(t, dir, (&kube{current: "no-creds"}).withKind(t).user("nobody", map[string]any{}).context("no-creds", "kind-c", "nobody"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textNC, "no-creds", "kind-c"), recover: bySwitch(t)}
		}},
		{"credential program not run", "credential-program-unsupported", "K-EX", func(t *testing.T, dir string) fixtureRun {
			program, _ := credentialProgram(t, dir)
			kc := single(t, dir, (&kube{current: "exec-ctx"}).withKind(t).user("exec-user", execUser(program)).context("exec-ctx", "kind-c", "exec-user"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textEX, "exec-ctx", program), recover: bySwitch(t)}
		}},
		{"unsupported auth provider", "auth-provider-unsupported", "K-AP", func(t *testing.T, dir string) fixtureRun {
			kc := single(t, dir, (&kube{current: "gcp-ctx"}).withKind(t).user("gcp-user", map[string]any{"auth-provider": map[string]any{"name": "gcp"}}).context("gcp-ctx", "kind-c", "gcp-user"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textAP, "gcp-ctx", "gcp"), recover: bySwitch(t)}
		}},
		{"certificate mismatch", "certificate-mismatch", "K-TL", func(t *testing.T, dir string) fixtureRun {
			ca := otherCA(t)
			ki := kindCluster(t)
			kc := single(t, dir, (&kube{current: "wrong-ca"}).withKind(t).cluster("other-ca", ki.server, ca).context("wrong-ca", "other-ca", "kind-admin"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textTL, ki.server, "wrong-ca", tlsMismatch(t, ca)), recover: bySwitch(t)}
		}},
		{"unreachable closed port", "unreachable", "K-UR", func(t *testing.T, dir string) fixtureRun {
			port, osErr := closedPort(t)
			server := "https://127.0.0.1:" + strconv.Itoa(port)
			kc := single(t, dir, (&kube{current: "closed"}).withKind(t).cluster("closed-c", server, kindCluster(t).caData).context("closed", "closed-c", "kind-admin"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textUR, server, "closed", osErr), recover: bySwitch(t)}
		}},
		{"HTTP status other than the API", "unreachable", "K-HS", func(t *testing.T, dir string) fixtureRun {
			url, ca := statusServer(t, http.StatusInternalServerError)
			kc := single(t, dir, (&kube{current: "busy"}).withKind(t).cluster("busy-c", url, ca).user("busy-user", tokenUser("intuitik-"+randomHex(8))).context("busy", "busy-c", "busy-user"))
			return fixtureRun{kubeconfig: kc, message: fmt.Sprintf(textHS, url, "busy", 500), recover: bySwitch(t)}
		}},
	}
}

// checkText compares the state, text id and message of the cluster object with the
// fixture and prints expected and actual.
func checkText(t *testing.T, label string, f stateFixture, run fixtureRun, o clusterObj) bool {
	t.Helper()
	t.Logf("%s %s: expected state=%s text_id=%s, actual state=%s text_id=%s", label, f.name, f.state, f.id, o.State, str(o.TextID))
	ok := true
	if o.State != f.state || str(o.TextID) != f.id {
		t.Errorf("%s %s: state %q text_id %q, want %q %q", label, f.name, o.State, str(o.TextID), f.state, f.id)
		ok = false
	}
	switch {
	case run.check != nil:
		if err := run.check(str(o.Message)); err != nil {
			t.Errorf("%s %s: %v", label, f.name, err)
			ok = false
		}
	case str(o.Message) != run.message:
		t.Errorf("%s %s: message\n  actual   %q\n  expected %q", label, f.name, str(o.Message), run.message)
		ok = false
	}
	return ok
}

// TestK_States (packet sections 3.2, 3.3 and 6.6): one fixture per state and text. Each
// checks the state, the text id, the exact API message, the exact terminal line once
// per attempt, that the process keeps running, and then reaches the kind context.
func TestK_States(t *testing.T) {
	for _, f := range stateFixtures() {
		t.Run(f.id, func(t *testing.T) {
			run := f.setup(t, t.TempDir())
			p := startPanel(t, panelOpts{kubeconfig: run.kubeconfig})
			o := p.mustSettle()
			checkText(t, "K-STATE", f, run, o)
			stream, name := p.stderr, "stderr"
			if run.stdout {
				stream, name = p.stdout, "stdout"
			}
			line := str(o.Message)
			if n := waitCount(stream, line, 1); n != 1 {
				t.Errorf("K-STATE %s: the text is on %s %d times, want once; %s %q", f.name, name, n, name, stream.list())
			} else {
				t.Logf("K-STATE %s: the text is on %s once", f.name, name)
			}
			if err := p.running(); err != nil {
				t.Errorf("K-STATE %s: after the failure: %v", f.name, err)
			}
			if run.recover == nil {
				return
			}
			how := run.recover(p)
			after := p.mustSettle()
			t.Logf("K-STATE %s: %s: state %s text_id %s", f.name, how, after.State, str(after.TextID))
			if after.State != "connected" {
				t.Errorf("K-STATE %s: after %s the state is %q, want connected (message %q)", f.name, how, after.State, str(after.Message))
			}
		})
	}
}

// shapeProblems checks the field names and JSON types of a cluster object (section 6.1).
func shapeProblems(body []byte, connecting bool) []string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return []string{fmt.Sprintf("body %q is not a JSON object", body)}
	}
	want := []string{"cluster", "context", "contexts", "kubeconfig_paths", "message", "server", "state", "text_id"}
	var problems []string
	if got := sortedKeys(m); !reflect.DeepEqual(got, want) {
		problems = append(problems, fmt.Sprintf("fields %v, want %v", got, want))
	}
	isString := func(v any) bool { _, ok := v.(string); return ok }
	if !isString(m["state"]) {
		problems = append(problems, "state is not a string")
	}
	for _, k := range []string{"text_id", "message"} {
		if connecting && m[k] != nil {
			problems = append(problems, k+" is not null while connecting")
		}
		if !connecting && !isString(m[k]) {
			problems = append(problems, k+" is not a string")
		}
	}
	for _, k := range []string{"context", "cluster", "server"} {
		if m[k] != nil && !isString(m[k]) {
			problems = append(problems, k+" is neither a string nor null")
		}
	}
	paths, ok := m["kubeconfig_paths"].([]any)
	if !ok {
		problems = append(problems, "kubeconfig_paths is not an array")
	}
	for _, p := range paths {
		if !isString(p) {
			problems = append(problems, "kubeconfig_paths holds a non-string")
		}
	}
	list, ok := m["contexts"].([]any)
	if !ok {
		problems = append(problems, "contexts is not an array")
	}
	for _, c := range list {
		e, ok := c.(map[string]any)
		if !ok {
			problems = append(problems, "a contexts entry is not an object")
			continue
		}
		if got := sortedKeys(e); !reflect.DeepEqual(got, []string{"active", "cluster", "current", "name", "server"}) {
			problems = append(problems, fmt.Sprintf("contexts entry fields %v", got))
		}
		for _, k := range []string{"name", "cluster", "server"} {
			if !isString(e[k]) {
				problems = append(problems, "contexts entry "+k+" is not a string")
			}
		}
		for _, k := range []string{"current", "active"} {
			if _, ok := e[k].(bool); !ok {
				problems = append(problems, "contexts entry "+k+" is not a boolean")
			}
		}
	}
	return problems
}

// TestK_APIShape (packet section 6.1): the field names and types of the cluster object
// in every state of 3.2, and of the 202 answer of a retry (state connecting).
func TestK_APIShape(t *testing.T) {
	for _, f := range stateFixtures() {
		t.Run(f.id, func(t *testing.T) {
			run := f.setup(t, t.TempDir())
			p := startPanel(t, panelOpts{kubeconfig: run.kubeconfig})
			o := p.mustSettle()
			_, r := p.cluster()
			problems := shapeProblems(r.body, false)
			if o.State != f.state {
				problems = append(problems, fmt.Sprintf("state %q, want %q", o.State, f.state))
			}
			retry := p.retry()
			if retry.status != 202 {
				problems = append(problems, fmt.Sprintf("retry answered %d, want 202", retry.status))
			} else {
				var ro clusterObj
				json.Unmarshal(retry.body, &ro)
				if ro.State != "connecting" {
					problems = append(problems, fmt.Sprintf("retry answer state %q, want connecting", ro.State))
				}
				for _, pr := range shapeProblems(retry.body, true) {
					problems = append(problems, "retry answer: "+pr)
				}
			}
			for _, pr := range problems {
				t.Errorf("K-SHAPE %s: %s", f.name, pr)
			}
			if len(problems) == 0 {
				t.Logf("K-SHAPE %s: state %s and the connecting answer have the fields and types of section 6.1", f.name, f.state)
			}
		})
	}
}
