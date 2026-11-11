package readonly

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestK_ReadonlyBoundary (packet M1-T3a section 6.6, the T4 R1 rule): only
// internal/kube/readonly imports k8s.io/client-go/..., in every package of the module,
// its tests included, for each build tag in use.
func TestK_ReadonlyBoundary(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const format = `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}`
	const own = "github.com/prdroom/intuitik/internal/kube/readonly"
	for _, tags := range []string{"", "e2etest", "e2e", "kind"} {
		cmd := exec.Command("go", "list", "-tags", tags, "-f", format, "./...")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list -tags %q: %v\n%s", tags, err, out)
		}
		packages, importers := 0, 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			packages++
			pkg := fields[0]
			for _, imp := range fields[1:] {
				if imp != "k8s.io/client-go" && !strings.HasPrefix(imp, "k8s.io/client-go/") {
					continue
				}
				if pkg == own || strings.HasPrefix(pkg, own+"/") {
					importers++
					continue
				}
				t.Errorf("K-BOUNDARY tags %q: %s imports %s", tags, pkg, imp)
			}
		}
		t.Logf("K-BOUNDARY tags %q: %d packages listed, client-go imports only in %s (%d)", tags, packages, own, importers)
	}
}
