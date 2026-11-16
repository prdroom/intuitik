package readonly

import "k8s.io/client-go/rest"

// Credential kinds of section 6.3: what the resolved configuration of the active
// context holds, computed before the request and never from its answer.
const (
	credentialNone              = "none"
	credentialBearerToken       = "bearer-token"
	credentialTokenFile         = "token-file"
	credentialClientCertificate = "client-certificate"
	credentialBasicAuth         = "basic-auth"
	credentialAuthProvider      = "auth-provider"
)

// supportedAuthProvider reports an auth provider that the client library supports.
// Only oidc is still built into client-go (imported in connect.go).
func supportedAuthProvider(name string) bool {
	return name == "oidc"
}

// credentialKind names the credential that cfg carries. An exec context reaches this
// point only with the credential of its program on a dedicated copy (withCredential).
func credentialKind(cfg *rest.Config) string {
	switch {
	case cfg.BearerTokenFile != "":
		return credentialTokenFile
	case cfg.BearerToken != "":
		return credentialBearerToken
	case (len(cfg.CertData) > 0 || cfg.CertFile != "") && (len(cfg.KeyData) > 0 || cfg.KeyFile != ""):
		return credentialClientCertificate
	case cfg.Username != "" && cfg.Password != "":
		return credentialBasicAuth
	case cfg.AuthProvider != nil && supportedAuthProvider(cfg.AuthProvider.Name):
		return credentialAuthProvider
	}
	return credentialNone
}
