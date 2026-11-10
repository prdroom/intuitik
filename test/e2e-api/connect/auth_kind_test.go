//go:build kind

package connect

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// TestK_ConnectedNeedsAuthenticatedAnswer (packet W3, section 6.3): /version answers an
// anonymous request, yet a context whose token the server rejects is
// credentials-rejected, never connected.
func TestK_ConnectedNeedsAuthenticatedAnswer(t *testing.T) {
	token, _, _ := canaries()
	version := kindRequest(t, "/version", "")
	api := kindRequest(t, "/api", token)
	t.Logf("K-W3 kind API server: anonymous GET /version HTTP %d, GET /api with the rejected token HTTP %d", version, api)
	if version != 200 || api != 401 {
		t.Fatalf("setup: /version %d (want 200), /api with the rejected token %d (want 401)", version, api)
	}
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "bad-token"}).withKind(t).user("bad", tokenUser(token)).context("bad-token", "kind-c", "bad").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	t.Logf("K-W3: expected state credentials-rejected K-AU, actual %s %s", o.State, str(o.TextID))
	if o.State != "credentials-rejected" || str(o.Message) != fmt.Sprintf(textAU, "kind-c", "bad-token") {
		t.Errorf("K-W3: state %q message %q, want credentials-rejected with K-AU", o.State, str(o.Message))
	}
}

// serviceAccountToken creates a ServiceAccount in the kind cluster and returns a token
// for it. The ServiceAccount is deleted at the end of the test.
func serviceAccountToken(t *testing.T, name string) string {
	t.Helper()
	kubectl(t, nil, "create", "serviceaccount", name, "-n", "default")
	t.Cleanup(func() {
		kubectl(t, nil, "delete", "serviceaccount", name, "-n", "default", "--ignore-not-found")
	})
	return strings.TrimSpace(string(kubectl(t, nil, "create", "token", name, "-n", "default", "--duration", "10m")))
}

// TestK_ForbiddenWithCredentialsIsConnected (packet section 6.3): with credentials, a
// 403 on GET /api is connected. The test removes system:authenticated from the
// system:discovery ClusterRoleBinding for the duration of the case and restores it.
func TestK_ForbiddenWithCredentialsIsConnected(t *testing.T) {
	token := serviceAccountToken(t, "intuitik-forbidden")
	raw := kubectl(t, nil, "get", "clusterrolebinding", "system:discovery", "-o", "json")
	var original rbacv1.ClusterRoleBinding
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	var kept []rbacv1.Subject
	for _, s := range original.Subjects {
		if !(s.Kind == "Group" && s.Name == "system:authenticated") {
			kept = append(kept, s)
		}
	}
	changed := original.DeepCopy()
	changed.Subjects = kept
	body, _ := json.Marshal(changed)
	kubectl(t, body, "replace", "-f", "-")
	t.Cleanup(func() {
		var now rbacv1.ClusterRoleBinding
		json.Unmarshal(kubectl(t, nil, "get", "clusterrolebinding", "system:discovery", "-o", "json"), &now)
		now.Subjects = original.Subjects
		restored, _ := json.Marshal(now)
		kubectl(t, restored, "replace", "-f", "-")
		t.Logf("K-403 system:discovery restored; GET /api with the token: HTTP %d", waitStatus(t, token, 200))
	})
	status := waitStatus(t, token, http.StatusForbidden)
	t.Logf("K-403 GET /api with the ServiceAccount token after the binding change: HTTP %d", status)
	if status != http.StatusForbidden {
		t.Fatalf("setup: GET /api answered %d, want 403", status)
	}
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "sa-ctx"}).withKind(t).user("sa", tokenUser(token)).context("sa-ctx", "kind-c", "sa").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	t.Logf("K-403: expected state connected K-OK, actual %s %s", o.State, str(o.TextID))
	if o.State != "connected" || str(o.Message) != fmt.Sprintf(textOK, "kind-c", "sa-ctx") {
		t.Errorf("K-403: state %q message %q, want connected with K-OK", o.State, str(o.Message))
	}
}

// TestK_AnonymousForbidden (packet section 6.3, P-a): a context with no credentials
// against kind, where the anonymous GET /api answers 403, is credentials-missing.
func TestK_AnonymousForbidden(t *testing.T) {
	status := kindRequest(t, "/api", "")
	t.Logf("K-NC kind API server: anonymous GET /api HTTP %d", status)
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "no-creds"}).withKind(t).user("nobody", map[string]any{}).context("no-creds", "kind-c", "nobody").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	t.Logf("K-NC HTTP %d: expected state credentials-missing K-NC, actual %s %s", status, o.State, str(o.TextID))
	if status != http.StatusForbidden {
		t.Errorf("setup: anonymous GET /api answered %d, want 403", status)
	}
	if o.State != "credentials-missing" || str(o.Message) != fmt.Sprintf(textNC, "no-creds", "kind-c") {
		t.Errorf("K-NC: state %q message %q, want credentials-missing with K-NC", o.State, str(o.Message))
	}
}

