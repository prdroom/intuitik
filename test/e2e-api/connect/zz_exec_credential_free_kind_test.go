//go:build kind

package connect

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// credCase is one case of TestK_ExecCredentialFreeOutput: its program plans, whether it
// uses the read trigger, and what happens after connected.
type credCase struct {
	name    string
	plans   []string
	trigger bool                                      // e2etest binary with READ-TRIGGER-T3b
	after   func(t *testing.T, p *panel, s *credServer) int // the read after the first attempt; its answer
	tokens  map[string]string                         // names of the tokens the server may record
	want    string                                    // the server record of the read (READ-TRIGGER-T3b)
	// NEGFIX-T3b: the canary the refresh run prints, and the read's answer and server
	// record under the negative fixture refresh-output-leak (the refresh run fails there).
	refreshed            string
	leakRead             int
	leakWant             string
}

// leakFixture is the negative fixture of NEGFIX-T3b: the read route of cluster-read-route,
// and a refresh run's output put into its K-EP text.
const leakFixture = "refresh-output-leak"

// subRun is what one panel of a case produced: its output and API bodies, collected the
// same way for every sub-run.
type subRun struct {
	output    [][]byte
	responses [][]byte
}

// TestK_ExecCredentialFreeOutput (acceptance 7, E8): canaries in the program's stdout (a
// token and a client certificate key) and in the kubeconfig exec argument and
// environment are found 0 times in intuitik's stdout, stderr and API bodies, for the
// first run, refresh on expiry, refresh after 401, malformed output, wrong API version,
// missing status, output over 1 MiB and timeout. Positive control in the same run: each
// canary is found in its fixture (the kubeconfig or the program's output file). The
// program's stderr marker is found at least once on intuitik's stderr. The two refresh
// cases start the e2etest binary with READ-TRIGGER-T3b; every other case starts the
// release binary. This file sorts last.
func TestK_ExecCredentialFreeOutput(t *testing.T) {
	marker := "intuitik-exec-stderr-marker-" + randomHex(8)
	tok := func(name string) string { return strings.Join([]string{"intuitik", "canary", name, randomHex(12)}, "-") }
	first, expiryA, expiryB, rejectA, rejectB := tok("first"), tok("expiry-a"), tok("expiry-b"), tok("reject-a"), tok("reject-b")
	malformed, version, nostatus, bigToken := tok("malformed"), tok("version"), tok("nostatus"), tok("big")
	keyPEM, certPEM := clientPair(t)
	keyCanary := strings.Split(string(keyPEM), "\n")[1]

	dir := t.TempDir()
	certFile := writeFile(t, filepath.Join(dir, "cert.pem"), certPEM)
	keyFile := writeFile(t, filepath.Join(dir, "key.pem"), keyPEM)
	files := map[string][]byte{
		"malformed": []byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"` + malformed),
		"nostatus":  []byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"token":"` + nostatus + `"}}`),
		"big":       append([]byte(`{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"`+bigToken+`"}}`), bytes.Repeat([]byte(" "), 1<<20)...),
	}
	paths := map[string]string{}
	for name, data := range files {
		paths[name] = writeFile(t, filepath.Join(dir, name+".json"), data)
	}
	stderr := "stderr=" + marker + ";"
	cases := []credCase{
		{name: "first run", plans: []string{stderr + "token=" + first}},
		{name: "first run with a client certificate", plans: []string{stderr + "certfile=" + certFile + ";keyfile=" + keyFile}},
		{name: "refresh on expiry", trigger: true, plans: []string{stderr + "token=" + expiryA + ";expiry=3", stderr + "token=" + expiryB},
			tokens: map[string]string{expiryA: "A", expiryB: "B"}, want: "[B]", refreshed: expiryB, leakRead: 503, leakWant: "[]",
			after: func(t *testing.T, p *panel, s *credServer) int {
				untilAfterStart(p, 4*time.Second)
				return p.readOnce(30 * time.Second)
			}},
		{name: "refresh after 401", trigger: true, plans: []string{stderr + "token=" + rejectA, stderr + "token=" + rejectB},
			tokens: map[string]string{rejectA: "A", rejectB: "B"}, want: "[A B]", refreshed: rejectB, leakRead: 401, leakWant: "[A]",
			after: func(t *testing.T, p *panel, s *credServer) int {
				s.reject(rejectA)
				return p.readOnce(30 * time.Second)
			}},
		{name: "malformed output", plans: []string{stderr + "raw=" + paths["malformed"]}},
		{name: "wrong API version", plans: []string{stderr + "version=client.authentication.k8s.io/v1beta1;token=" + version}},
		{name: "missing status", plans: []string{stderr + "raw=" + paths["nostatus"]}},
		{name: "output over 1 MiB", plans: []string{stderr + "raw=" + paths["big"]}},
		{name: "timeout", plans: []string{stderr + "hang"}},
	}
	var output, responses, fixtures [][]byte
	// run starts one panel for case c with fixture ("" for the release binary) and returns
	// what it produced. readWant and recordWant are the read's expected answer and server
	// record; for a case without a read they are unused.
	run := func(c credCase, fixture string, readWant int, recordWant string) subRun {
		srv := newCredServer(t)
		prog, user := newProgram(t, "IfAvailable", c.plans...)
		raw := (&kube{current: "exec-ctx"}).withServer("fake-c", srv).user("exec-user", user).context("exec-ctx", "fake-c", "exec-user").bytes()
		fixtures = append(fixtures, raw)
		kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), raw)
		opts := panelOpts{kubeconfig: strPtr(kc)}
		if fixture != "" {
			opts.test, opts.fixture = true, fixture
		}
		p := startPanel(t, opts)
		o := p.mustSettle()
		label := c.name
		if fixture != "" {
			label += " [" + fixture + "]"
		}
		if c.after != nil {
			// Preparation: connected on the first run, with 0 findings of either canary.
			early := secretscan.Count(append(p.stdout.bytes(), p.stderr.bytes()...), c.refreshed)
			if o.State != "connected" || early != 0 {
				t.Fatalf("preparation: K-E8 case %s: state %s, refresh canary already found %d times", label, o.State, early)
			}
			// READ-TRIGGER-T3b: the read, the program starts and the server record show that
			// the product's refresh path ran in this case.
			before := srv.count()
			status := c.after(t, p, srv)
			got := fmt.Sprint(srv.received(before, c.tokens))
			t.Logf("K-E8 case %s: read answered %d; program starts %d %q; server record of the read %s (want %d, 2 starts, %s)",
				label, status, prog.starts(), prog.lines("starts"), got, readWant, recordWant)
			if status != readWant || prog.starts() != 2 || got != recordWant {
				t.Errorf("K-E8 case %s: read %d, starts %d, server record %s; want %d, 2 starts and %s", label, status, prog.starts(), got, readWant, recordWant)
			}
		}
		_, r := p.cluster()
		final, _ := p.cluster()
		p.cmd.Process.Kill()
		<-p.done
		t.Logf("K-E8 case %s: binary %s%s; first state %s, final state %s", label, binaryInfo(t, p), map[bool]string{true: ", fixture " + fixture, false: ""}[fixture != ""], o.State, final.State)
		return subRun{output: [][]byte{p.stdout.bytes(), p.stderr.bytes()}, responses: [][]byte{r.raw}}
	}
	for _, c := range cases {
		fixture := ""
		if c.trigger {
			fixture = readFixture
		}
		normal := run(c, fixture, 200, c.want)
		output = append(output, normal.output...)
		responses = append(responses, normal.responses...)
		allOutput.add(normal.output...)
		if !c.trigger {
			continue
		}
		// NEGFIX-T3b: the same case with the negative fixture. Same binary, plan and read;
		// the same collection and the same searcher. The refresh run's own canary must be
		// found; in the normal sub-run it must not (checked with every canary below).
		leak := run(c, leakFixture, c.leakRead, c.leakWant)
		leakOut, leakAPI := bytes.Join(leak.output, []byte("\n")), bytes.Join(leak.responses, []byte("\n"))
		nOut, nAPI := secretscan.Count(bytes.Join(normal.output, []byte("\n")), c.refreshed), secretscan.Count(bytes.Join(normal.responses, []byte("\n")), c.refreshed)
		fOut, fAPI := secretscan.Count(leakOut, c.refreshed), secretscan.Count(leakAPI, c.refreshed)
		t.Logf("K-E8 NEGFIX case %s: refresh canary in the normal sub-run (fixture %s) output %d, API bodies %d; with the negative fixture %s output %d, API bodies %d (want at least 1)",
			c.name, readFixture, nOut, nAPI, leakFixture, fOut, fAPI)
		if fOut+fAPI == 0 {
			t.Errorf("K-E8 NEGFIX case %s: the canary search did not find the refresh run's canary under the negative fixture %s", c.name, leakFixture)
		}
	}
	for _, f := range files {
		fixtures = append(fixtures, f)
	}
	fixtures = append(fixtures, keyPEM)

	_, arg, env := canaries()
	all := bytes.Join(output, []byte("\n"))
	api := bytes.Join(responses, []byte("\n"))
	fix := bytes.Join(fixtures, []byte("\n"))
	for _, c := range []struct{ name, value string }{
		{"first run token", first}, {"client certificate key", keyCanary},
		{"refresh on expiry token (first)", expiryA}, {"refresh on expiry token (refreshed)", expiryB},
		{"refresh after 401 token (rejected)", rejectA}, {"refresh after 401 token (refreshed)", rejectB},
		{"malformed output", malformed}, {"wrong API version token", version}, {"missing status", nostatus}, {"output over 1 MiB token", bigToken},
		{"exec argument", arg}, {"exec environment value", env},
	} {
		control := secretscan.Count(fix, c.value)
		found, inAPI := secretscan.Count(all, c.value), secretscan.Count(api, c.value)
		t.Logf("K-E8 canary %s: positive control %d, findings in stdout and stderr %d, in API bodies %d", c.name, control, found, inAPI)
		if control == 0 {
			t.Errorf("K-E8 canary %s: the positive control did not find it in its fixture", c.name)
		}
		if found != 0 || inAPI != 0 {
			t.Errorf("K-E8 canary %s: found %d times in the output and %d times in the API bodies, want 0", c.name, found, inAPI)
		}
	}
	markers := bytes.Count(all, []byte(marker))
	t.Logf("K-E8 the program's stderr marker on intuitik's stderr: %d times (want at least 1)", markers)
	if markers == 0 {
		t.Errorf("K-E8: the program's stderr marker never reached intuitik's stderr")
	}
}

// clientPair is a client certificate and key made at run time, in PEM form.
func clientPair(t *testing.T) (keyPEM, certPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "intuitik-e8"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
