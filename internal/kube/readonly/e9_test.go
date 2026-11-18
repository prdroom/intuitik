package readonly

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	clientexec "k8s.io/client-go/plugin/pkg/client/auth/exec"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/transport"
)

// TestE9_OutputValidation (packet M1-T3b E9): a run is ok only for an ExecCredential of
// the entry's apiVersion whose status holds a token, a valid certificate-and-key pair, or
// both. One subtest per bad form; each must give K-EP "no valid credential in its output"
// with no part of the output in the text. Output over 1 MiB is invalid and the program
// tree is ended.
func TestE9_OutputValidation(t *testing.T) {
	canary := "e9-canary-" + randomValue(t, "")
	cert, key := certPair(t, "e9-cert")
	other, _ := certPair(t, "e9-other")
	_, otherKey := certPair(t, "e9-other-key")
	for _, c := range []struct {
		name string
		out  []byte
	}{
		{"empty output", nil},
		{"not JSON or YAML", []byte("{" + canary)},
		{"wrong kind", []byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"` + canary + `","status":{"token":"` + canary + `"}}`)},
		{"wrong apiVersion", execOutput(execV1beta1, map[string]any{"token": canary})},
		{"no apiVersion", []byte(`{"kind":"ExecCredential","status":{"token":"` + canary + `"}}`)},
		{"no status", []byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"x":"` + canary + `"}}`)},
		{"status without a credential", execOutput(execV1, map[string]any{"expirationTimestamp": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})},
		{"certificate without key", execOutput(execV1, map[string]any{"clientCertificateData": string(cert)})},
		{"key without certificate", execOutput(execV1, map[string]any{"clientKeyData": string(key)})},
		{"token and certificate without key", execOutput(execV1, map[string]any{"token": canary, "clientCertificateData": string(cert)})},
		{"certificate and a key of another pair", execOutput(execV1, map[string]any{"clientCertificateData": string(other), "clientKeyData": string(otherKey)})},
		{"token as a number", []byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":42}}`)},
		{"field names in another case", []byte(`{"APIVERSION":"client.authentication.k8s.io/v1","KIND":"ExecCredential","STATUS":{"TOKEN":"` + canary + `"}}`)},
		{"output over 1 MiB", append(execOutput(execV1, map[string]any{"token": canary}), bytes.Repeat([]byte(" "), execOutputLimit)...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			runner := &listRunner{answers: []RunResult{{Class: "ok", Stdout: c.out}}}
			g := NewGeneration(context.Background(), runner.run, nil)
			defer g.Close()
			g.setProgram(Program{Command: "/bin/e9-program"}, execV1, "/bin/e9-program")
			got := g.current(context.Background())
			line := kEP("/bin/e9-program", "e9-context", got.fail)
			found := strings.Count(line, canary)
			t.Logf("E9 %s: credential %v reason %q; canary in the K-EP text %d", c.name, got.cred != nil, got.fail, found)
			if got.cred != nil || got.fail != "no valid credential in its output" {
				t.Errorf("E9 %s: credential %v reason %q, want no credential and %q", c.name, got.cred != nil, got.fail, "no valid credential in its output")
			}
			if found != 0 || strings.Contains(line, "{") {
				t.Errorf("E9 %s: the K-EP text holds program output: %q", c.name, line)
			}
		})
	}
	t.Run("2 MiB of output with the tree ended", func(t *testing.T) {
		got, _, dir := runClass(t, classCase{plan: "child=holdpipe;bytes=2097152", deadline: 20 * time.Second})
		pids := recordedPIDs(dir)
		alive, took := waitGoneSince(pids, time.Now(), 2*time.Second)
		t.Logf("E9 2 MiB: credential %v reason %q; recorded PIDs %v, alive %v %s after the run", got.cred != nil, got.fail, pids, alive, took.Round(time.Millisecond))
		if got.cred != nil || got.fail != "no valid credential in its output" {
			t.Errorf("E9 2 MiB: credential %v reason %q, want %q", got.cred != nil, got.fail, "no valid credential in its output")
		}
		if len(pids) < 2 || len(alive) != 0 {
			t.Errorf("E9 2 MiB: recorded PIDs %v, alive %v; want the program and its descendant, both ended", pids, alive)
		}
	})
	// Positive control: the same forms with a valid credential are accepted.
	for _, out := range [][]byte{
		execOutput(execV1, map[string]any{"token": canary}),
		execOutput(execV1, map[string]any{"clientCertificateData": string(cert), "clientKeyData": string(key)}),
		[]byte("apiVersion: client.authentication.k8s.io/v1\nkind: ExecCredential\nstatus:\n  token: " + canary + "\n"),
	} {
		if c, ok := parseCredential(out, execV1); !ok || c == nil {
			t.Errorf("E9 positive control: a valid credential (%d bytes) was refused", len(out))
		}
	}
}