// TestK_AnonymousUnauthorized (packet section 6.3, P-a): a context with no credentials
// against a local TLS test server that answers 401 to every request (a stand-in for a
// cluster with anonymous authentication off) is credentials-missing.
func TestK_AnonymousUnauthorized(t *testing.T) {
	url, ca := statusServer(t, http.StatusUnauthorized)
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "no-creds"}).cluster("test-c", url, ca).user("nobody", map[string]any{}).context("no-creds", "test-c", "nobody").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	o := p.mustSettle()
	t.Logf("K-NC HTTP 401 test server: expected state credentials-missing K-NC, actual %s %s", o.State, str(o.TextID))
	if o.State != "credentials-missing" || str(o.Message) != fmt.Sprintf(textNC, "no-creds", "test-c") {
		t.Errorf("K-NC: state %q message %q, want credentials-missing with K-NC", o.State, str(o.Message))
	}
}

// TestK_CredentialsRejected (packet section 6.5, D4): the token of a deleted
// ServiceAccount and a malformed bearer token both get 401 and give
// credentials-rejected with K-AU.
func TestK_CredentialsRejected(t *testing.T) {
	malformed, _, _ := canaries()
	token := serviceAccountToken(t, "intuitik-deleted")
	kubectl(t, nil, "delete", "serviceaccount", "intuitik-deleted", "-n", "default")
	deleted := waitStatus(t, token, http.StatusUnauthorized)
	bad := kindRequest(t, "/api", malformed)
	t.Logf("K-AU GET /api: deleted ServiceAccount token HTTP %d, malformed token HTTP %d", deleted, bad)
	if deleted != 401 || bad != 401 {
		t.Fatalf("setup error: deleted ServiceAccount token %d, malformed token %d, want 401 for both", deleted, bad)
	}
	kc := writeFile(t, filepath.Join(t.TempDir(), "kubeconfig"), (&kube{current: "deleted-sa"}).withKind(t).
		user("deleted", tokenUser(token)).context("deleted-sa", "kind-c", "deleted").
		user("malformed", tokenUser(malformed)).context("malformed", "kind-c", "malformed").bytes())
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	for i, name := range []string{"deleted-sa", "malformed"} {
		if i > 0 {
			p.switchTo(name)
		}
		o := p.mustSettle()
		t.Logf("K-AU %s: expected state credentials-rejected K-AU, actual %s %s", name, o.State, str(o.TextID))
		if o.State != "credentials-rejected" || str(o.Message) != fmt.Sprintf(textAU, "kind-c", name) {
			t.Errorf("K-AU %s: state %q message %q, want credentials-rejected with K-AU", name, o.State, str(o.Message))
		}
	}
}

// TestK_ExecNotRun (packet section 6.7): a context whose only credential is an exec entry
// is credential-program-unsupported with the exact K-EX line once per attempt. The
// program, which would create a marker file, is never started, at start or on retry,
// and the exec canaries are in no output.
func TestK_ExecNotRun(t *testing.T) {
	dir := t.TempDir()
	program, marker := credentialProgram(t, dir)
	raw := (&kube{current: "exec-ctx"}).withKind(t).user("exec-user", execUser(program)).context("exec-ctx", "kind-c", "exec-user").bytes()
	kc := writeFile(t, filepath.Join(dir, "kubeconfig"), raw)
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	line := fmt.Sprintf(textEX, "exec-ctx", program)
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt == 2 {
			p.retry()
		}
		o := p.mustSettle()
		n := waitCount(p.stderr, line, attempt)
		_, statErr := os.Stat(marker)
		ran := statErr == nil
		t.Logf("K-EX attempt %d: state %s text_id %s, K-EX lines %d, marker file exists %v", attempt, o.State, str(o.TextID), n, ran)
		if o.State != "credential-program-unsupported" || str(o.Message) != line || n != attempt {
			t.Errorf("K-EX attempt %d: state %q message %q, %d K-EX lines; want credential-program-unsupported, the exact K-EX line, %d lines", attempt, o.State, str(o.Message), n, attempt)
		}
		if ran {
			t.Errorf("K-EX attempt %d: the credential program ran (marker %s exists); stderr %q", attempt, marker, p.stderr.list())
		}
	}
	_, arg, env := canaries()
	for _, c := range []struct{ name, value string }{{"exec argument", arg}, {"exec environment value", env}} {
		control := secretscan.Count(raw, c.value)
		found := secretscan.Count(p.stdout.bytes(), c.value) + secretscan.Count(p.stderr.bytes(), c.value)
		t.Logf("K-EX canary %s: positive control %d, found in the output of this run %d", c.name, control, found)
		if control == 0 || found != 0 {
			t.Errorf("K-EX canary %s: positive control %d (want at least 1), found %d (want 0)", c.name, control, found)
		}
	}
}
