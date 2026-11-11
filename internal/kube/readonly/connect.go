// Package readonly is the only package of intuitik that reads the kubeconfig and calls
// the API server (packet M1-T3a, M1_TICKETS 7.1). It never writes a kubeconfig file and
// never runs a credential program. internal/server gets a client only through its
// cluster gate (NewClient, called in internal/server/cluster_gate.go).
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
	StateConnecting                   State = "connecting"
	StateConnected                    State = "connected"
	StateNoKubeconfig                 State = "no-kubeconfig"
	StateKubeconfigInvalid            State = "kubeconfig-invalid"
	StateCredentialsRejected          State = "credentials-rejected"
	StateCredentialsMissing           State = "credentials-missing"
	StateCredentialProgramUnsupported State = "credential-program-unsupported"
	StateAuthProviderUnsupported      State = "auth-provider-unsupported"
	StateCertificateMismatch          State = "certificate-mismatch"
	StateUnreachable                  State = "unreachable"
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

	// config is set only in the connected state; the gate builds the client from it.
	config *rest.Config
}

// Stdout reports whether the text goes to stdout (K-OK) rather than stderr.
func (o Outcome) Stdout() bool {
	return o.State == StateConnected
}

func (o Outcome) with(state State, id, message string) Outcome {
	o.State, o.TextID, o.Message = state, id, message
	return o
}

// Attempt runs the loading rules again, reads the kubeconfig files anew (W2) and
// connects to the named context, or to the kubeconfig's current context when name is
// "". It sends one authenticated GET /api (W3). W1 starts here and covers the whole
// attempt: reading the files, building the configuration and the request. When it
// passes, the outcome is K-UR with what the attempt knew by then, even while a file
// read is still blocked.
func Attempt(ctx context.Context, name string) Outcome {
	actx, cancel := context.WithTimeout(ctx, AttemptTimeout)
	defer cancel()
	p := &progress{}
	done := make(chan Outcome, 1)
	go func() { done <- attempt(ctx, actx, name, p) }()
	select {
	case o := <-done:
		return o
	case <-actx.Done():
	}
	select {
	case o := <-done:
		return o
	default:
	}
	o := p.get()
	if ctx.Err() != nil {
		o.Canceled = true
		return o
	}
	return o.with(StateUnreachable, "K-UR", text(textUR, o.Server, o.Context, textTimeout))
}

// progress is what a running attempt knows so far, for the outcome of W1.
type progress struct {
	mu sync.Mutex
	o  Outcome
}

func (p *progress) set(o Outcome) {
	p.mu.Lock()
	p.o = o
	p.mu.Unlock()
}

func (p *progress) get() Outcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.o
}

// attempt is the body of Attempt. ctx is the caller's context (a cancel means a newer
// attempt or the stop); actx adds the W1 deadline.
func attempt(ctx, actx context.Context, name string, p *progress) Outcome {
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
		return o.with(StateKubeconfigInvalid, "K-IV2", text(textIV2, o.Contexts[0].Name))
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
		return o.with(StateKubeconfigInvalid, "K-IV3", text(textIV3, name))
	}
	// Decided before any client is built: the exec entry never reaches client-go, so
	// the credential program is never started (section 6.7).
	if user.Exec != nil {
		return o.with(StateCredentialProgramUnsupported, "K-EX", text(textEX, name, user.Exec.Command))
	}
	if user.AuthProvider != nil && !supportedAuthProvider(user.AuthProvider.Name) {
		return o.with(StateAuthProviderUnsupported, "K-AP", text(textAP, name, user.AuthProvider.Name))
	}
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*merged, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return o.with(StateKubeconfigInvalid, "K-IV1", text(textIV1, origin(kctx, found[0]), err.Error()))
	}
	return probe(ctx, actx, o, guarded(cfg), origin(kctx, found[0]))
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
			return o.with(StateUnreachable, "K-UR", text(textUR, o.Server, o.Context, textTimeout))
		}
		if msg, ok := tlsVerification(err); ok {
			return o.with(StateCertificateMismatch, "K-TL", text(textTL, o.Server, o.Context, msg))
		}
		return o.with(StateUnreachable, "K-UR", text(textUR, o.Server, o.Context, osError(err)))
	}
	// The status line and headers are the answer; the body is never read, so a body
	// that the server holds cannot turn an answer into K-UR. Closing it unread ends the
	// connection.
	resp.Body.Close()
	if kind == credentialNone {
		// An anonymous request: any HTTP answer, 200 included (P-a).
		return o.with(StateCredentialsMissing, "K-NC", text(textNC, o.Context, o.Cluster))
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusForbidden:
		// 403 with credentials is connected: permissions are T7a.
		o.config = cfg
		return o.with(StateConnected, "K-OK", text(textOK, o.Cluster, o.Context))
	case http.StatusUnauthorized:
		return o.with(StateCredentialsRejected, "K-AU", text(textAU, o.Cluster, o.Context))
	default:
		return o.with(StateUnreachable, "K-HS", text(textHS, o.Server, o.Context, resp.StatusCode))
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
