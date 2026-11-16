// Package readonly is the only package of intuitik that reads the kubeconfig and calls
// the API server (packet M1-T3a, M1_TICKETS 7.1). It never writes a kubeconfig file. A
// credential program of an exec entry runs only through the Runner that the caller
// gives a Generation (packet M1-T3b); client-go's exec authenticator is never used.
// internal/server gets a client only through its cluster gate (NewClient, called in
// internal/server/cluster_gate.go).
package readonly

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	// The auth provider that the client library still supports (section 3.2, K-AP).
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

// AttemptTimeout is W1: from the start of an attempt to the first authenticated answer.
const AttemptTimeout = 10 * time.Second

// State is one connection state of packet section 3.2.
type State string

const (
	StateConnecting              State = "connecting"
	StateConnected               State = "connected"
	StateNoKubeconfig            State = "no-kubeconfig"
	StateKubeconfigInvalid       State = "kubeconfig-invalid"
	StateCredentialsRejected     State = "credentials-rejected"
	StateCredentialsMissing      State = "credentials-missing"
	StateCredentialProgramFailed State = "credential-program-failed"
	StateAuthProviderUnsupported State = "auth-provider-unsupported"
	StateCertificateMismatch     State = "certificate-mismatch"
	StateUnreachable             State = "unreachable"
)

// ContextEntry is one context of the merged kubeconfig.
type ContextEntry struct {
	Name    string
	Cluster string
	Server  string
}

// Outcome is the result of one connection attempt.
type Outcome struct {
	State    State
	TextID   string // the section 3.3 id
	Message  string // the exact section 3.3 text
	Context  string // "" where unknown
	Cluster  string
	Server   string
	Paths    []string       // the files the loading rules looked at, in loading order (W5)
	Contexts []ContextEntry // every context of the merged kubeconfig, in loading order
	Current  string         // the kubeconfig's current context, "" if none
	// Canceled is true when a newer attempt or the stop ended this one; its result
	// is never shown.
	Canceled bool

	// config is set only in the connected state; the gate builds the client from it. For
	// an exec context it holds no credential, and gen gives each read its credential.
	config *rest.Config
	gen    *Generation
}

// Stdout reports whether the text goes to stdout (K-OK) rather than stderr.
func (o Outcome) Stdout() bool {
	return o.State == StateConnected
}

func (o Outcome) with(state State, id, message string) Outcome {
	o.State, o.TextID, o.Message = state, id, message
	return o
}

// Attempt is one attempt in a generation of its own that runs no credential program; an
// exec context then fails with K-EP "not found".
func Attempt(ctx context.Context, name string) Outcome {
	return NewGeneration(ctx, nil, nil).Attempt(name)
}

// Attempt runs the loading rules again, reads the kubeconfig files anew (W2) and
// connects to the named context, or to the kubeconfig's current context when name is
// "". It sends one authenticated GET /api (W3). W1 starts here and covers the whole
// attempt: reading the files, building the configuration, the first run of a
// credential program and the request. When it passes, the outcome is K-EP "no answer
// within 10 s" while that run is in progress, and otherwise K-UR with what the attempt
// knew by then, even while a file read is still blocked (E14). A cancel of the
// generation means a newer attempt or the stop.
func (g *Generation) Attempt(name string) Outcome {
	g.start = time.Now()
	actx, cancel := context.WithTimeout(g.ctx, AttemptTimeout)
	defer cancel()
	p := &progress{}
	done := make(chan Outcome, 1)
	go func() { done <- attempt(g.ctx, actx, name, p, g) }()
	select {
	case o := <-done:
		return g.finish(o)
	case <-actx.Done():
	}
	select {
	case o := <-done:
		return g.finish(o)
	default:
	}
	// W1 has passed: a late read of this attempt starts no program (D7).
	g.mu.Lock()
	if g.failed == "" {
		g.failed = failReason("timeout", 0)
	}
	g.mu.Unlock()
	o, read, running := p.get()
	if g.ctx.Err() != nil {
		o.Canceled = true
		return o
	}
	if running != "" {
		return o.with(StateCredentialProgramFailed, "K-EP", kEP(running, o.Context, failReason("timeout", 0)))
	}
	osErr := textTimeout
	if !read {
		osErr = textKubeconfigTimeout
		if o.Context == "" {
			o.Context = name
		}
	}
	return o.with(StateUnreachable, "K-UR", kUR(o.Server, o.Context, osErr))
}

// finish keeps a connected outcome as the start of the state changes after it.
func (g *Generation) finish(o Outcome) Outcome {
	if o.State == StateConnected && !o.Canceled {
		g.connected(o)
	}
	return o
}

