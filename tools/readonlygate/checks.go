package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Rule names, in the order check runs them.
const (
	ruleGoEnv          = "go-env"
	ruleThirdParty     = "third-party"
	ruleBuildMatrix    = "build-matrix"
	ruleImportBoundary = "import-boundary"
	ruleExportedAPI    = "exported-api"
	ruleWriteCalls     = "write-calls"
	ruleImports        = "imports"
	ruleEscapeHatches  = "escape-hatches"
	ruleNoBypassConfig = "no-bypass-config"
)

const (
	readonlyRel    = "internal/kube/readonly"
	allowedAPIFile = "tools/readonlygate/allowed_api.txt"
	escapeFile     = "tools/readonlygate/escape_allow.txt"
)

// R2: the only third-party modules that may import client-go.
var clientGoModules = map[string]bool{"k8s.io/client-go": true, "k8s.io/apimachinery": true, "k8s.io/api": true}

// Modules that must not be in the product build (exceptions d50648d4 and 3e37ae69):
// the disk cache module and the test-only modules that the client-go graph brings.
var notInProduct = map[string]bool{
	"github.com/peterbourgon/diskv":                      true,
	"github.com/go-openapi/swag/jsonutils/fixtures_test": true,
	"github.com/go-openapi/testify/enable/yaml/v2":       true,
	"github.com/go-openapi/testify/v2":                   true,
	"github.com/kr/pretty":                               true,
	"github.com/kr/text":                                 true,
	"github.com/rogpeppe/go-internal":                    true,
	"go.uber.org/goleak":                                 true,
	"gopkg.in/check.v1":                                  true,
	"github.com/golang/protobuf":                         true,
}

// R4: the client-go packages that readonly may import; the standard library and
// k8s.io/apimachinery are allowed too. The executors are forbidden by name.
var readonlyClientGo = map[string]bool{
	"k8s.io/client-go/rest":                        true,
	"k8s.io/client-go/tools/clientcmd":             true,
	"k8s.io/client-go/tools/clientcmd/api":         true,
	"k8s.io/client-go/plugin/pkg/client/auth/oidc": true,
	"k8s.io/client-go/dynamic":                     true,
	"k8s.io/client-go/discovery":                   true,
	"k8s.io/client-go/discovery/cached/memory":     true,
}

var forbiddenImports = []string{
	"k8s.io/client-go/tools/remotecommand",
	"k8s.io/client-go/tools/portforward",
	"k8s.io/client-go/transport/spdy",
	"k8s.io/client-go/transport/websocket",
}

// R4: the write calls, method values and wrappers included.
var writeCalls = map[string]bool{"Create": true, "Update": true, "UpdateStatus": true, "Patch": true, "Apply": true,
	"Delete": true, "DeleteCollection": true, "Post": true, "Put": true, "Verb": true}

// clientGoImport reports a direct import of the Kubernetes client (R1).
func clientGoImport(path string) bool {
	return path == "k8s.io/client-go" || strings.HasPrefix(path, "k8s.io/client-go/") ||
		path == "k8s.io/apimachinery/pkg/client" || strings.HasPrefix(path, "k8s.io/apimachinery/pkg/client/")
}

type result struct {
	rule     string
	problems []string
	info     string
}

func (r *result) ok() bool { return len(r.problems) == 0 }

func (r *result) fail(format string, args ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, args...))
}

func (r *result) line() string {
	if r.ok() {
		if r.info != "" {
			return "pass: " + r.info
		}
		return "pass"
	}
	shown := r.problems
	if len(shown) > 5 {
		shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("and %d more", len(r.problems)-5))
	}
	return "FAIL: " + strings.Join(shown, "; ")
}

