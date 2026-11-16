package readonly

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// e6Setup is a connected exec context on the fake TLS server whose program answers come
// from runner. It returns the connected outcome and the state changes it reports.
func e6Setup(t *testing.T, srv *credServer, runner *listRunner) (Outcome, *changes) {
	t.Helper()
	useKubeconfig(t, kubeconfig(srv.srv.URL, srv.caPEM(), execUser("/bin/credential-program", nil, nil)))
	ch := &changes{}
	g := NewGeneration(context.Background(), runner.run, ch.add)
	t.Cleanup(g.Close)
	o := g.Attempt("")
	if o.State != StateConnected {
		t.Fatalf("setup: the exec context did not connect: state %s message %q", o.State, o.Message)
	}
	return o, ch
}

func okToken(token string, expiry time.Duration) RunResult {
	st := map[string]any{"token": token}
	if expiry != 0 {
		st["expirationTimestamp"] = time.Now().Add(expiry).UTC().Format(time.RFC3339)
	}
	return RunResult{Class: "ok", Stdout: execOutput(execV1, st)}
}

func okCert(cert, key []byte) RunResult {
	return RunResult{Class: "ok", Stdout: execOutput(execV1, map[string]any{"clientCertificateData": string(cert), "clientKeyData": string(key)})}
}

// auths are the Authorization headers of the records from index from.
func auths(records []credRecord, from int) []string {
	var out []string
	for _, r := range records[from:] {
		out = append(out, r.auth)
	}
	return out
}

