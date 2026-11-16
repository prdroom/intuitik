package readonly

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"k8s.io/client-go/rest"
)

// Generation is one connection attempt and, when it ends connected, every read that uses
// its credential (packet M1-T3b E5, D2). It ends when the next attempt starts or intuitik
// stops (Close). For a context with an exec entry it holds the credential source: the
// current credential and at most one run of the credential program in progress.
//
// Locks have one order: the caller's cluster state lock, then the source lock (mu). No
// path of this type calls changed, or anything that changes cluster state, while it
// holds mu.
type Generation struct {
	ctx     context.Context
	cancel  context.CancelFunc
	run     Runner
	changed func(Outcome) // a state change after connected; called without mu
	start   time.Time     // the start of the attempt; set by Attempt before any run

	mu       sync.Mutex // the credential source lock
	open     bool
	prog     Program
	version  string      // the exec entry's apiVersion
	command  string      // the exec entry's command, for K-EP
	cur      *credential // the current credential
	flight   *flight     // the run in progress
	failed   string      // the K-EP reason of a failed run; "" while none failed
	reported bool        // a state change after connected was reported (EP-1)
	base     Outcome     // the connected outcome that state changes start from
}

// flight is one run of the program; every read that needs a run while it is in progress
// waits for it and gets its result (E6).
type flight struct {
	done chan struct{}
	got  obtained
}

// obtained is the result of asking the source for a credential.
type obtained struct {
	cred   *credential
	fail   string // the K-EP reason of a failed run
	closed bool   // the generation is closed: nothing ran, nothing is shown
}

// admitHooks are set only by the E5 tests: before runs when a run is about to take the
// source lock to start its process, started runs inside the lock right after the
// process started.
var admitHooks struct {
	before  func()
	started func()
}

var errGenerationClosed = errors.New("readonly: the generation is closed")

// NewGeneration makes the generation of one attempt. Its context lives until Close or
// the end of ctx. run starts credential programs; changed receives each state change
// after connected and is called at most once per generation.
func NewGeneration(ctx context.Context, run Runner, changed func(Outcome)) *Generation {
	gctx, cancel := context.WithCancel(ctx)
	return &Generation{ctx: gctx, cancel: cancel, run: run, changed: changed, open: true}
}

// Close ends the generation (E5). The caller has marked it closed under its cluster state
// lock; Close then takes the source lock, marks the source closed, so that no run starts
// any more, and cancels the generation's context, which ends every running program tree.
func (g *Generation) Close() {
	g.mu.Lock()
	g.open = false
	g.mu.Unlock()
	g.cancel()
}

// setProgram keeps the exec entry of the attempt's context in the source (D1).
func (g *Generation) setProgram(p Program, version, command string) {
	g.mu.Lock()
	g.prog, g.version, g.command = p, version, command
	g.mu.Unlock()
}

func expired(c *credential) bool {
	return !c.expiry.IsZero() && !time.Now().Before(c.expiry)
}

// current returns the credential for a request: the current one before its expiry,
// otherwise the result of a run. ctx is the attempt's context for the runs of the attempt
// (E4: they end at the attempt's W1 instant); nil is a read after connected, whose run
// has its own 10 s under the generation's context.
func (g *Generation) current(ctx context.Context) obtained {
	g.mu.Lock()
	if c := g.cur; c != nil && !expired(c) {
		g.mu.Unlock()
		return obtained{cred: c}
	}
	return g.runLocked(ctx)
}

// refresh replaces the credential used after a 401: a newer current credential is
// returned as it is; otherwise the program runs again.
func (g *Generation) refresh(ctx context.Context, used *credential) obtained {
	g.mu.Lock()
	if c := g.cur; c != nil && c != used && !expired(c) {
		g.mu.Unlock()
		return obtained{cred: c}
	}
	return g.runLocked(ctx)
}

