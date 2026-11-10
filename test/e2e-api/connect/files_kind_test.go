//go:build kind

package connect

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestK_KubeconfigLoading (packet section 6.6): the loading rules over real files.
// KUBECONFIG with two files lists the contexts of both, and the current context comes
// from the first file that sets one; a missing file gives K-NK with its path; with
// KUBECONFIG unset, ~/.kube/config of the HOME directory is read.
func TestK_KubeconfigLoading(t *testing.T) {
	ki := kindCluster(t)
	file := func(dir, name, current, ctx string) string {
		k := &kube{current: current}
		k.cluster("kind-c", ki.server, ki.caData).
			user("kind-admin", map[string]any{"client-certificate-data": ki.certData, "client-key-data": ki.keyData}).
			context(ctx, "kind-c", "kind-admin")
		return writeFile(t, filepath.Join(dir, name), k.bytes())
	}
	for _, c := range []struct {
		name        string
		firstSets   bool
		wantCurrent string
	}{
		{"first file sets no current context", false, "ctx-two"},
		{"first file sets the current context", true, "ctx-one"},
	} {
		dir := t.TempDir()
		first := ""
		if c.firstSets {
			first = "ctx-one"
		}
		one, two := file(dir, "one", first, "ctx-one"), file(dir, "two", "ctx-two", "ctx-two")
		p := startPanel(t, panelOpts{kubeconfig: strPtr(one + ":" + two)})
		o := p.mustSettle()
		t.Logf("K-LOAD %s: contexts %v, current %q, active %q, paths %v, state %s", c.name, o.names(), o.flagged(true), str(o.Context), o.KubeconfigPaths, o.State)
		if !reflect.DeepEqual(o.names(), []string{"ctx-one", "ctx-two"}) || o.flagged(true) != c.wantCurrent ||
			str(o.Context) != c.wantCurrent || o.State != "connected" || !reflect.DeepEqual(o.KubeconfigPaths, []string{one, two}) {
			t.Errorf("K-LOAD %s: contexts %v current %q context %q state %q paths %v; want [ctx-one ctx-two], %q, connected, [%s %s]",
				c.name, o.names(), o.flagged(true), str(o.Context), o.State, o.KubeconfigPaths, c.wantCurrent, one, two)
		}
	}

	missing := filepath.Join(t.TempDir(), "no-such-kubeconfig")
	p := startPanel(t, panelOpts{kubeconfig: strPtr(missing)})
	o := p.mustSettle()
	t.Logf("K-LOAD missing file: state %s, message %q, contexts %d", o.State, str(o.Message), len(o.Contexts))
	if o.State != "no-kubeconfig" || str(o.Message) != fmt.Sprintf(textNK, missing) || len(o.Contexts) != 0 {
		t.Errorf("K-LOAD missing file: state %q message %q contexts %v", o.State, str(o.Message), o.names())
	}

	home := t.TempDir()
	def := file(filepath.Join(home, ".kube"), "config", "ctx-home", "ctx-home")
	p = startPanel(t, panelOpts{home: home})
	o = p.mustSettle()
	t.Logf("K-LOAD KUBECONFIG unset: paths %v, state %s, context %s", o.KubeconfigPaths, o.State, str(o.Context))
	if o.State != "connected" || str(o.Context) != "ctx-home" || !reflect.DeepEqual(o.KubeconfigPaths, []string{def}) {
		t.Errorf("K-LOAD KUBECONFIG unset: state %q context %q paths %v, want connected ctx-home [%s]", o.State, str(o.Context), o.KubeconfigPaths, def)
	}
}