// TestE6_RefreshContract (packet M1-T3b E6): per generation, the credential cache, the
// refresh on expiry and after a 401, the single repeat of a read, K-AU and K-EP once, a
// new TLS handshake for a changed client certificate, and one run for reads that wait
// together, for a success and a failure. Each case records what the fake server
// received (credential kind) and how many runs happened.
func TestE6_RefreshContract(t *testing.T) {
	t.Run("reused before expiry", func(t *testing.T) {
		srv := newCredServer(t)
		runner := &listRunner{answers: []RunResult{okToken("e6-token-a", time.Hour)}}
		o, _ := e6Setup(t, srv, runner)
		s1, err1 := read(t, o)
		s2, err2 := read(t, o)
		t.Logf("E6 reuse: reads %d %v, %d %v; runs %d", s1, err1, s2, err2, runner.count())
		if s1 != 200 || s2 != 200 || runner.count() != 1 {
			t.Errorf("E6 reuse: reads %d %d, runs %d; want 200 200 and 1 run", s1, s2, runner.count())
		}
	})

	t.Run("replaced after expiry", func(t *testing.T) {
		srv := newCredServer(t)
		runner := &listRunner{answers: []RunResult{okToken("e6-token-a", 2*time.Second), okToken("e6-token-b", time.Hour)}}
		o, _ := e6Setup(t, srv, runner)
		before := len(srv.seen())
		time.Sleep(2500 * time.Millisecond)
		s, err := read(t, o)
		got := auths(srv.seen(), before)
		t.Logf("E6 expiry: read %d %v; runs %d; server received %d requests after the expiry", s, err, runner.count(), len(got))
		if s != 200 || runner.count() != 2 || len(got) != 1 || got[0] != "Bearer e6-token-b" {
			t.Errorf("E6 expiry: read %d, runs %d, the read used the new credential %v (%d requests); want 200, 2 runs, one request with the new credential",
				s, runner.count(), len(got) == 1 && got[0] == "Bearer e6-token-b", len(got))
		}
	})

	t.Run("401 refreshes once and repeats once", func(t *testing.T) {
		srv := newCredServer(t)
		runner := &listRunner{answers: []RunResult{okToken("e6-token-a", 0), okToken("e6-token-b", 0)}}
		o, ch := e6Setup(t, srv, runner)
		srv.reject("Bearer e6-token-a")
		before := len(srv.seen())
		s, err := read(t, o)
		got := auths(srv.seen(), before)
		t.Logf("E6 401: read %d %v; runs %d; requests of the read %d, first rejected credential then the refreshed one %v; state changes %d",
			s, err, runner.count(), len(got), len(got) == 2 && got[0] == "Bearer e6-token-a" && got[1] == "Bearer e6-token-b", len(ch.get()))
		if s != 200 || runner.count() != 2 || len(got) != 2 || got[0] != "Bearer e6-token-a" || got[1] != "Bearer e6-token-b" || len(ch.get()) != 0 {
			t.Errorf("E6 401: read %d, runs %d, %d requests, state changes %d; want 200, 2 runs, the read sent twice (old then new credential) and no state change",
				s, runner.count(), len(got), len(ch.get()))
		}
	})

	t.Run("401 again is K-AU once", func(t *testing.T) {
		srv := newCredServer(t)
		runner := &listRunner{answers: []RunResult{okToken("e6-token-a", 0), okToken("e6-token-b", 0)}}
		o, ch := e6Setup(t, srv, runner)
		srv.reject("Bearer e6-token-a", "Bearer e6-token-b")
		before := len(srv.seen())
		s1, _ := read(t, o)
		s2, _ := read(t, o)
		changes := ch.get()
		want := kAU(o.Cluster, o.Context)
		t.Logf("E6 rejected: reads %d %d; runs %d; requests %d; state changes %d", s1, s2, runner.count(), len(srv.seen())-before, len(changes))
		if len(changes) != 1 || changes[0].State != StateCredentialsRejected || changes[0].Message != want {
			t.Errorf("E6 rejected: state changes %v, want exactly one credentials-rejected with %q", changes, want)
		}
		if n := len(srv.seen()) - before; n > 4 {
			t.Errorf("E6 rejected: the server received %d requests for 2 reads, want at most 2 per read", n)
		}
	})

	t.Run("failed refresh run is K-EP once and no run after it", func(t *testing.T) {
		srv := newCredServer(t)
		runner := &listRunner{answers: []RunResult{okToken("e6-token-a", 0), {Class: "exit", Code: 2}}}
		o, ch := e6Setup(t, srv, runner)
		srv.reject("Bearer e6-token-a")
		read(t, o)
		_, err := read(t, o)
		changes := ch.get()
		want := kEP("/bin/credential-program", o.Context, "exit code 2")
		t.Logf("E6 failed run: second read error %v; runs %d; state changes %d", err != nil, runner.count(), len(changes))
		if len(changes) != 1 || changes[0].State != StateCredentialProgramFailed || changes[0].Message != want {
			t.Errorf("E6 failed run: state changes %v, want exactly one credential-program-failed with %q", changes, want)
		}
		if runner.count() != 2 {
			t.Errorf("E6 failed run: %d runs, want 2 (no run after the failed one)", runner.count())
		}
	})

	t.Run("changed client certificate is a new handshake", func(t *testing.T) {
		srv := newCredServer(t)
		c1, k1 := certPair(t, "e6-cert-one")
		c2, k2 := certPair(t, "e6-cert-two")
		runner := &listRunner{answers: []RunResult{okCert(c1, k1), okCert(c2, k2)}}
		o, _ := e6Setup(t, srv, runner)
		srv.reject("cn:e6-cert-one")
		before, hs := len(srv.seen()), srv.handshakes.Load()
		s, err := read(t, o)
		recs := srv.seen()[before:]
		var cns []string
		for _, r := range recs {
			cns = append(cns, r.certCN)
		}
		t.Logf("E6 certificate: read %d %v; certificates received %v; new TLS handshakes %d", s, err, cns, srv.handshakes.Load()-hs)
		if s != 200 || len(cns) != 2 || cns[0] != "e6-cert-one" || cns[1] != "e6-cert-two" || srv.handshakes.Load()-hs < 2 {
			t.Errorf("E6 certificate: read %d, certificates %v, handshakes %d; want 200, [e6-cert-one e6-cert-two] and a new handshake", s, cns, srv.handshakes.Load()-hs)
		}
	})

	for _, fail := range []bool{false, true} {
		name := "parallel reads share one run (success)"
		second := okToken("e6-token-b", time.Hour)
		if fail {
			name = "parallel reads share one run (failure)"
			second = RunResult{Class: "exit", Code: 5}
		}
		t.Run(name, func(t *testing.T) {
			srv := newCredServer(t)
			runner := &listRunner{answers: []RunResult{okToken("e6-token-a", 2*time.Second), second}}
			o, ch := e6Setup(t, srv, runner)
			time.Sleep(2500 * time.Millisecond)
			runner.mu.Lock()
			runner.delay = time.Second
			runner.mu.Unlock()
			var wg sync.WaitGroup
			results := make([]string, 2)
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s, err := read(t, o)
					results[i] = strings.TrimSpace(map[bool]string{true: "error", false: "ok"}[err != nil] + " " + map[bool]string{true: "200", false: ""}[s == 200])
				}()
			}
			wg.Wait()
			t.Logf("E6 %s: reads %v; runs %d; state changes %d", name, results, runner.count(), len(ch.get()))
			if runner.count() != 2 {
				t.Errorf("E6 %s: %d runs, want 2 (one refresh for both reads)", name, runner.count())
			}
			for i, r := range results {
				if (fail && r != "error") || (!fail && r != "ok 200") {
					t.Errorf("E6 %s: read %d got %q, want both reads to get the run's result", name, i, r)
				}
			}
			if fail && len(ch.get()) != 1 {
				t.Errorf("E6 %s: %d state changes, want one K-EP", name, len(ch.get()))
			}
		})
	}
}
