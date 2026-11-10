package readonly

import "k8s.io/client-go/rest"

// Credential kinds of section 6.3.
const (
	credentialNone              = "none"
	credentialBearerToken       = "bearer-token"
	credentialTokenFile         = "token-file"
	credentialClientCertificate = "client-certificate"
	credentialBasicAuth         = "basic-auth"
	credentialAuthProvider      = "auth-provider"
)

// credentialKind is a RED STUB (packet section 5): it names no credential.
func credentialKind(cfg *rest.Config) string {
	return credentialNone
}