// check runs every rule on the module in dir for one combination.
func check(dir string, env []string, tagset string) []*result {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	results := map[string]*result{}
	order := []string{ruleGoEnv, ruleThirdParty, ruleBuildMatrix, ruleImportBoundary, ruleExportedAPI, ruleWriteCalls, ruleImports, ruleEscapeHatches, ruleNoBypassConfig}
	for _, name := range order {
		results[name] = &result{rule: name}
	}
	out := func() []*result {
		var list []*result
		for _, name := range order {
			list = append(list, results[name])
		}
		return list
	}

	checkGoEnv(abs, env, results[ruleGoEnv])

	modOut, err := runGo(abs, env, "list", "-m", "-f", "{{.Path}}")
	if err != nil {
		for _, name := range order[1:] {
			results[name].fail("module path: %v", err)
		}
		return out()
	}
	modPath := strings.TrimSpace(string(modOut))
	own := modPath + "/" + readonlyRel
	tags := tagArgs(tagset)

	deps, depsErr := goList(abs, env, append(append([]string{"-deps"}, tags...), "./cmd/intuitik")...)
	all, allErr := goList(abs, env, append(tags, "./...")...)

	checkThirdParty(abs, env, deps, depsErr, results[ruleThirdParty])
	checkBuildMatrix(abs, own, deps, depsErr, all, allErr, results[ruleBuildMatrix])
	checkImportBoundary(own, all, allErr, results[ruleImportBoundary])
	checkReadonly(abs, env, own, tags, results[ruleExportedAPI], results[ruleWriteCalls], results[ruleImports])
	checkProductFiles(abs, deps, depsErr, results[ruleEscapeHatches], results[ruleNoBypassConfig])
	return out()
}

// R2, go-env: the gate environment of packet section 6.1 holds, in the environment
// and as the go command sees it.
func checkGoEnv(abs string, env []string, r *result) {
	want := map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly", "GOENV": "off"}
	for _, k := range []string{"GOWORK", "GOFLAGS", "GOENV"} {
		if v := getenv(env, k); v != want[k] {
			r.fail("%s=%q, expected %q", k, v, want[k])
		}
	}
	out, err := runGo(abs, env, "env", "-json", "GOFLAGS", "GOMOD", "GOWORK")
	if err != nil {
		r.fail("go env: %v", err)
		return
	}
	var ge map[string]string
	if err := json.Unmarshal(out, &ge); err != nil {
		r.fail("go env output: %v", err)
		return
	}
	if ge["GOFLAGS"] != "-mod=readonly" {
		r.fail("go env GOFLAGS=%q", ge["GOFLAGS"])
	}
	if ge["GOWORK"] != "" && ge["GOWORK"] != "off" {
		r.fail("go env GOWORK=%q", ge["GOWORK"])
	}
	if filepath.Clean(ge["GOMOD"]) != filepath.Join(abs, "go.mod") {
		r.fail("go env GOMOD=%q, expected %s", ge["GOMOD"], filepath.Join(abs, "go.mod"))
	}
	r.info = fmt.Sprintf("GOWORK=%s GOFLAGS=%s GOENV=%s GOMOD=%s", getenv(env, "GOWORK"), getenv(env, "GOFLAGS"), getenv(env, "GOENV"), ge["GOMOD"])
}

