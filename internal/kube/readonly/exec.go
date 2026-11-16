package readonly

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kjson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The ExecCredential protocol of client.authentication.k8s.io v1 and v1beta1 (packet
// M1-T3b D5), implemented here instead of client-go's exec authenticator, which intuitik
// never uses (E10). TestE9_ProtocolMatchesClientGo compares it with client-go.

const (
	execInfoEnv     = "KUBERNETES_EXEC_INFO"
	execKind        = "ExecCredential"
	execV1          = "client.authentication.k8s.io/v1"
	execV1beta1     = "client.authentication.k8s.io/v1beta1"
	execOutputLimit = 1 << 20 // credexec.MaxOutput
)

// Program is the run of a context's credential program: the exec entry's command and
// arguments, and its environment with KUBERNETES_EXEC_INFO, as NAME=value.
type Program struct {
	Command string
	Args    []string
	Env     []string
}

// RunResult is the outcome of one run as the runner reports it: Class is one of "ok",
// "exit", "not-found", "timeout" and "invalid"; Code is the exit code of "exit"; Stdout
// is the output of "ok". Closed means the run was not started because its generation
// was closed.
type RunResult struct {
	Class  string
	Code   int
	Stdout []byte
	Closed bool
}

// Runner runs a credential program under ctx. It starts the process only inside admit,
// which checks under the credential source lock that the generation is still open (E5).
type Runner func(ctx context.Context, p Program, admit func(start func() error) error) RunResult

// execCluster is spec.cluster of v1 and v1beta1.
type execCluster struct {
	Server                   string               `json:"server"`
	TLSServerName            string               `json:"tls-server-name,omitempty"`
	InsecureSkipTLSVerify    bool                 `json:"insecure-skip-tls-verify,omitempty"`
	CertificateAuthorityData []byte               `json:"certificate-authority-data,omitempty"`
	ProxyURL                 string               `json:"proxy-url,omitempty"`
	DisableCompression       bool                 `json:"disable-compression,omitempty"`
	Config                   runtime.RawExtension `json:"config,omitempty"`
}

type execSpec struct {
	Cluster     *execCluster `json:"cluster,omitempty"`
	Interactive bool         `json:"interactive"`
}

type execStatus struct {
	ExpirationTimestamp   *metav1.Time `json:"expirationTimestamp,omitempty"`
	Token                 string       `json:"token,omitempty"`
	ClientCertificateData string       `json:"clientCertificateData,omitempty"`
	ClientKeyData         string       `json:"clientKeyData,omitempty"`
}

type execCredential struct {
	metav1.TypeMeta `json:",inline"`
	Spec            execSpec    `json:"spec,omitempty"`
	Status          *execStatus `json:"status,omitempty"`
}

// execProgram builds the run of an exec entry. cfg is the client configuration that
// clientcmd made for the context, before its ExecProvider is removed; it gives
// spec.cluster when provideClusterInfo is set, as client-go fills it. spec.interactive
// is always false: intuitik gives the program no input (E2).
func execProgram(e *clientcmdapi.ExecConfig, cfg *rest.Config) (Program, error) {
	info := execCredential{TypeMeta: metav1.TypeMeta{APIVersion: e.APIVersion, Kind: execKind}}
	if e.ProvideClusterInfo {
		c, err := rest.ConfigToExecCluster(cfg)
		if err != nil {
			return Program{}, err
		}
		ec := &execCluster{
			Server:                   c.Server,
			TLSServerName:            c.TLSServerName,
			InsecureSkipTLSVerify:    c.InsecureSkipTLSVerify,
			CertificateAuthorityData: c.CertificateAuthorityData,
			ProxyURL:                 c.ProxyURL,
			DisableCompression:       c.DisableCompression,
		}
		if err := runtime.Convert_runtime_Object_To_runtime_RawExtension(&c.Config, &ec.Config, nil); err != nil {
			return Program{}, err
		}
		info.Spec.Cluster = ec
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return Program{}, err
	}
	p := Program{Command: e.Command, Args: append([]string(nil), e.Args...)}
	for _, v := range e.Env {
		p.Env = append(p.Env, v.Name+"="+v.Value)
	}
	p.Env = append(p.Env, execInfoEnv+"="+string(raw))
	return p, nil
}

// credential is one credential a run returned: a token, a client certificate and key,
// or both, and its expiry (zero: none).
type credential struct {
	token     string
	cert, key []byte
	expiry    time.Time
}

// kind names the credential for a test record, never its value.
func (c *credential) kind() string {
	switch {
	case c.token != "" && len(c.cert) > 0:
		return "token and certificate"
	case c.token != "":
		return "token"
	default:
		return "certificate"
	}
}

// parseCredential validates a program's stdout (E9): at most 1 MiB, an ExecCredential of
// apiVersion with a status that holds a token, a certificate-and-key pair that
// tls.X509KeyPair accepts, or both. A certificate without its key, or a key without its
// certificate, is invalid even with a token. No part of the output goes to any text.
func parseCredential(out []byte, apiVersion string) (*credential, bool) {
	if len(out) > execOutputLimit {
		return nil, false
	}
	data := bytes.TrimSpace(out)
	if len(data) == 0 {
		return nil, false
	}
	if data[0] != '{' {
		converted, err := yaml.ToJSON(data)
		if err != nil {
			return nil, false
		}
		data = converted
	}
	var cred execCredential
	if err := kjson.Unmarshal(data, &cred); err != nil {
		return nil, false
	}
	if cred.APIVersion != apiVersion || cred.Kind != execKind || cred.Status == nil {
		return nil, false
	}
	st := cred.Status
	if st.Token == "" && st.ClientCertificateData == "" && st.ClientKeyData == "" {
		return nil, false
	}
	if (st.ClientCertificateData == "") != (st.ClientKeyData == "") {
		return nil, false
	}
	c := &credential{token: st.Token}
	if st.ClientCertificateData != "" {
		if _, err := tls.X509KeyPair([]byte(st.ClientCertificateData), []byte(st.ClientKeyData)); err != nil {
			return nil, false
		}
		c.cert, c.key = []byte(st.ClientCertificateData), []byte(st.ClientKeyData)
	}
	if st.ExpirationTimestamp != nil {
		c.expiry = st.ExpirationTimestamp.Time
	}
	return c, true
}

// staticCredential reports a user entry that holds a credential of its own: a token, a
// token file, a username or a client certificate with its key. Such an entry does not
// use its exec entry, as in client-go and kubectl (E10, SC).
func staticCredential(u *clientcmdapi.AuthInfo) bool {
	return u.Token != "" || u.TokenFile != "" || u.Username != "" ||
		((u.ClientCertificate != "" || len(u.ClientCertificateData) > 0) && (u.ClientKey != "" || len(u.ClientKeyData) > 0))
}

// withCredential is a dedicated copy of cfg that carries only cred (E10): every
// competing field is cleared first.
func withCredential(cfg *rest.Config, cred *credential) *rest.Config {
	c := rest.CopyConfig(cfg)
	c.BearerToken = ""
	c.BearerTokenFile = ""
	c.Username, c.Password = "", ""
	c.CertData, c.KeyData, c.CertFile, c.KeyFile = nil, nil, "", ""
	c.ExecProvider, c.AuthProvider = nil, nil
	c.BearerToken = cred.token
	if len(cred.cert) > 0 {
		c.CertData, c.KeyData = append([]byte(nil), cred.cert...), append([]byte(nil), cred.key...)
	}
	return c
}

// failReason is the <reason> of K-EP (EP-R) for a run's class, or for the class
// "interactive" of an exec entry with interactiveMode Always, decided before any run.
func failReason(class string, code int) string {
	switch class {
	case "exit":
		return fmt.Sprintf("exit code %d", code)
	case "not-found":
		return "not found"
	case "timeout":
		return textTimeout
	case "interactive":
		return "needs input from a terminal"
	}
	return "no valid credential in its output"
}
