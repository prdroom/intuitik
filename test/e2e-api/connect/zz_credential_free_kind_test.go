//go:build kind

package connect

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/prdroom/intuitik/test/e2e-api/server/secretscan"
)

// TestK_CredentialFreeOutput (packet section 6.4, D3): the canaries (a bearer token, the
// client key of the kind admin user, an exec argument and an exec environment value)
// are in no output. Searched: the stdout and stderr of every intuitik process of this
// run and every raw T3 API response (status line, headers and body). The product
// writes no access log. Positive control in the same run: the searcher finds each
// canary in the fixture kubeconfig bytes. This file sorts last, so the tests before it
// have run and their output is collected.
func TestK_CredentialFreeOutput(t *testing.T) {
	dir := t.TempDir()
	program, _ := credentialProgram(t, dir)
	token, arg, env := canaries()
	raw := (&kube{current: "kind-ok"}).withKind(t).
		user("bad", tokenUser(token)).context("bad-token", "kind-c", "bad").
		user("exec-user", execUser(program)).context("exec-ctx", "kind-c", "exec-user").bytes()
	kc := writeFile(t, filepath.Join(dir, "kubeconfig"), raw)
	p := startPanel(t, panelOpts{kubeconfig: strPtr(kc)})
	p.mustSettle()
	for _, name := range []string{"bad-token", "exec-ctx", "kind-ok"} {
		p.switchTo(name)
		p.mustSettle()
		p.retry()
		p.mustSettle()
	}
	// The process ends here, so that its output joins the collection.
	p.cmd.Process.Kill()
	<-p.done
	allOutput.add(p.stdout.bytes(), p.stderr.bytes())

	key, err := base64.StdEncoding.DecodeString(kindCluster(t).keyData)
	if err != nil {
		t.Fatal(err)
	}
	canary := []struct{ name, value string }{
		{"bearer token", token},
		{"client key", string(key)},
		{"exec argument", arg},
		{"exec environment value", env},
	}
	allOutput.mu.Lock()
	output := bytes.Join(allOutput.output, []byte("\n"))
	responses := bytes.Join(allOutput.responses, []byte("\n"))
	nOut, nResp := len(allOutput.output), len(allOutput.responses)
	allOutput.mu.Unlock()
	t.Logf("K-D3 searched: %d output streams (%d bytes), %d raw API responses (%d bytes)", nOut, len(output), nResp, len(responses))
	for _, c := range canary {
		control := secretscan.Count(raw, c.value)
		inOutput, inResponses := secretscan.Count(output, c.value), secretscan.Count(responses, c.value)
		t.Logf("K-D3 canary %s: positive control %d, findings in output %d, in API responses %d", c.name, control, inOutput, inResponses)
		if control == 0 {
			t.Errorf("K-D3 canary %s: the positive control did not find it in the fixture kubeconfig", c.name)
		}
		if inOutput != 0 || inResponses != 0 {
			t.Errorf("K-D3 canary %s: found %d times in the output and %d times in the API responses, want 0", c.name, inOutput, inResponses)
		}
	}
}