// R2, third-party: no replace, no vendor/, go.work or go.work.sum, go mod verify passes,
// every module of the build list is unreplaced and in go.sum, and the third-party
// modules that import client-go are only client-go, apimachinery and api.
func checkThirdParty(abs string, env []string, deps []*pkg, depsErr error, r *result) {
	walkModule(abs, func(rel string, d fs.DirEntry) {
		name := d.Name()
		if (d.IsDir() && name == "vendor") || (!d.IsDir() && (name == "go.work" || name == "go.work.sum")) {
			r.fail("%s present", rel)
		}
	})
	if out, err := runGo(abs, env, "mod", "edit", "-json"); err != nil {
		r.fail("go mod edit -json: %v", err)
	} else {
		var gm struct{ Replace []any }
		if err := json.Unmarshal(out, &gm); err != nil {
			r.fail("go mod edit -json output: %v", err)
		} else if len(gm.Replace) != 0 {
			r.fail("go.mod has %d replace directive(s)", len(gm.Replace))
		}
	}
	if _, err := runGo(abs, env, "mod", "download"); err != nil {
		r.fail("go mod download: %v", err)
	}
	if _, err := runGo(abs, env, "mod", "verify"); err != nil {
		r.fail("go mod verify: %v", err)
	}
	tidy := "empty"
	if out, err := runGo(abs, env, "mod", "tidy", "-diff"); err != nil || len(bytes.TrimSpace(out)) != 0 {
		tidy = fmt.Sprintf("%d diff lines", bytes.Count(out, []byte("\n")))
		r.fail("go mod tidy -diff: %d diff lines, err=%v", bytes.Count(out, []byte("\n")), err)
	}
	sum, _ := os.ReadFile(filepath.Join(abs, "go.sum"))
	mods, err := goListModules(abs, env)
	if err != nil {
		r.fail("go list -m -json all: %v", err)
	}
	graph := map[string]*module{}
	for _, m := range mods {
		if m.Replace != nil {
			r.fail("module %s is replaced by %s %s", m.Path, m.Replace.Path, m.Replace.Version)
		}
		graph[m.Path] = m
	}
	// The module sources actually used are the modules of the packages in the build:
	// each one is in the module graph at the same version and go.sum holds its content
	// hash. A module of the graph whose packages are not built has no content line.
	used := map[string]*module{}
	for _, p := range deps {
		if p.Module != nil && !p.Module.Main {
			used[p.Module.Path] = p.Module
		}
	}
	for path, m := range used {
		if m.Replace != nil {
			r.fail("module %s is replaced by %s %s", m.Path, m.Replace.Path, m.Replace.Version)
		}
		if g, ok := graph[path]; !ok || g.Version != m.Version {
			r.fail("module %s %s of the build is not at that version in go list -m -json all", m.Path, m.Version)
		}
		if !bytes.Contains(sum, []byte("\n"+m.Path+" "+m.Version+" h1:")) && !bytes.HasPrefix(sum, []byte(m.Path+" "+m.Version+" h1:")) {
			r.fail("module %s %s of the build has no content hash in go.sum", m.Path, m.Version)
		}
	}
	if depsErr != nil {
		r.fail("go list -deps ./cmd/intuitik: %v", depsErr)
	}
	excluded := 0
	for _, p := range deps {
		if p.ImportPath == "k8s.io/client-go/discovery/cached/disk" || (p.Module != nil && notInProduct[p.Module.Path]) {
			excluded++
			r.fail("package %s (module %s) is in the product build", p.ImportPath, p.Module.Path)
		}
	}
	importers := map[string]bool{}
	for _, p := range deps {
		if p.Standard || p.Module == nil || p.Module.Main {
			continue
		}
		for _, imp := range p.Imports {
			if clientGoImport(imp) {
				importers[p.Module.Path] = true
				if !clientGoModules[p.Module.Path] {
					r.fail("third-party module %s (package %s) imports %s", p.Module.Path, p.ImportPath, imp)
				}
			}
		}
	}
	var names []string
	for m := range importers {
		names = append(names, m)
	}
	sort.Strings(names)
	r.info = fmt.Sprintf("modules=%d; go mod verify ok; go mod tidy -diff %s; product build packages=%d, of them in discovery/cached/disk or the excluded modules: %d; third-party modules importing client-go: %s",
		len(mods), tidy, len(deps), excluded, strings.Join(names, ", "))
}

// R2, build-matrix: the product build of this combination loads without errors, and
// no file that this combination's build constraints exclude imports client-go outside
// readonly (a client-go import placed only in a file of another GOOS or tag).
func checkBuildMatrix(abs, own string, deps []*pkg, depsErr error, all []*pkg, allErr error, r *result) {
	if depsErr != nil {
		r.fail("go list -deps ./cmd/intuitik: %v", depsErr)
	}
	if allErr != nil {
		r.fail("go list ./...: %v", allErr)
	}
	for _, p := range deps {
		if p.Error != nil {
			r.fail("package %s: %s", p.ImportPath, p.Error.Err)
		}
	}
	fset := token.NewFileSet()
	ignored := 0
	for _, p := range all {
		if p.Module == nil || !p.Module.Main || p.ImportPath == own {
			continue
		}
		for _, f := range p.IgnoredGoFiles {
			ignored++
			af, err := parser.ParseFile(fset, filepath.Join(p.Dir, f), nil, parser.ImportsOnly)
			if err != nil {
				continue
			}
			for _, imp := range af.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if clientGoImport(path) {
					r.fail("%s: file excluded from this combination imports %s", relPath(abs, filepath.Join(p.Dir, f)), path)
				}
			}
		}
	}
	r.info = fmt.Sprintf("product build packages=%d; excluded files read=%d", len(deps), ignored)
}

// R1, import-boundary: only readonly (and its tests) imports client-go directly.
func checkImportBoundary(own string, all []*pkg, allErr error, r *result) {
	if allErr != nil {
		r.fail("go list ./...: %v", allErr)
	}
	n, importers := 0, 0
	for _, p := range all {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		n++
		for _, imp := range append(append(append([]string{}, p.Imports...), p.TestImports...), p.XTestImports...) {
			if !clientGoImport(imp) {
				continue
			}
			if p.ImportPath == own {
				importers++
				continue
			}
			r.fail("%s imports %s", p.ImportPath, imp)
		}
	}
	r.info = fmt.Sprintf("module packages=%d; client-go imports only in readonly (%d)", n, importers)
}

