package readonly

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// TestE10_NoExecProvider (packet M1-T3b E10): the configuration of a connected exec
// context has no exec provider, and a client built from it never starts a process: the
// fake program's starts stay at the one run of the attempt.
func TestE10_NoExecProvider(t *testing.T) {
	srv := newCredServer(t)
	prog, _ := fakeProgram(t)
	dir := t.TempDir()
	useKubeconfig(t, kubeconfig(srv.srv.URL, srv.caPEM(), execUser(prog, fakeEnv(t, dir, "token="+randomValue(t, "e10t")), nil)))
	g := NewGeneration(context.Background(), credexecRunner(helperBinary(t), nil), nil)
	defer g.Close()
	o := g.Attempt("")
	if o.State != StateConnected || o.config == nil {
		t.Fatalf("setup: state %s message %q", o.State, o.Message)
	}
	startsAfterAttempt := fakeStarts(dir)
	hc, err := rest.HTTPClientFor(o.config)
	if err == nil {
		if resp, err := hc.Get(srv.srv.URL + "/api"); err == nil {
			resp.Body.Close()
		}
	}
	time.Sleep(200 * time.Millisecond)
	t.Logf("E10 exec provider of the connected configuration nil %v; program starts after the attempt %d, after a client of the configuration sent a request %d",
		o.config.ExecProvider == nil, startsAfterAttempt, fakeStarts(dir))
	if o.config.ExecProvider != nil {
		t.Errorf("E10: the connected configuration has an exec provider")
	}
	if fakeStarts(dir) != startsAfterAttempt {
		t.Errorf("E10: a client of the configuration started the program (%d starts, want %d)", fakeStarts(dir), startsAfterAttempt)
	}
}

// TestE10_AuthPrecedence (E10, SC, acceptance 9): a fake TLS API server records the
// credential each request carried. A user entry with an exec entry and a static
// credential (token, token file, basic auth, certificate pair) runs no program and sends
// the static credential. Exec alone sends the program's token; a refresh that turns a
// token into a certificate pair, and back, sends only the new credential.
func TestE10_AuthPrecedence(t *testing.T) {
	prog, _ := fakeProgram(t)
	staticCert, staticKey := certPair(t, "e10-static")
	staticToken, basicValue := randomValue(t, "e10s"), randomValue(t, "e10b")
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("e10-file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		static map[string]any
		want   string // the record the server must see: "Bearer ...", "basic" or "cn:..."
	}{
		{"token", map[string]any{"token": staticToken}, "Bearer " + staticToken},
		{"token file", map[string]any{"tokenFile": tokenFile}, "Bearer e10-file-token"},
		{"basic auth", map[string]any{"username": "e10-user", "password": basicValue}, "Basic " + base64.StdEncoding.EncodeToString([]byte("e10-user:"+basicValue))},
		{"certificate pair", map[string]any{"client-certificate-data": base64.StdEncoding.EncodeToString(staticCert), "client-key-data": base64.StdEncoding.EncodeToString(staticKey)}, "cn:e10-static"},
	} {
		t.Run("exec with "+c.name, func(t *testing.T) {
			srv := newCredServer(t)
			dir := t.TempDir()
			useKubeconfig(t, kubeconfig(srv.srv.URL, srv.caPEM(), execUser(prog, fakeEnv(t, dir, "token="+randomValue(t, "e10p")), c.static)))
			g := NewGeneration(context.Background(), credexecRunner(helperBinary(t), nil), nil)
			defer g.Close()
			o := g.Attempt("")
			s, _ := read(t, o)
			recs := srv.seen()
			ok := len(recs) > 0
			for _, r := range recs {
				got := r.auth
				if c.want[:3] == "cn:" {
					got = "cn:" + r.certCN
				}
				if got != c.want {
					ok = false
				}
			}
			kinds := []string{}
			for _, r := range recs {
				kinds = append(kinds, r.kind())
			}
			t.Logf("E10 exec with %s: state %s, read %d, program starts %d, server received %v (static credential each time %v)", c.name, o.State, s, fakeStarts(dir), kinds, ok)
			if o.State != StateConnected || fakeStarts(dir) != 0 || !ok {
				t.Errorf("E10 exec with %s: state %s, program starts %d, static credential sent %v; want connected, no run, only the static credential", c.name, o.State, fakeStarts(dir), ok)
			}
		})
	}

	t.Run("exec alone, refresh from token to certificate and back", func(t *testing.T) {
		srv := newCredServer(t)
		dir := t.TempDir()
		cert, key := certPair(t, "e10-refreshed")
		cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
		os.WriteFile(cf, cert, 0o600)
		os.WriteFile(kf, key, 0o600)
		env := fakeEnv(t, dir, "token=e10-first;expiry=3", "certfile="+cf+";keyfile="+kf, "token=e10-third")
		useKubeconfig(t, kubeconfig(srv.srv.URL, srv.caPEM(), execUser(prog, env, nil)))
		g := NewGeneration(context.Background(), credexecRunner(helperBinary(t), nil), nil)
		defer g.Close()
		o := g.Attempt("")
		if o.State != StateConnected {
			t.Fatalf("setup: state %s message %q", o.State, o.Message)
		}
		first := len(srv.seen())
		time.Sleep(3500 * time.Millisecond) // the token expires
		srv.reject("cn:e10-refreshed")      // the certificate is rejected, so the next run gives a token
		s, _ := read(t, o)
		recs := srv.seen()
		var kinds []string
		for _, r := range recs {
			kinds = append(kinds, r.kind())
		}
		t.Logf("E10 refresh: read %d; program starts %d; server received %v (the attempt's %d, then the read's)", s, fakeStarts(dir), kinds, first)
		after := recs[first:]
		if len(after) != 2 {
			t.Fatalf("E10 refresh: the read sent %d requests, want 2 (the certificate, then after its 401 the new token)", len(after))
		}
		if after[0].auth != "" || after[0].certCN != "e10-refreshed" {
			t.Errorf("E10 refresh: the first request carried %q, want only the certificate e10-refreshed (no old token)", after[0].kind())
		}
		if after[1].auth != "Bearer e10-third" || after[1].certCN != "" {
			t.Errorf("E10 refresh: the repeated request carried %q, want only the new token (no old certificate)", after[1].kind())
		}
	})
}