// protocolRow is one row of the D5 table.
type protocolRow struct {
	id, version     string
	clusterInfo     bool
	insecure        bool
	plan            func(t *testing.T, dir string) string
	ok              bool
	wantToken, cert bool
}

// TestE9_ProtocolMatchesClientGo (packet M1-T3b D5, E9): for every row of the D5 table,
// intuitik and client-go v0.37.2's own exec authenticator run the same fake program from
// the same kubeconfig entry. KUBERNETES_EXEC_INFO, recorded by the program, is equal as
// canonical JSON; both accept or both refuse the response; and both keep the same token
// and certificate.
func TestE9_ProtocolMatchesClientGo(t *testing.T) {
	prog, _ := fakeProgram(t)
	srv := newCredServer(t)
	cert, key := certPair(t, "e9-proto")
	write := func(t *testing.T, dir, name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pair := func(t *testing.T, dir string) string {
		return "certfile=" + write(t, dir, "cert.pem", cert) + ";keyfile=" + write(t, dir, "key.pem", key)
	}
	rows := []protocolRow{
		{"P1", execV1, false, false, func(*testing.T, string) string { return "token=p1-token" }, true, true, false},
		{"P2", execV1beta1, false, false, func(*testing.T, string) string { return "token=p2-token" }, true, true, false},
		{"P3", execV1, true, true, func(*testing.T, string) string { return "token=p3-token" }, true, true, false},
		{"P4", execV1beta1, true, false, pair, true, false, true},
		{"P5", execV1, false, false, func(t *testing.T, d string) string { return "token=p5-token;" + pair(t, d) }, true, true, true},
		{"P6", execV1, false, false, func(t *testing.T, d string) string { return "token=p6-token;certfile=" + write(t, d, "cert.pem", cert) }, false, false, false},
		{"P7", execV1, false, false, func(t *testing.T, d string) string { return "keyfile=" + write(t, d, "key.pem", key) }, false, false, false},
		{"P8", execV1, false, false, func(*testing.T, string) string { return "" }, false, false, false},
		{"P9", execV1, false, false, func(*testing.T, string) string { return "version=" + execV1beta1 + ";token=p9-token" }, false, false, false},
		{"P10", execV1, false, false, func(t *testing.T, d string) string {
			return "raw=" + write(t, d, "out.json", []byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"Other"}`))
		}, false, false, false},
		{"P11", execV1, false, false, func(*testing.T, string) string { return "token=p11-token;expiry=3600" }, true, true, false},
	}
	for _, r := range rows {
		t.Run(r.id, func(t *testing.T) {
			dir := t.TempDir()
			env := fakeEnv(t, dir, r.plan(t, dir))
			var envList []any
			for k, v := range env {
				envList = append(envList, map[string]any{"name": k, "value": v})
			}
			e := map[string]any{"apiVersion": r.version, "command": prog, "args": []any{"--row=" + r.id}, "env": envList, "provideClusterInfo": r.clusterInfo}
			if r.version == execV1 {
				e["interactiveMode"] = "Never"
			}
			cluster := map[string]any{"server": srv.srv.URL}
			if r.insecure {
				cluster["insecure-skip-tls-verify"] = true
				cluster["tls-server-name"] = "e9.invalid"
				cluster["proxy-url"] = "http://127.0.0.1:3128"
				cluster["disable-compression"] = true
				cluster["extensions"] = []any{map[string]any{"name": "client.authentication.k8s.io/exec", "extension": map[string]any{"audience": "e9-audience"}}}
			} else {
				cluster["certificate-authority-data"] = base64.StdEncoding.EncodeToString(srv.caPEM())
				cluster["tls-server-name"] = "127.0.0.1"
			}
			raw, _ := json.Marshal(map[string]any{
				"apiVersion": "v1", "kind": "Config", "current-context": "ctx",
				"clusters": []any{map[string]any{"name": "cl", "cluster": cluster}},
				"users":    []any{map[string]any{"name": "u", "user": map[string]any{"exec": e}}},
				"contexts": []any{map[string]any{"name": "ctx", "context": map[string]any{"cluster": "cl", "user": "u"}}},
			})
			cfgFile := write(t, dir, "kubeconfig", raw)
			loaded, err := clientcmd.LoadFromFile(cfgFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := clientcmd.NewNonInteractiveClientConfig(*loaded, "ctx", &clientcmd.ConfigOverrides{}, nil).ClientConfig()
			if err != nil {
				t.Fatalf("client configuration: %v", err)
			}

			// intuitik: the program built from the entry, run through credexec (run 1).
			p, err := execProgram(cfg.ExecProvider, cfg)
			if err != nil {
				t.Fatal(err)
			}
			ours := credexecRunner(helperBinary(t), nil)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			res := ours(ctx, p, func(start func() error) error { return start() })
			oursCred, oursOK := (*credential)(nil), false
			if res.Class == "ok" {
				oursCred, oursOK = parseCredential(res.Stdout, cfg.ExecProvider.APIVersion)
			}

			// client-go: its exec authenticator on the same entry (run 2).
			theirCluster, err := rest.ConfigToExecCluster(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !r.clusterInfo {
				theirCluster = nil
			}
			a, err := clientexec.GetAuthenticator(cfg.ExecProvider, theirCluster)
			if err != nil {
				t.Fatalf("client-go authenticator: %v", err)
			}
			tc := &transport.Config{}
			if err := a.UpdateTransportConfig(tc); err != nil {
				t.Fatalf("client-go transport: %v", err)
			}
			var header string
			rt := tc.WrapTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				header = req.Header.Get("Authorization")
				return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
			}))
			req, _ := http.NewRequest(http.MethodGet, srv.srv.URL+"/api", nil)
			_, theirErr := rt.RoundTrip(req)
			theirOK := theirErr == nil
			var theirCert []byte
			if theirOK && tc.TLS.GetCertHolder != nil {
				if c, err := tc.TLS.GetCertHolder.GetCert(); err == nil && c != nil && len(c.Certificate) > 0 {
					theirCert = c.Certificate[0]
				}
			}

			info1, _ := os.ReadFile(filepath.Join(dir, "info-1.json"))
			info2, _ := os.ReadFile(filepath.Join(dir, "info-2.json"))
			sameInfo := canonical(t, info1) == canonical(t, info2)
			oursToken := ""
			var oursCert []byte
			if oursCred != nil {
				oursToken = oursCred.token
				if len(oursCred.cert) > 0 {
					if b, _ := pemBlock(oursCred.cert); b != nil {
						oursCert = b
					}
				}
			}
			theirToken := strings.TrimPrefix(header, "Bearer ")
			t.Logf("E9 %s: KUBERNETES_EXEC_INFO equal %v; accepted intuitik %v client-go %v (want %v); token equal %v (present %v); certificate equal %v (present %v)",
				r.id, sameInfo, oursOK, theirOK, r.ok, oursToken == theirToken, oursToken != "", bytes.Equal(oursCert, theirCert), len(oursCert) > 0)
			if !sameInfo {
				t.Errorf("E9 %s: KUBERNETES_EXEC_INFO differs\n  intuitik %s\n  client-go %s", r.id, canonical(t, info1), canonical(t, info2))
			}
			if oursOK != theirOK || oursOK != r.ok {
				t.Errorf("E9 %s: accepted by intuitik %v, by client-go %v; want both %v", r.id, oursOK, theirOK, r.ok)
			}
			if oursToken != theirToken || (oursToken != "") != r.wantToken {
				t.Errorf("E9 %s: token kept by intuitik and client-go differ, or presence %v != %v", r.id, oursToken != "", r.wantToken)
			}
			if !bytes.Equal(oursCert, theirCert) || (len(oursCert) > 0) != r.cert {
				t.Errorf("E9 %s: certificate kept by intuitik and client-go differ, or presence %v != %v", r.id, len(oursCert) > 0, r.cert)
			}
			if r.id == "P11" && (oursCred == nil || oursCred.expiry.IsZero()) {
				t.Errorf("E9 P11: the expiry was not kept")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// canonical is a JSON document re-encoded with sorted keys.
func canonical(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "invalid JSON: " + string(raw)
	}
	out, _ := json.Marshal(v)
	if reflect.ValueOf(v).Kind() != reflect.Map {
		return "not an object: " + string(out)
	}
	return string(out)
}

// pemBlock is the DER of the first PEM block.
func pemBlock(data []byte) ([]byte, error) {
	b, _ := pem.Decode(data)
	if b == nil {
		return nil, nil
	}
	return b.Bytes, nil
}
