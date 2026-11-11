package readonly

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// randomValue is a credential value made at run time, so that no secret-like literal
// is committed.
func randomValue(t *testing.T, prefix string) string {
	t.Helper()
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return prefix + hex.EncodeToString(b)
}

// kubeconfig is the JSON form of a kubeconfig file with one cluster, one user and one
// context; JSON is valid YAML, so the loading rules read it as written.
func kubeconfig(server string, caPEM []byte, user map[string]any) map[string]any {
	cluster := map[string]any{"server": server}
	if caPEM != nil {
		cluster["certificate-authority-data"] = base64.StdEncoding.EncodeToString(caPEM)
	}
	return map[string]any{
		"apiVersion":      "v1",
		"kind":            "Config",
		"current-context": "ctx",
		"clusters":        []any{map[string]any{"name": "cl", "cluster": cluster}},
		"users":           []any{map[string]any{"name": "u", "user": user}},
		"contexts":        []any{map[string]any{"name": "ctx", "context": map[string]any{"cluster": "cl", "user": "u"}}},
	}
}

// useKubeconfig writes cfg to a temporary file and points KUBECONFIG at it.
func useKubeconfig(t *testing.T, cfg map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	return path
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
