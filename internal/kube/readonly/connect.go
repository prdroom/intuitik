// Package readonly is the only package of intuitik that reads the kubeconfig and calls
// the API server (packet M1-T3a, M1_TICKETS 7.1).
//
// RED STUB (packet M1-T3a section 5): this commit carries a compiling stub so that the
// new tests run and fail for their own reasons. It keeps the first loaded kubeconfig
// (W2), counts any anonymous answer as connected (W3), waits 30 s (W1), hands the exec
// entry to client-go and prints it (6.7, D3), and reports texts that differ from 3.3.
// The green commit replaces it.
package readonly

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// AttemptTimeout is W1 in the green commit; the stub waits longer.
const AttemptTimeout = 10 * time.Second

const stubTimeout = 30 * time.Second

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
	TextID   string
	Message  string
	Context  string
	Cluster  string
	Server   string
	Paths    []string
	Contexts []ContextEntry
	Current  string
	Canceled bool

	config *rest.Config
}

// Stdout reports whether the text goes to stdout; the stub prints everything on stderr.
func (o Outcome) Stdout() bool {
	return false
}

func (o Outcome) with(state State, id, message string) Outcome {
	o.State, o.TextID, o.Message = state, id, message
	return o
}

var (
	stubMu     sync.Mutex
	stubLoaded bool
	stubConfig *clientcmdapi.Config
)

// Attempt is the stub attempt.
func Attempt(ctx context.Context, name string) Outcome {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.MigrationRules = nil
	o := Outcome{Paths: rules.GetLoadingPrecedence()}
	stubMu.Lock()
	if !stubLoaded {
		stubConfig, _ = rules.Load()
		stubLoaded = true
	}
	merged := stubConfig
	stubMu.Unlock()
	if merged == nil || len(merged.Contexts) == 0 {
		return o.with(StateUnreachable, "K-STUB", "stub: the first loaded kubeconfig has no context")
	}
	for n, c := range merged.Contexts {
		e := ContextEntry{Name: n, Cluster: c.Cluster}
		if cl := merged.Clusters[c.Cluster]; cl != nil {
			e.Server = cl.Server
		}
		o.Contexts = append(o.Contexts, e)
	}
	sort.Slice(o.Contexts, func(i, j int) bool { return o.Contexts[i].Name < o.Contexts[j].Name })
	o.Current = merged.CurrentContext
	if name == "" {
		name = merged.CurrentContext
	}
	kctx := merged.Contexts[name]
	if kctx == nil {
		return o.with(StateUnreachable, "K-STUB", "stub: no context "+name)
	}
	o.Context, o.Cluster = name, kctx.Cluster
	if cl := merged.Clusters[kctx.Cluster]; cl != nil {
		o.Server = cl.Server
	}
	var notes []string
	cfg, cfgErr := clientcmd.NewNonInteractiveClientConfig(*merged, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if user := merged.AuthInfos[kctx.AuthInfo]; user != nil && user.Exec != nil {
		var env []string
		for _, e := range user.Exec.Env {
			env = append(env, e.Name+"="+e.Value)
		}
		notes = append(notes, fmt.Sprintf("exec entry command %s args %v env %v", user.Exec.Command, user.Exec.Args, env))
		if cfgErr == nil {
			result := "no client"
			if hc, err := rest.HTTPClientFor(cfg); err == nil {
				resp, err := hc.Get(strings.TrimRight(cfg.Host, "/") + "/api")
				result = fmt.Sprint(err)
				if err == nil {
					result = resp.Status
					resp.Body.Close()
				}
			}
			notes = append(notes, "GET /api sent with the exec entry: "+result)
		}
	}
	client := &http.Client{Timeout: stubTimeout, Transport: &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		TLSHandshakeTimeout: stubTimeout,
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(o.Server, "/")+"/version", nil)
	resp, err := client.Do(req)
	if err != nil {
		return o.with(StateUnreachable, "K-STUB", fmt.Sprintf("stub: %v %s", err, strings.Join(notes, "; ")))
	}
	resp.Body.Close()
	if cfgErr == nil {
		o.config = cfg
	}
	return o.with(StateConnected, "K-STUB", fmt.Sprintf("stub: anonymous GET /version answered %s, counted as connected to %s (%s) %s", resp.Status, o.Cluster, name, strings.Join(notes, "; ")))
}
