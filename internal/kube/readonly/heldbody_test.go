package readonly

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestK_AnswerWithHeldBody (packet M1-T3a sections 3.1 W1 and W3, 6.3): the first
// HTTP answer is the status line and headers of GET /api. A server that flushes them
// at once, sends part of a longer body and holds the rest beyond W1 has answered: the
// state follows the status (with credentials 200 and 403 connected, 401
// credentials-rejected, 500 K-HS; without credentials K-NC), the server sees one
// request, and the outcome comes well before W1 instead of K-UR "no answer within 10 s".
func TestK_AnswerWithHeldBody(t *testing.T) {
	const limit = 3 * time.Second
	cases := []struct {
		status          int
		withCredentials bool
	}{{200, true}, {403, true}, {401, true}, {500, true}, {200, false}}
	for _, c := range cases {
		name := fmt.Sprintf("HTTP %d credentials=%v", c.status, c.withCredentials)
		var requests atomic.Int32
		release := make(chan struct{})
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(c.status)
			w.Write([]byte(`{"kind":"APIVersions",`))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		user := map[string]any{}
		if c.withCredentials {
			user["token"] = randomValue(t, "hb-")
		}
		useKubeconfig(t, kubeconfig(srv.URL, certPEM(srv.Certificate().Raw), user))
		start := time.Now()
		o := Attempt(context.Background(), "")
		elapsed := time.Since(start)
		close(release)
		srv.Close()

		wantState, wantID, wantMsg := StateCredentialsMissing, "K-NC", fmt.Sprintf(textNC, "ctx", "cl")
		if c.withCredentials {
			switch c.status {
			case 200, 403:
				wantState, wantID, wantMsg = StateConnected, "K-OK", fmt.Sprintf(textOK, "cl", "ctx")
			case 401:
				wantState, wantID, wantMsg = StateCredentialsRejected, "K-AU", fmt.Sprintf(textAU, "cl", "ctx")
			default:
				wantState, wantID, wantMsg = StateUnreachable, "K-HS", fmt.Sprintf(textHS, srv.URL, "ctx", c.status)
			}
		}
		n := requests.Load()
		t.Logf("K-HELDBODY %s: expected %s %s, 1 request, within %s; actual %s %s, %d request(s), after %s",
			name, wantState, wantID, limit, o.State, o.TextID, n, elapsed.Round(10*time.Millisecond))
		if o.State != wantState || o.TextID != wantID || o.Message != wantMsg || n != 1 {
			t.Errorf("K-HELDBODY %s: got state %q text %q message %q, %d request(s); want %q %q %q, 1 request", name, o.State, o.TextID, o.Message, n, wantState, wantID, wantMsg)
		}
		if elapsed > limit {
			t.Errorf("K-HELDBODY %s: the outcome came after %s; want within %s of the answer's headers", name, elapsed.Round(10*time.Millisecond), limit)
		}
	}
}
