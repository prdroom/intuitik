package readonly

import (
	"fmt"
	"testing"
)

// TestK_UnknownSlotsFormat (packet M1-T3b section 3 UV, P-T3b-1): every text with a
// server, cluster or context slot prints "(unknown)" for an empty value, never an empty
// string. Each case prints expected and actual.
func TestK_UnknownSlotsFormat(t *testing.T) {
	const u = "(unknown)"
	for _, c := range []struct {
		id, got, want string
	}{
		{"K-OK", kOK("", ""), fmt.Sprintf(textOK, u, u)},
		{"K-IV2", text(textIV2, slot("")), fmt.Sprintf(textIV2, u)},
		{"K-IV3", kIV3(""), fmt.Sprintf(textIV3, u)},
		{"K-AU", kAU("", ""), fmt.Sprintf(textAU, u, u)},
		{"K-NC", kNC("", ""), fmt.Sprintf(textNC, u, u)},
		{"K-AP", kAP("", "gcp"), fmt.Sprintf(textAP, u, "gcp")},
		{"K-TL", kTL("", "", "x509: unknown authority"), fmt.Sprintf(textTL, u, u, "x509: unknown authority")},
		{"K-UR", kUR("", "", textKubeconfigTimeout), fmt.Sprintf(textUR, u, u, "the kubeconfig was not read within 10 s")},
		{"K-HS", kHS("", "", 500), fmt.Sprintf(textHS, u, u, 500)},
		{"K-EP", kEP("/bin/program", "", "not found"), fmt.Sprintf(textEP, "/bin/program", u, "not found")},
		{"K-UC", UnknownContext(""), fmt.Sprintf(textUC, u)},
		{"K-UR with values", kUR("https://h:6443", "ctx", textTimeout), fmt.Sprintf(textUR, "https://h:6443", "ctx", "no answer within 10 s")},
	} {
		t.Logf("UV %s\n  expected %q\n  actual   %q", c.id, c.want, c.got)
		if c.got != c.want {
			t.Errorf("UV %s: actual %q, expected %q", c.id, c.got, c.want)
		}
	}
}
