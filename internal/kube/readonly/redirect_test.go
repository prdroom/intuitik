package readonly

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestK_RedirectNotFollowed (packet M1-T3a sections 3.1 W3 and 6.3, P-b): GET /api is
// the one request of an attempt, and its own answer is classified. A redirect answer
// with a Location header is never followed: with credentials it is unreachable with
// K-HS and its own status, without credentials credentials-missing with K-NC. The
// Location points either to a path of the same server that answers 200 or to a closed
// local port; the test server counts every request it receives (want 1).
func TestK_RedirectNotFollowed(t *testing.T) {
	closed := closedLocalURL(t)
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, target := range []string{"same server /version (200)", "closed local port"} {
			for _, withCredentials := range []bool{true, false} {
				name := fmt.Sprintf("HTTP %d to %s credentials=%v", status, target, withCredentials)
				var requests atomic.Int32
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.URL.Path != "/api" {
						w.WriteHeader(http.StatusOK)
						return
					}
					location := closed + "/api"
					if target == "same server /version (200)" {
						location = "/version"
					}
					w.Header().Set("Location", location)
					w.WriteHeader(status)
				}))
				user := map[string]any{}
				if withCredentials {
					user["token"] = randomValue(t, "rd-")
				}
				useKubeconfig(t, kubeconfig(srv.URL, certPEM(srv.Certificate().Raw), user))
				o := Attempt(context.Background(), "")
				srv.Close()

				wantState, wantID, wantMsg := StateCredentialsMissing, "K-NC", fmt.Sprintf(textNC, "ctx", "cl")
				if withCredentials {
					wantState, wantID, wantMsg = StateUnreachable, "K-HS", fmt.Sprintf(textHS, srv.URL, "ctx", status)
				}
				n := requests.Load()
				t.Logf("K-REDIRECT %s: expected %s %s and 1 request, actual %s %s and %d request(s)", name, wantState, wantID, o.State, o.TextID, n)
				if o.State != wantState || o.TextID != wantID || o.Message != wantMsg || n != 1 {
					t.Errorf("K-REDIRECT %s: got state %q text %q message %q, %d request(s); want %q %q %q, 1 request", name, o.State, o.TextID, o.Message, n, wantState, wantID, wantMsg)
				}
			}
		}
	}
}

// closedLocalURL is an https address of a local port that nothing listens on.
func closedLocalURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "https://" + addr
}