// progress is what a running attempt knows so far, for the outcome of W1: the outcome
// so far, whether the kubeconfig was read, and the command of a first run in progress.
type progress struct {
	mu      sync.Mutex
	o       Outcome
	read    bool
	running string
}

func (p *progress) set(o Outcome) {
	p.mu.Lock()
	p.o = o
	p.mu.Unlock()
}

func (p *progress) setRead() {
	p.mu.Lock()
	p.read = true
	p.mu.Unlock()
}

func (p *progress) setRunning(command string) {
	p.mu.Lock()
	p.running = command
	p.mu.Unlock()
}

func (p *progress) get() (Outcome, bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.o, p.read, p.running
}

// attempt is the body of Attempt. ctx is the generation's context (a cancel means a
// newer attempt or the stop); actx adds the W1 deadline.
func attempt(ctx, actx context.Context, name string, p *progress, g *Generation) Outcome {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.MigrationRules = nil // W4: the loading rules never copy or write a file
	rules.Warner = func(error) {}
	o := Outcome{Paths: rules.GetLoadingPrecedence()}
	p.set(o)

	var found, order []string
	for _, p := range o.Paths {
		if p == "" {
			continue
		}
		raw, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, p, err.Error()))
		}
		if _, err := clientcmd.Load(raw); err != nil {
			return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, p, err.Error()))
		}
		found = append(found, p)
		order = append(order, contextOrder(raw)...)
	}
	if len(found) == 0 {
		return o.with(StateNoKubeconfig, "K-NK", text(textNK, strings.Join(o.Paths, ", ")))
	}
	merged, err := rules.Load()
	if err != nil {
		return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, found[0], err.Error()))
	}
	p.setRead()
	o.Contexts = contextList(merged, order)
	o.Current = merged.CurrentContext
	p.set(o)
	if len(o.Contexts) == 0 {
		return o.with(StateKubeconfigInvalid, "K-IV4", text(textIV4, found[0]))
	}
	if name == "" {
		name = merged.CurrentContext
	}
	kctx := merged.Contexts[name]
	if name == "" || kctx == nil {
		return o.with(StateKubeconfigInvalid, "K-IV2", text(textIV2, slot(o.Contexts[0].Name)))
	}
	o.Context, o.Cluster = name, kctx.Cluster
	cluster := merged.Clusters[kctx.Cluster]
	if cluster != nil {
		o.Server = cluster.Server
	}
	p.set(o)
	user :=&clientcmdapi.AuthInfo{} // a context without a user entry has no credentials
	if kctx.AuthInfo != "" {
		user = merged.AuthInfos[kctx.AuthInfo]
	}
	if cluster == nil || user == nil {
		return o.with(StateKubeconfigInvalid, "K-IV3", kIV3(name))
	}
	// D1, E10: a user entry with a static credential uses it and never its exec entry,
	// as client-go and kubectl do. Otherwise an exec entry with interactiveMode Always
	// is refused before any run (E2).
	execEntry := user.Exec != nil && !staticCredential(user)
	if execEntry && user.Exec.InteractiveMode == clientcmdapi.AlwaysExecInteractiveMode {
		return o.with(StateCredentialProgramFailed, "K-EP", kEP(user.Exec.Command, name, failReason("interactive", 0)))
	}
	if user.AuthProvider != nil && !supportedAuthProvider(user.AuthProvider.Name) {
		return o.with(StateAuthProviderUnsupported, "K-AP", kAP(name, user.AuthProvider.Name))
	}
	path := origin(kctx, found[0])
	// client-go's validation of the exec entry still gives K-IV1.
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*merged, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, path, err.Error()))
	}
	if !execEntry {
		cfg.ExecProvider = nil // E10: no rest.Config of intuitik carries an exec provider
		return probe(ctx, actx, o, guarded(cfg), path)
	}
	prog, err := execProgram(cfg.ExecProvider, cfg)
	if err != nil {
		return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, path, err.Error()))
	}
	version := cfg.ExecProvider.APIVersion
	cfg.ExecProvider = nil
	g.setProgram(prog, version, user.Exec.Command)
	return probeExec(ctx, actx, o, cfg, path, g, user.Exec.Command, p)
}

