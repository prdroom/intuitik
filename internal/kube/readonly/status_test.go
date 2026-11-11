package readonly

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestK_StatusTable (packet M1-T3a section 6.3, P-a and P-b): a local TLS test server
// answers GET /api with one status of each class, with and without credentials. With
// credentials: 200 and 403 are connected, 401 is credentials-rejected, every other
// status is unreachable with K-HS. Without credentials every answer is
// credentials-missing with K-NC. A final 1xx answer cannot be produced by the test
// server, so the classes are 2xx, 3xx, 4xx and 5xx.
func TestK_StatusTable(t *testing.T) {
	// 300 and 304 are 3xx answers that an HTTP client does not follow as redirects.
	statuses := []int{200, 204, 300, 304, 400, 401, 403, 404, 429, 500, 503}
	for _, status := range statuses {
		for _, withCredentials := range []bool{true, false} {
			name := fmt.Sprintf("HTTP %d credentials=%v", status, withCredentials)
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			user := map[string]any{}
			if withCredentials {
				user["token"] = randomValue(t, "st-")
			}
			useKubeconfig(t, kubeconfig(srv.URL, certPEM(srv.Certificate().Raw), user))
			o := Attempt(context.Background(), "")
			srv.Close()

			wantState, wantID, wantMsg := StateCredentialsMissing, "K-NC", fmt.Sprintf(textNC, "ctx", "cl")
			if withCredentials {
				switch status {
				case 200, 403:
					wantState, wantID, wantMsg = StateConnected, "K-OK", fmt.Sprintf(textOK, "cl", "ctx")
				case 401:
					wantState, wantID, wantMsg = StateCredentialsRejected, "K-AU", fmt.Sprintf(textAU, "cl", "ctx")
				default:
					wantState, wantID, wantMsg = StateUnreachable, "K-HS", fmt.Sprintf(textHS, srv.URL, "ctx", status)
				}
			}
			t.Logf("K-STATUS %s: expected %s %s, actual %s %s", name, wantState, wantID, o.State, o.TextID)
			if o.State != wantState || o.TextID != wantID || o.Message != wantMsg {
				t.Errorf("K-STATUS %s: got state %q text %q message %q, want %q %q %q", name, o.State, o.TextID, o.Message, wantState, wantID, wantMsg)
			}
		}
	}
}