// R3, R4: exported API, write calls and imports of readonly, from its type-checked
// non-test files of this combination.
func checkReadonly(abs string, env []string, own string, tags []string, api, writes, imports *result) {
	list, err := goList(abs, env, append(tags, own)...)
	if err != nil || len(list) != 1 {
		for _, r := range []*result{api, writes, imports} {
			r.fail("go list %s: %v", own, err)
		}
		return
	}
	ro := list[0]
	std := map[string]bool{}
	if stdPkgs, err := goList(abs, env, "std"); err == nil {
		for _, p := range stdPkgs {
			std[p.ImportPath] = true
		}
	} else {
		imports.fail("go list std: %v", err)
	}
	for _, imp := range ro.Imports {
		forbidden := false
		for _, f := range forbiddenImports {
			if imp == f || strings.HasPrefix(imp, f+"/") {
				imports.fail("readonly imports the forbidden %s", imp)
				forbidden = true
			}
		}
		if forbidden || std[imp] || strings.HasPrefix(imp, "k8s.io/apimachinery/") || readonlyClientGo[imp] {
			continue
		}
		imports.fail("readonly imports %s, which is not in the allowed list", imp)
	}
	imports.info = fmt.Sprintf("readonly imports=%d, all allowed", len(ro.Imports))

	deps, err := goList(abs, env, append(append([]string{"-export", "-deps"}, tags...), own)...)
	exports := map[string]string{}
	for _, p := range deps {
		if p.Error != nil {
			err = fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
		}
		exports[p.ImportPath] = p.Export
	}
	if err != nil {
		api.fail("type information: %v", err)
		writes.fail("type information: %v", err)
		return
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, f := range append(append([]string{}, ro.GoFiles...), ro.CgoFiles...) {
		af, err := parser.ParseFile(fset, filepath.Join(ro.Dir, f), nil, parser.ParseComments)
		if err != nil {
			api.fail("parse %s: %v", f, err)
			writes.fail("parse %s: %v", f, err)
			return
		}
		files = append(files, af)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		if e := exports[path]; e != "" {
			return os.Open(e)
		}
		return nil, fmt.Errorf("no export data for %s", path)
	})
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: imp, Sizes: types.SizesFor("gc", getenv(env, "GOARCH"))}
	tpkg, err := conf.Check(own, fset, files, info)
	if err != nil {
		api.fail("type check: %v", err)
		writes.fail("type check: %v", err)
		return
	}

	// write-calls: every use of a function or method with a write name.
	uses := 0
	for id, obj := range info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || !writeCalls[fn.Name()] {
			continue
		}
		uses++
		where := ""
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			where = types.TypeString(sig.Recv().Type(), nil) + "."
		}
		writes.fail("%s: %s%s", relPath(abs, fset.Position(id.Pos()).String()), where, fn.Name())
	}
	writes.info = fmt.Sprintf("readonly files=%d, write calls=%d", len(files), uses)

	// exported-api: the full exported surface equals the allowed list.
	got := exportedAPI(tpkg)
	allowed, err := readLines(filepath.Join(abs, allowedAPIFile))
	if err != nil {
		api.fail("%s: %v", allowedAPIFile, err)
		return
	}
	for _, l := range got {
		if strings.Contains(l, "k8s.io/client-go/") {
			api.fail("exported API refers to client-go: %s", l)
		}
	}
	gotSet, allowedSet := setOf(got), setOf(allowed)
	for _, l := range got {
		if !allowedSet[l] {
			api.fail("not in the allowed list: %s", l)
		}
	}
	for _, l := range allowed {
		if !gotSet[l] {
			api.fail("in the allowed list but not exported: %s", l)
		}
	}
	api.info = fmt.Sprintf("exported API lines=%d equal %s", len(got), allowedAPIFile)
}

