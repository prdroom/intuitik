package readonly

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prdroom/intuitik/internal/kube/credexec"
)

// Fakes of the M1-T3b tests: a TLS API server that records the credential of every
// request, runners that answer from a list or run the fake program through credexec,
// and certificate pairs made at run time.

// credRecord is what the fake server received with one request: the credential kind and
// the credential itself, kept only inside the test and never printed.
type credRecord struct {
	auth   string // the Authorization header
	certCN string // the common name of the client certificate, "" without one
	path   string
}

// kind names the credential of a record for the test log, never its value.
func (r credRecord) kind() string {
	switch {
	case r.auth != "" && r.certCN != "":
		return "authorization header and client certificate " + r.certCN
	case strings.HasPrefix(r.auth, "Bearer "):
		return "bearer token"
	case strings.HasPrefix(r.auth, "Basic "):
		return "basic auth"
	case r.certCN != "":
		return "client certificate " + r.certCN
	}
	return "none"
}

// credServer is a TLS fake API server. It answers 401 to a request whose bearer token or
// client certificate common name is rejected, and 200 to every other request.
type credServer struct {
	srv        *httptest.Server
	mu         sync.Mutex
	records    []credRecord
	rejected   map[string]bool // "Bearer <token>" or "cn:<name>"
	handshakes atomic.Int64
}

func newCredServer(t *testing.T) *credServer {
	t.Helper()
	s := &credServer{rejected: map[string]bool{}}
	s.srv = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	s.srv.Config.ConnState = func(c net.Conn, st http.ConnState) {
		if st == http.StateNew {
			s.handshakes.Add(1)
		}
	}
	s.srv.StartTLS()
	t.Cleanup(s.srv.Close)
	return s
}

func (s *credServer) serve(w http.ResponseWriter, r *http.Request) {
	rec := credRecord{auth: r.Header.Get("Authorization"), path: r.URL.Path}
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		rec.certCN = r.TLS.PeerCertificates[0].Subject.CommonName
	}
	s.mu.Lock()
	s.records = append(s.records, rec)
	reject := s.rejected[rec.auth] || (rec.certCN != "" && s.rejected["cn:"+rec.certCN])
	s.mu.Unlock()
	if reject {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
}

func (s *credServer) reject(keys ...string) {
	s.mu.Lock()
	for _, k := range keys {
		s.rejected[k] = true
	}
	s.mu.Unlock()
}

func (s *credServer) seen() []credRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]credRecord(nil), s.records...)
}

func (s *credServer) caPEM() []byte { return certPEM(s.srv.Certificate().Raw) }

// certPair is a client certificate and key in PEM form with the common name cn.
func certPair(t *testing.T, cn string) (certData, keyData []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

// execOutput is the stdout of a program that returns a credential.
func execOutput(version string, status map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": status})
	return raw
}

// listRunner answers the runs of a generation from a list, one entry per run; after the
// list it repeats the last entry. Each answer waits delay first. It starts no process:
// its start is an empty function passed through admit, so that the E5 gate still
// decides.
type listRunner struct {
	mu      sync.Mutex
	answers []RunResult
	delay   time.Duration
	runs    int
}

func (l *listRunner) run(ctx context.Context, p Program, admit func(func() error) error) RunResult {
	if err := admit(func() error { return nil }); err != nil {
		return RunResult{Closed: true}
	}
	l.mu.Lock()
	l.runs++
	i := l.runs - 1
	if i >= len(l.answers) {
		i = len(l.answers) - 1
	}
	answer, delay := l.answers[i], l.delay
	l.mu.Unlock()
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return RunResult{Class: "timeout"}
	}
	return answer
}

func (l *listRunner) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.runs
}

// changes records the state changes after connected that a generation reports.
type changes struct {
	mu   sync.Mutex
	list []Outcome
}

func (c *changes) add(o Outcome) {
	c.mu.Lock()
	c.list = append(c.list, o)
	c.mu.Unlock()
}

func (c *changes) get() []Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Outcome(nil), c.list...)
}

// execUser is a kubeconfig user with an exec entry of the fake program.
func execUser(command string, env map[string]string, extra map[string]any) map[string]any {
	var list []any
	for k, v := range env {
		list = append(list, map[string]any{"name": k, "value": v})
	}
	e := map[string]any{"apiVersion": execV1, "command": command, "interactiveMode": "Never", "env": list}
	u := map[string]any{"exec": e}
	for k, v := range extra {
		u[k] = v
	}
	return u
}

// read sends one read after connected through the gate's client constructor.
func read(t *testing.T, o Outcome) (int, error) {
	t.Helper()
	c, err := NewClient(o)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.Get(ctx, "/api")
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// The fake program (internal/kube/credexec/testdata/credfake), built once per test
// binary, and a copy under another name as the descendant executable.
var (
	fakeOnce  sync.Once
	fakeProg  string
	fakeChild string
	fakeErr   error
)

func fakeProgram(t *testing.T) (program, child string) {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "credfake-")
		if err != nil {
			fakeErr = err
			return
		}
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		fakeProg = filepath.Join(dir, "credfake"+ext)
		out, err := exec.Command("go", "build", "-o", fakeProg, "../credexec/testdata/credfake").CombinedOutput()
		if err != nil {
			fakeErr = fmt.Errorf("go build credfake: %v: %s", err, out)
			return
		}
		raw, err := os.ReadFile(fakeProg)
		if err != nil {
			fakeErr = err
			return
		}
		fakeChild = filepath.Join(dir, "credchild"+ext)
		fakeErr = os.WriteFile(fakeChild, raw, 0o700)
	})
	if fakeErr != nil {
		t.Fatalf("fake program: %v", fakeErr)
	}
	return fakeProg, fakeChild
}

// fakeEnv is the environment of a fake program run that records into dir.
func fakeEnv(t *testing.T, dir string, plans ...string) map[string]string {
	_, child := fakeProgram(t)
	env := map[string]string{"CREDFAKE_DIR": dir, "CREDFAKE_CHILD": child}
	for i, p := range plans {
		env["CREDFAKE_RUN"+strconv.Itoa(i+1)] = p
	}
	if len(plans) > 0 {
		env["CREDFAKE_RUN"] = plans[len(plans)-1]
	}
	return env
}

// fakeStarts counts the recorded starts of the fake program in dir.
func fakeStarts(dir string) int {
	raw, _ := os.ReadFile(filepath.Join(dir, "starts"))
	return strings.Count(string(raw), "\n")
}

// credexecRunner runs programs through credexec, as internal/server does, with helper
// and helperEnv for the Windows helper entry.
func credexecRunner(helper string, helperEnv []string) Runner {
	return func(ctx context.Context, p Program, admit func(func() error) error) RunResult {
		r := credexec.Run(ctx, credexec.Spec{Command: p.Command, Args: p.Args, Env: p.Env, Stderr: os.Stderr, Helper: helper, HelperEnv: helperEnv}, func(start func() error) error {
			started := false
			err := admit(func() error {
				started = true
				return start()
			})
			if err != nil && !started {
				return credexec.ErrClosed
			}
			return err
		})
		return RunResult{Class: r.Class, Code: r.Code, Stdout: r.Stdout, Closed: r.Closed}
	}
}

// helperBinary is the intuitik binary whose helper entry runs programs on Windows; ""
// elsewhere.
func helperBinary(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return ""
	}
	bin := os.Getenv("INTUITIK_E2E_BINARY")
	if bin == "" {
		t.Fatal("INTUITIK_E2E_BINARY is not set")
	}
	return bin
}
