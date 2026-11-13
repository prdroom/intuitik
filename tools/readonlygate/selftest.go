package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fixture is one bypass fixture of R7: a copy of the product module with one bypass
// added, and the rule that must catch it.
type fixture struct {
	id   string // the directory under the fixtures directory, or a generated case
	row  string // the R row the fixture belongs to
	rule string
	// generate, for the R2 method cases that are not numbered fixtures, writes the
	// bypass into the copy instead of an overlay directory.
	generate func(dir string) error
}

// numbered are the bypass fixtures 1 to 9 of packet M1-T4 6.2; each has a directory
// with go.mod, an overlay/ tree copied onto the product module and an optional
// fixture.env with environment lines.
var numbered = []fixture{
	{id: "01-import-outside-readonly", row: "R1", rule: ruleImportBoundary},
	{id: "02-write-alias-wrapper", row: "R4", rule: ruleWriteCalls},
	{id: "03-rest-interface-post", row: "R4", rule: ruleWriteCalls},
	{id: "04-exported-client-go-type", row: "R3", rule: ruleExportedAPI},
	{id: "05-spdy-executor", row: "R4", rule: ruleImports},
	{id: "06-go-linkname", row: "R4", rule: ruleEscapeHatches},
	{id: "07-exec-kubectl", row: "R4", rule: ruleEscapeHatches},
	{id: "08-go-work-replace", row: "R2", rule: ruleThirdParty},
	{id: "09-goflags-modfile-overlay", row: "R2", rule: ruleGoEnv},
}

// r2Cases are the other fixtures of the R2 method: a module outside the list, an
// added replace and a client-go import only in a //go:build windows file (G5).
var r2Cases = []fixture{
	{id: "R2 third-party module outside the list", row: "R2", rule: ruleThirdParty, generate: func(dir string) error {
		return writeFiles(dir, map[string]string{
			"third_party/kubewrap/go.mod":  "module example.invalid/kubewrap\n\ngo 1.27.3\n\nrequire k8s.io/client-go v0.37.2\n",
			"third_party/kubewrap/wrap.go": "package kubewrap\n\nimport _ \"k8s.io/client-go/rest\"\n",
			"cmd/intuitik/zz_kubewrap.go":  "package main\n\nimport _ \"example.invalid/kubewrap\"\n",
			"go.mod.append":                "\nrequire example.invalid/kubewrap v0.0.0\n\nreplace example.invalid/kubewrap => ./third_party/kubewrap\n",
		})
	}},
	{id: "R2 added replace", row: "R2", rule: ruleThirdParty, generate: func(dir string) error {
		return writeFiles(dir, map[string]string{"go.mod.append": "\nreplace k8s.io/client-go => k8s.io/client-go v0.37.2\n"})
	}},
	{id: "R2 client-go import only in a windows file", row: "R2", rule: ruleBuildMatrix, generate: func(dir string) error {
		return writeFiles(dir, map[string]string{
			"internal/server/zz_kube_windows.go": "//go:build windows\n\npackage server\n\nimport _ \"k8s.io/client-go/rest\"\n",
		})
	}},
}

// rowCounts are the numbers of numbered fixtures per R row (R1 1, R2 2, R3 1, R4 5).
var rowCounts = map[string]int{"R1": 1, "R2": 2, "R3": 1, "R4": 5}

// selfTest runs the gate on every fixture until its rule fails in some combination.
func selfTest(fixturesDir string, targets, tagsets []string) bool {
	ok := true
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		fmt.Printf("readonly-gate: self-test FAIL: %v\n", err)
		return false
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	counts := map[string]int{}
	for _, f := range numbered {
		counts[f.row]++
	}
	if len(dirs) != len(numbered) {
		fmt.Printf("readonly-gate: self-test FAIL: %d fixture directories %v, expected the %d numbered fixtures\n", len(dirs), dirs, len(numbered))
		ok = false
	}
	for row, n := range rowCounts {
		if counts[row] != n {
			fmt.Printf("readonly-gate: self-test FAIL: row %s has %d numbered fixtures, expected %d\n", row, counts[row], n)
			ok = false
		}
	}
	if len(targets) == 0 || len(tagsets) == 0 {
		fmt.Println("readonly-gate: self-test FAIL: no targets or tag sets")
		return false
	}
	caught := 0
	for _, f := range append(append([]fixture{}, numbered...), r2Cases...) {
		if runFixture(fixturesDir, f, targets, tagsets) {
			caught++
		} else {
			ok = false
		}
	}
	fmt.Printf("readonly-gate: self-test %d of %d fixtures caught (%d numbered, %d R2 method cases)\n", caught, len(numbered)+len(r2Cases), len(numbered), len(r2Cases))
	return ok
}

func runFixture(fixturesDir string, f fixture, targets, tagsets []string) bool {
	tmp, err := os.MkdirTemp("", "readonly-bypass-")
	if err != nil {
		fmt.Printf("readonly-gate: self-test fixture %s FAIL: %v\n", f.id, err)
		return false
	}
	defer os.RemoveAll(tmp)
	env := os.Environ()
	if err := copyModule(".", tmp); err != nil {
		fmt.Printf("readonly-gate: self-test fixture %s FAIL: copy: %v\n", f.id, err)
		return false
	}
	if f.generate != nil {
		err = f.generate(tmp)
	} else {
		dir := filepath.Join(fixturesDir, f.id)
		if _, serr := os.Stat(filepath.Join(dir, "go.mod")); serr != nil {
			err = fmt.Errorf("fixture module: %v", serr)
		} else {
			err = overlay(filepath.Join(dir, "overlay"), tmp)
		}
		if err == nil {
			var lines []string
			lines, err = readLines(filepath.Join(dir, "fixture.env"))
			if os.IsNotExist(err) {
				err = nil
			}
			env = append(env, lines...)
		}
	}
	if err != nil {
		fmt.Printf("readonly-gate: self-test fixture %s FAIL: %v\n", f.id, err)
		return false
	}
	for _, target := range targets {
		goos, goarch, _ := strings.Cut(target, "/")
		for _, tagset := range tagsets {
			cenv := append(append([]string{}, env...), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
			var failing []string
			var line string
			for _, r := range check(tmp, cenv, tagset) {
				if !r.ok() {
					failing = append(failing, r.rule)
				}
				if r.rule == f.rule {
					line = r.line()
				}
			}
			for _, rule := range failing {
				if rule == f.rule {
					fmt.Printf("readonly-gate: self-test fixture %s (%s) caught by %s at %s tagset=%s: %s; failing rules %v\n",
						f.id, f.row, f.rule, target, tagset, line, failing)
					return true
				}
			}
		}
	}
	fmt.Printf("readonly-gate: self-test fixture %s (%s) NOT CAUGHT: rule %s passed in every combination\n", f.id, f.row, f.rule)
	return false
}

// copyModule copies the product module in src to dst, without .git, .factory, web,
// node_modules, testdata and nested modules.
func copyModule(src, dst string) error {
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(abs, p)
		if d.IsDir() {
			if p == abs {
				return nil
			}
			switch d.Name() {
			case ".git", ".factory", "web", "node_modules", "testdata":
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

// overlay copies the files of an overlay tree onto dst; a file named <name>.append is
// appended to <name>.
func overlay(src, dst string) error {
	files := map[string]string{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		return err
	}
	return writeFiles(dst, files)
}

func writeFiles(dst string, files map[string]string) error {
	for rel, content := range files {
		target := filepath.Join(dst, filepath.FromSlash(strings.TrimSuffix(rel, ".append")))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if strings.HasSuffix(rel, ".append") {
			flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(target, flag, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(content)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