// exportedAPI renders every exported identifier of p: constants, variables and
// functions with their types, types with their kind, exported struct fields, every
// embedded field, interface methods and the method set of each type and its pointer.
func exportedAPI(p *types.Package) []string {
	qf := func(q *types.Package) string {
		if q == p {
			return ""
		}
		return q.Path()
	}
	ts := func(t types.Type) string { return types.TypeString(t, qf) }
	var lines []string
	scope := p.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Const:
			lines = append(lines, "const "+name+" "+ts(o.Type()))
		case *types.Var:
			lines = append(lines, "var "+name+" "+ts(o.Type()))
		case *types.Func:
			lines = append(lines, "func "+name+" "+ts(o.Type()))
		case *types.TypeName:
			if o.IsAlias() {
				lines = append(lines, "type "+name+" = "+ts(types.Unalias(o.Type())))
				continue
			}
			switch u := o.Type().Underlying().(type) {
			case *types.Struct:
				lines = append(lines, "type "+name+" struct")
				for i := 0; i < u.NumFields(); i++ {
					f := u.Field(i)
					switch {
					case f.Embedded():
						lines = append(lines, "embedded "+name+"."+f.Name()+" "+ts(f.Type()))
					case f.Exported():
						lines = append(lines, "field "+name+"."+f.Name()+" "+ts(f.Type()))
					}
				}
			case *types.Interface:
				lines = append(lines, "type "+name+" interface")
				for i := 0; i < u.NumMethods(); i++ {
					m := u.Method(i)
					lines = append(lines, "method "+name+"."+m.Name()+" "+ts(m.Type()))
				}
				continue
			default:
				lines = append(lines, "type "+name+" "+ts(u))
			}
			ms := types.NewMethodSet(types.NewPointer(o.Type()))
			for i := 0; i < ms.Len(); i++ {
				m := ms.At(i).Obj()
				if m.Exported() {
					lines = append(lines, "method "+name+"."+m.Name()+" "+ts(m.Type()))
				}
			}
		}
	}
	sort.Strings(lines)
	return lines
}

// R4 escape-hatches and R5 no-bypass-config, over every non-test file of the module's
// packages in this combination's product build.
func checkProductFiles(abs string, deps []*pkg, depsErr error, escape, bypass *result) {
	if depsErr != nil {
		escape.fail("go list -deps ./cmd/intuitik: %v", depsErr)
		bypass.fail("go list -deps ./cmd/intuitik: %v", depsErr)
	}
	allowLines, err := readLines(filepath.Join(abs, escapeFile))
	if err != nil {
		escape.fail("%s: %v", escapeFile, err)
	}
	allow := map[string]bool{} // "<import> <file or package dir>"
	for _, l := range allowLines {
		allow[l] = true
	}
	fset := token.NewFileSet()
	files := 0
	for _, p := range deps {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		pkgDir := relPath(abs, p.Dir)
		for _, f := range append(append([]string{}, p.GoFiles...), p.CgoFiles...) {
			path := filepath.Join(p.Dir, f)
			rel := relPath(abs, path)
			af, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				escape.fail("parse %s: %v", rel, err)
				bypass.fail("parse %s: %v", rel, err)
				continue
			}
			files++
			for _, cg := range af.Comments {
				for _, c := range cg.List {
					if strings.HasPrefix(c.Text, "//go:linkname") {
						escape.fail("%s: %s", rel, c.Text)
					}
				}
			}
			for _, imp := range af.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if path != "unsafe" && path != "os/exec" {
					continue
				}
				if !allow[path+" "+rel] && !allow[path+" "+pkgDir] {
					escape.fail("%s imports %s", rel, path)
				}
			}
			ast.Inspect(af, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if strings.HasSuffix(x.Name, "ForConfigAndClient") {
						bypass.fail("%s: %s", relPath(abs, fset.Position(x.Pos()).String()), x.Name)
					}
				case *ast.AssignStmt:
					for _, lhs := range x.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "WrapTransport" {
							bypass.fail("%s: assignment to WrapTransport", relPath(abs, fset.Position(x.Pos()).String()))
						}
					}
				case *ast.KeyValueExpr:
					if k, ok := x.Key.(*ast.Ident); ok && k.Name == "WrapTransport" {
						bypass.fail("%s: WrapTransport in a composite literal", relPath(abs, fset.Position(x.Pos()).String()))
					}
				}
				return true
			})
		}
	}
	escape.info = fmt.Sprintf("product files=%d; allowed unsafe and os/exec entries=%d", files, len(allowLines))
	bypass.info = fmt.Sprintf("product files=%d", files)
}

// walkModule visits the module tree in abs, without .git, node_modules and nested
// modules.
func walkModule(abs string, visit func(rel string, d fs.DirEntry)) {
	filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != abs {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
		}
		if p != abs {
			visit(relPath(abs, p), d)
		}
		return nil
	})
}

func relPath(abs, p string) string {
	if rel, err := filepath.Rel(abs, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// readLines reads the non-empty lines of a list file that do not start with #.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		l := strings.TrimSpace(s.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return lines, s.Err()
}

func setOf(lines []string) map[string]bool {
	m := map[string]bool{}
	for _, l := range lines {
		m[l] = true
	}
	return m
}