// probeExec is the probe of an exec context (D4): the first run under the attempt's
// context, the probe with its credential and, on a 401, one refresh and one repeat
// inside the same W1.
func probeExec(ctx, actx context.Context, o Outcome, cfg *rest.Config, path string, g *Generation, command string, p *progress) Outcome {
	p.setRunning(command)
	got := g.current(actx)
	p.setRunning("")
	if ctx.Err() != nil || got.closed {
		o.Canceled = true
		return o
	}
	if got.fail != "" {
		return o.with(StateCredentialProgramFailed, "K-EP", kEP(command, o.Context, got.fail))
	}
	out := probe(ctx, actx, o, guarded(withCredential(cfg, got.cred)), path)
	if out.State == StateCredentialsRejected && !out.Canceled {
		p.setRunning(command)
		got = g.refresh(actx, got.cred)
		p.setRunning("")
		if ctx.Err() != nil || got.closed {
			o.Canceled = true
			return o
		}
		if got.fail != "" {
			return o.with(StateCredentialProgramFailed, "K-EP", kEP(command, o.Context, got.fail))
		}
		out = probe(ctx, actx, o, guarded(withCredential(cfg, got.cred)), path)
	}
	if out.State == StateConnected {
		out.config, out.gen = cfg, g
	}
	return out
}

// probe sends the one authenticated GET /api of an attempt and classifies its answer
// (section 6.3). The request ends at the deadline of actx, which the attempt started.
func probe(ctx, actx context.Context, o Outcome, cfg *rest.Config, path string) Outcome {
	kind := credentialKind(cfg)
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, path, err.Error()))
	}
	defer client.CloseIdleConnections()
	// The first answer is classified as it is: a redirect is never followed (W3).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(actx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/api", nil)
	if err != nil {
		return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, path, err.Error()))
	}
	resp, err := client.Do(req)
	if ctx.Err() != nil {
		o.Canceled = true
		return o
	}
	if err != nil {
		if actx.Err() != nil {
			return o.with(StateUnreachable, "K-UR", kUR(o.Server, o.Context, textTimeout))
		}
		if msg, ok := tlsVerification(err); ok {
			return o.with(StateCertificateMismatch, "K-TL", kTL(o.Server, o.Context, msg))
		}
		return o.with(StateUnreachable, "K-UR", kUR(o.Server, o.Context, osError(err)))
	}
	// The status line and headers are the answer; the body is never read, so a body
	// that the server holds cannot turn an answer into K-UR. Closing it unread ends the
	// connection.
	resp.Body.Close()
	if kind == credentialNone {
		// An anonymous request: any HTTP answer, 200 included (P-a).
		return o.with(StateCredentialsMissing, "K-NC", kNC(o.Context, o.Cluster))
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusForbidden:
		// 403 with credentials is connected: permissions are T7a.
		o.config = cfg
		return o.with(StateConnected, "K-OK", kOK(o.Cluster, o.Context))
	case http.StatusUnauthorized:
		return o.with(StateCredentialsRejected, "K-AU", kAU(o.Cluster, o.Context))
	default:
		return o.with(StateUnreachable, "K-HS", kHS(o.Server, o.Context, resp.StatusCode))
	}
}

// contextOrder returns the context names of one kubeconfig file in file order.
func contextOrder(raw []byte) []string {
	var doc struct {
		Contexts []struct {
			Name string `json:"name"`
		} `json:"contexts"`
	}
	if yaml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var names []string
	for _, c := range doc.Contexts {
		names = append(names, c.Name)
	}
	return names
}

// contextList lists the merged contexts in loading order; the first file that defines
// a name wins, as in the merge of the loading rules.
func contextList(merged *clientcmdapi.Config, order []string) []ContextEntry {
	var list []ContextEntry
	seen := map[string]bool{}
	for _, name := range order {
		kctx := merged.Contexts[name]
		if seen[name] || kctx == nil {
			continue
		}
		seen[name] = true
		e := ContextEntry{Name: name, Cluster: kctx.Cluster}
		if c := merged.Clusters[kctx.Cluster]; c != nil {
			e.Server = c.Server
		}
		list = append(list, e)
	}
	return list
}

func origin(kctx *clientcmdapi.Context, fallback string) string {
	if kctx.LocationOfOrigin != "" {
		return kctx.LocationOfOrigin
	}
	return fallback
}

// tlsVerification returns the TLS verification message of a failed request.
func tlsVerification(err error) (string, bool) {
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		return cve.Err.Error(), true
	}
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	switch {
	case errors.As(err, &ua):
		return ua.Error(), true
	case errors.As(err, &he):
		return he.Error(), true
	case errors.As(err, &ci):
		return ci.Error(), true
	}
	return "", false
}

// osError is the operating-system part of a transport error, for example
// "connect: connection refused".
func osError(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return err.Error()
}