// TestK_KubeconfigUntouched (packet W4, section 6.6): the sha256 and modification time
// of every kubeconfig file are the same before and after a run with context switches
// and a retry.
func TestK_KubeconfigUntouched(t *testing.T) {
	dir := t.TempDir()
	port, _ := closedPort(t)
	one := writeFile(t, filepath.Join(dir, "one"), (&kube{current: "kind-ok"}).withKind(t).bytes())
	two := writeFile(t, filepath.Join(dir, "two"), (&kube{}).cluster("closed-c", "https://127.0.0.1:"+strconv.Itoa(port), kindCluster(t).caData).context("closed", "closed-c", "kind-admin").bytes())
	before := map[string]string{one: fileStamp(t, one), two: fileStamp(t, two)}
	time.Sleep(20 * time.Millisecond) // a write after this point has a later mtime
	p := startPanel(t, panelOpts{kubeconfig: strPtr(one + ":" + two)})
	steps := []string{fmt.Sprintf("start %s", p.mustSettle().State)}
	p.switchTo("closed")
	steps = append(steps, fmt.Sprintf("switch closed %s", p.mustSettle().State))
	p.switchTo("kind-ok")
	steps = append(steps, fmt.Sprintf("switch kind-ok %s", p.mustSettle().State))
	p.retry()
	steps = append(steps, fmt.Sprintf("retry %s", p.mustSettle().State))
	t.Logf("K-W4 steps: %s", strings.Join(steps, ", "))
	for _, f := range []string{one, two} {
		after := fileStamp(t, f)
		t.Logf("K-W4 %s before %s", f, before[f])
		t.Logf("K-W4 %s after  %s", f, after)
		if after != before[f] {
			t.Errorf("K-W4 %s changed during the run: before %s, after %s", f, before[f], after)
		}
	}
}

// TestK_RetryRereadsKubeconfig (packet W2, section 4): a kubeconfig written after start
// is read by the next attempt: start with no kubeconfig, write it at the same path,
// retry, and the state is connected with K-OK.
func TestK_RetryRereadsKubeconfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kube", "config")
	p := startPanel(t, panelOpts{kubeconfig: strPtr(path)})
	first := p.mustSettle()
	t.Logf("K-W2 before the file exists: state %s text_id %s", first.State, str(first.TextID))
	writeFile(t, path, (&kube{current: "kind-ok"}).withKind(t).bytes())
	if r := p.retry(); r.status != 202 {
		t.Errorf("retry: status %d", r.status)
	}
	o := p.mustSettle()
	want := fmt.Sprintf(textOK, "kind-c", "kind-ok")
	t.Logf("K-W2 after writing the file and retry: expected connected K-OK, actual %s %s", o.State, str(o.TextID))
	if o.State != "connected" || str(o.Message) != want || waitCount(p.stdout, want, 1) != 1 {
		t.Errorf("K-W2: state %q message %q; want connected and %q once on stdout (stdout %q)", o.State, str(o.Message), want, p.stdout.list())
	}
}

// TestK_UnreachableTimeout (packet W1, section 6.6): a server that accepts the TCP
// connection and never completes the TLS handshake, and one that holds the HTTP answer
// after the handshake, give unreachable with "no answer within 10 s" within W1 plus 2 s.
// A closed local port gives the OS error.
func TestK_UnreachableTimeout(t *testing.T) {
	held := newHeldServer(t)
	release := make(chan struct{})
	holding := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); holding.Close() })
	holdingURL, holdingCA := holding.URL, caOf(holding)
	port, osErr := closedPort(t)
	closed := "https://127.0.0.1:" + strconv.Itoa(port)

	for _, c := range []struct {
		name, server, ca, os string
		arrivals             func() int
	}{
		{"TLS handshake never completes", held.url(), kindCluster(t).caData, textTimeout, held.count},
		{"HTTP answer held after the handshake", holdingURL, holdingCA, textTimeout, nil},
		{"closed local port", closed, kindCluster(t).caData, osErr, nil},
	} {
		kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "far"}).cluster("far-c", c.server, c.ca).
			user("far-user", tokenUser("intuitik-"+randomHex(8))).context("far", "far-c", "far-user").bytes())
		p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
		o, ok := p.settle(w1 + 2*time.Second)
		elapsed := time.Since(p.started)
		want := fmt.Sprintf(textUR, c.server, "far", c.os)
		arrivals := -1
		if c.arrivals != nil {
			arrivals = c.arrivals()
		}
		t.Logf("K-W1 %s: state %s after %s (limit %s), test server connections %d, message %q", c.name, o.State, elapsed.Round(10*time.Millisecond), w1+2*time.Second, arrivals, str(o.Message))
		if !ok || o.State != "unreachable" || str(o.Message) != want {
			t.Errorf("K-W1 %s: settled %v, state %q, message %q; want unreachable with %q within %s", c.name, ok, o.State, str(o.Message), want, w1+2*time.Second)
		}
		if c.arrivals != nil && arrivals == 0 {
			t.Errorf("K-W1 %s: the test server saw no connection", c.name)
		}
	}
}

// caOf is the certificate-authority-data that trusts a TLS test server.
func caOf(srv *httptest.Server) string {
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}
