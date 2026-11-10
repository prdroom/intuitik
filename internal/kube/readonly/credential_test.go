package readonly

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
)

// TestK_CredentialKind (packet M1-T3a section 6.3): credentialKind names the credential
// of the resolved configuration, one fixture per kind and a context with none.
func TestK_CredentialKind(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(randomValue(t, "tf-")), 0o600); err != nil {
		t.Fatal(err)
	}
	pemish := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name string
		user map[string]any
		want string
	}{
		{"bearer token", map[string]any{"token": randomValue(t, "tk-")}, credentialBearerToken},
		{"token file", map[string]any{"tokenFile": tokenFile}, credentialTokenFile},
		{"client certificate", map[string]any{"client-certificate-data": pemish("certificate"), "client-key-data": pemish(randomValue(t, "k-"))}, credentialClientCertificate},
		{"basic auth", map[string]any{"username": "user", "password": randomValue(t, "pw-")}, credentialBasicAuth},
		{"auth provider oidc", map[string]any{"auth-provider": map[string]any{"name": "oidc", "config": map[string]any{"client-id": "intuitik", "idp-issuer-url": "https://issuer.invalid"}}}, credentialAuthProvider},
		{"none", map[string]any{}, credentialNone},
		{"client certificate without key", map[string]any{"client-certificate-data": pemish("certificate")}, credentialNone},
		{"username without password", map[string]any{"username": "user"}, credentialNone},
	}
	for _, c := range cases {
		path := useKubeconfig(t, kubeconfig("https://127.0.0.1:6443", nil, c.user))
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			t.Errorf("K-CRED %s: building the configuration: %v", c.name, err)
			continue
		}
		got := credentialKind(cfg)
		t.Logf("K-CRED %s: expected %s, actual %s", c.name, c.want, got)
		if got != c.want {
			t.Errorf("K-CRED %s: credentialKind %q, want %q", c.name, got, c.want)
		}
	}
}