// runLocked starts a run, or waits for the one in progress. It is called with mu held
// and returns with mu released. After a failed run the source does not run again: the
// generation keeps that failure until a retry or switch starts a new one (W2).
func (g *Generation) runLocked(ctx context.Context) obtained {
	if !g.open {
		g.mu.Unlock()
		return obtained{closed: true}
	}
	if g.failed != "" {
		f := g.failed
		g.mu.Unlock()
		return obtained{fail: f}
	}
	wait := g.ctx
	if ctx != nil {
		wait = ctx
	}
	if f := g.flight; f != nil {
		g.mu.Unlock()
		select {
		case <-f.done:
			return f.got
		case <-wait.Done():
			return obtained{fail: failReason("timeout", 0)}
		}
	}
	f := &flight{done: make(chan struct{})}
	g.flight = f
	prog, version := g.prog, g.version
	g.mu.Unlock()

	rctx, cancel := g.runContext(ctx)
	var res RunResult
	if g.run == nil {
		res = RunResult{Class: "not-found"}
	} else {
		res = g.run(rctx, prog, g.admit)
	}
	cancel()
	var got obtained
	switch {
	case res.Closed:
		got.closed = true
	case res.Class == "ok":
		if c, ok := parseCredential(res.Stdout, version); ok {
			got.cred = c
		} else {
			got.fail = failReason("invalid", 0)
		}
	default:
		got.fail = failReason(res.Class, res.Code)
	}

	g.mu.Lock()
	g.flight = nil
	switch {
	case got.fail != "":
		g.failed = got.fail
	case got.cred != nil:
		g.cur = got.cred
	}
	g.mu.Unlock()
	f.got = got
	close(f.done)
	return got
}

// runContext is the context of one run (E4): a run of the attempt runs under the
// attempt's context and ends at its W1 instant; every later run runs under the
// generation's context with its own 10 s from its own start.
func (g *Generation) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx != nil {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(g.ctx, AttemptTimeout)
}

// admit is the start gate of a run (E5): the process starts only inside the source lock
// and only while the source is open.
func (g *Generation) admit(start func() error) error {
	if h := admitHooks.before; h != nil {
		h()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.open {
		return errGenerationClosed
	}
	err := start()
	if h := admitHooks.started; h != nil && err == nil {
		h()
	}
	return err
}

// report prints a state change after connected once per generation (EP-1), if the
// generation is still open. It is called without mu held by its caller and calls
// changed without mu.
func (g *Generation) report(state State, id string, message func(base Outcome) string) {
	g.mu.Lock()
	if g.reported || !g.open || g.base.State != StateConnected {
		g.mu.Unlock()
		return
	}
	g.reported = true
	base := g.base
	g.mu.Unlock()
	if g.changed != nil {
		o := base.with(state, id, message(base))
		o.config, o.gen = nil, nil
		g.changed(o)
	}
}

// reportFailure is K-EP with the reason of the failed run.
func (g *Generation) reportFailure(reason string) {
	g.mu.Lock()
	command := g.command
	g.mu.Unlock()
	g.report(StateCredentialProgramFailed, "K-EP", func(base Outcome) string { return kEP(command, base.Context, reason) })
}

// reportRejected is K-AU after the repeated read was answered 401 again.
func (g *Generation) reportRejected() {
	g.report(StateCredentialsRejected, "K-AU", func(base Outcome) string { return kAU(base.Cluster, base.Context) })
}

// connected keeps the connected outcome of the attempt as the start of later changes.
func (g *Generation) connected(o Outcome) {
	g.mu.Lock()
	g.base = o
	g.mu.Unlock()
}

// client is the client of one read after connected (D4): the current credential, or a
// new run when it expired, on a dedicated copy of base. A failed run changes the state.
func (g *Generation) client(base *rest.Config) (*Client, error) {
	got := g.current(nil)
	switch {
	case got.closed:
		return nil, errGenerationClosed
	case got.fail != "":
		g.reportFailure(got.fail)
		return nil, errors.New("readonly: the credential program failed")
	}
	return newClient(g.credentialConfig(base, got.cred))
}

// credentialConfig is the guarded configuration of one credential, with the resend of
// E6 around the guard.
func (g *Generation) credentialConfig(base *rest.Config, cred *credential) *rest.Config {
	c := guarded(withCredential(base, cred))
	c.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &resend{g: g, base: base, cred: cred, next: rt}
	})
	return c
}

// resend handles a 401 of a read after connected (E6): one refresh run, then exactly one
// repeat of the read with a client built on the new credential. A second 401 is K-AU; a
// failed run is K-EP with its reason. The read gets the last answer.
type resend struct {
	g    *Generation
	base *rest.Config
	cred *credential
	next http.RoundTripper
}

func (r *resend) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.next.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	got := r.g.refresh(nil, r.cred)
	switch {
	case got.closed:
		return resp, nil
	case got.fail != "":
		r.g.reportFailure(got.fail)
		return resp, nil
	}
	rt, err := rest.TransportFor(guarded(withCredential(r.base, got.cred)))
	if err != nil {
		return resp, nil
	}
	resp.Body.Close()
	again := req.Clone(req.Context())
	again.Header.Del("Authorization")
	resp, err = rt.RoundTrip(again)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		r.g.reportRejected()
	}
	return resp, err
}
