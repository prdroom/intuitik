package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// TestK_ConnectingAnswer (packet M1-T3a section 6.1): POST /api/cluster/context and
// POST /api/cluster/retry answer 202 with the cluster object in state connecting, also
// when the attempt they start has already finished and been published before the
// handler writes its answer. The attempt of the test finishes at once with K-EX, and the
// started hook of the cluster waits for that publication, so this order is forced on
// every run instead of left to the scheduler.
func TestK_ConnectingAnswer(t *testing.T) {
	finished := readonly.Outcome{
		State: readonly.StateCredentialProgramUnsupported, TextID: "K-EX", Message: "finished before the answer",
		Context: "ctx-a", Cluster: "cl-a", Server: "https://127.0.0.1:1",
		Paths:    []string{"/kubeconfig"},
		Contexts: []readonly.ContextEntry{{Name: "ctx-a", Cluster: "cl-a", Server: "https://127.0.0.1:1"}},
		Current:  "ctx-a",
	}
	for _, c := range []struct {
		name, path, body string
		handler          func(s *server) http.HandlerFunc
	}{
		{"POST /api/cluster/context", "/api/cluster/context", `{"name":"ctx-a"}`, func(s *server) http.HandlerFunc { return s.handleClusterContext }},
		{"POST /api/cluster/retry", "/api/cluster/retry", `{}`, func(s *server) http.HandlerFunc { return s.handleClusterRetry }},
	} {
		s := &server{}
		published := make(chan struct{}, 1)
		s.cluster.outcome = finished
		s.cluster.attempt = func(context.Context, string) readonly.Outcome { return finished }
		s.cluster.print = func(readonly.Outcome) { published <- struct{}{} }
		waited := false
		s.cluster.started = func() {
			select {
			case <-published:
				waited = true
			case <-time.After(5 * time.Second):
			}
		}
		rec := httptest.NewRecorder()
		c.handler(s)(rec, httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body)))
		var obj map[string]any
		json.Unmarshal(rec.Body.Bytes(), &obj)
		_, after := s.cluster.snapshot()
		t.Logf("K-202 %s: attempt published before the answer %v, answer %d state %v text_id %v message %v; state afterwards %s",
			c.name, waited, rec.Code, obj["state"], obj["text_id"], obj["message"], after.State)
		if !waited {
			t.Errorf("K-202 %s: the attempt was not published within 5 s, so the order under test did not happen", c.name)
		}
		if rec.Code != http.StatusAccepted || obj["state"] != "connecting" || obj["text_id"] != nil || obj["message"] != nil {
			t.Errorf("K-202 %s: answer %d %s; want 202 with state connecting, text_id null and message null", c.name, rec.Code, rec.Body.String())
		}
		if obj["context"] != "ctx-a" {
			t.Errorf("K-202 %s: answer context %v, want ctx-a", c.name, obj["context"])
		}
		if after.State != string(readonly.StateCredentialProgramUnsupported) {
			t.Errorf("K-202 %s: state afterwards %q, want the finished attempt's %q", c.name, after.State, readonly.StateCredentialProgramUnsupported)
		}
	}
}
