package server

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	gotoken "go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	readonlyPath = "github.com/prdroom/intuitik/internal/kube/readonly"
	serverPath   = "github.com/prdroom/intuitik/internal/server"
)

// gateImporter type-checks internal/kube/readonly from source and the standard
// library from GOROOT. Every other import becomes an empty package: the check needs
// only the identity of readonly.NewClient and readonly.Client, and type errors in code
// that uses the empty packages are ignored.
type gateImporter struct {
	fset  *gotoken.FileSet
	std   types.Importer
	root  string
	cache map[string]*types.Package
}

func newGateImporter(t *testing.T) *gateImporter {
	t.Helper()
	build.Default.CgoEnabled = false // the source importer must not run cgo
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := gotoken.NewFileSet()
	return &gateImporter{fset: fset, std: importer.ForCompiler(fset, "source", nil), root: root, cache: map[string]*types.Package{}}
}

func (g *gateImporter) Import(path string) (*types.Package, error) {
	if p, ok := g.cache[path]; ok {
		return p, nil
	}
	var pkg *types.Package
	switch {
	case path == readonlyPath:
		p, _, _, err := g.check(filepath.Join(g.root, "internal", "kube", "readonly"), path, nil)
		if err != nil {
			return nil, err
		}
		pkg = p
	case !strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
		p, err := g.std.Import(path)
		if err != nil {
			return nil, err
		}
		pkg = p
	default:
		elems := strings.Split(path, "/")
		pkg = types.NewPackage(path, strings.ReplaceAll(elems[len(elems)-1], ".", "_"))
		pkg.MarkComplete()
	}
	g.cache[path] = pkg
	return pkg, nil
}

// check type-checks the non-test files of dir that match the build tags.
func (g *gateImporter) check(dir, path string, tags []string) (*types.Package, *types.Info, []*ast.File, error) {
	ctx := build.Default
	ctx.BuildTags = tags
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if ok, err := ctx.MatchFile(dir, name); err != nil || !ok {
			continue
		}
		f, err := parser.ParseFile(g.fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		files = append(files, f)
	}
	info := &types.Info{
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Types:      map[ast.Expr]types.TypeAndValue{},
	}
	conf := types.Config{Importer: g, Error: func(error) {}}
	pkg, _ := conf.Check(path, g.fset, files, info)
	return pkg, info, files, nil
}

// contains reports whether t is target or holds it in a pointer, slice, array, map,
// channel, struct field or function signature. Named types other than target are not
// opened; their declarations are checked on their own.
func contains(t, target types.Type) bool {
	if t == nil || target == nil {
		return false
	}
	if types.Identical(t, target) {
		return true
	}
	switch u := t.(type) {
	case *types.Pointer:
		return contains(u.Elem(), target)
	case *types.Slice:
		return contains(u.Elem(), target)
	case *types.Array:
		return contains(u.Elem(), target)
	case *types.Map:
		return contains(u.Key(), target) || contains(u.Elem(), target)
	case *types.Chan:
		return contains(u.Elem(), target)
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if contains(u.Field(i).Type(), target) {
				return true
			}
		}
	case *types.Signature:
		return tupleContains(u.Params(), target) || tupleContains(u.Results(), target)
	}
	return false
}

func tupleContains(tu *types.Tuple, target types.Type) bool {
	for i := 0; i < tu.Len(); i++ {
		if contains(tu.At(i).Type(), target) {
			return true
		}
	}
	return false
}

// gateProblems applies the rules of section 6.2 to one type-checked package:
//  1. readonly.NewClient is called only in cluster_gate.go, and only as a call;
//  2. no package variable, struct field or function result holds a readonly client;
//  3. a method of the client is only called, never taken as a method value;
//  4. no type besides clusterHandler wraps a cluster handler or a client;
//  5. Handle and HandleFunc of net/http are called only in controlRoute and clusterRoute.
func gateProblems(g *gateImporter, pkg *types.Package, info *types.Info, files []*ast.File) []string {
	ro, err := g.Import(readonlyPath)
	if err != nil {
		return []string{"readonly package: " + err.Error()}
	}
	ctor := ro.Scope().Lookup("NewClient")
	clientObj := ro.Scope().Lookup("Client")
	if ctor == nil || clientObj == nil {
		return []string{"readonly.NewClient or readonly.Client not found"}
	}
	client := types.NewPointer(clientObj.Type())
	var handler types.Type
	if h := pkg.Scope().Lookup("clusterHandler"); h != nil {
		handler = h.Type()
	}
	var problems []string
	add := func(n ast.Node, format string, args ...any) {
		pos := g.fset.Position(n.Pos())
		problems = append(problems, fmt.Sprintf("%s:%d: %s", filepath.Base(pos.Filename), pos.Line, fmt.Sprintf(format, args...)))
	}
	for _, f := range files {
		base := filepath.Base(g.fset.Position(f.Pos()).Filename)
		called := map[ast.Expr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				called[ast.Unparen(c.Fun)] = true
			}
			return true
		})
		// Rules 1 and 3.
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if info.Uses[x.Sel] == ctor {
					if base != "cluster_gate.go" {
						add(x, "readonly.NewClient is used outside cluster_gate.go")
					} else if !called[x] {
						add(x, "readonly.NewClient is used as a value, not called")
					}
				}
				if sel := info.Selections[x]; sel != nil && sel.Kind() != types.FieldVal && contains(sel.Recv(), client) && (sel.Kind() == types.MethodExpr || !called[x]) {
					add(x, "method %s of the readonly client is taken as a value", x.Sel.Name)
				}
			case *ast.Ident:
				if info.Uses[x] == ctor && base != "cluster_gate.go" {
					add(x, "readonly.NewClient is used outside cluster_gate.go")
				}
			}
			return true
		})
		// Rule 5.
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			allowed := fd.Name.Name == "controlRoute" || fd.Name.Name == "clusterRoute"
			ast.Inspect(fd, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := ast.Unparen(c.Fun).(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
					return true
				}
				obj := info.Uses[sel.Sel]
				if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "net/http" && !allowed {
					add(c, "net/http %s is called outside controlRoute and clusterRoute", sel.Sel.Name)
				}
				return true
			})
			return false
		})
	}
	// Rules 2 and 4, over every object the package defines.
	var ids []*ast.Ident
	for id := range info.Defs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Pos() < ids[j].Pos() })
	for _, id := range ids {
		obj := info.Defs[id]
		if obj == nil {
			continue
		}
		switch o := obj.(type) {
		case *types.Var:
			if o.Parent() == pkg.Scope() && contains(o.Type(), client) {
				add(id, "package variable %s holds a readonly client", o.Name())
			}
			if o.IsField() && (contains(o.Type(), client) || contains(o.Type(), handler)) {
				add(id, "field %s holds a readonly client or a cluster handler", o.Name())
			}
		case *types.Func:
			if sig, ok := o.Type().(*types.Signature); ok && tupleContains(sig.Results(), client) {
				add(id, "function %s returns a readonly client", o.Name())
			}
		case *types.TypeName:
			if o.Name() == "clusterHandler" {
				continue
			}
			if contains(o.Type().Underlying(), client) || contains(o.Type().Underlying(), handler) {
				add(id, "type %s wraps a readonly client or a cluster handler", o.Name())
			}
		}
	}
	return problems
}

// TestK_ClusterRouteOnlyThroughGate (packet M1-T3a section 6.2, go/types): the product
// package passes the gate rules in the product and the e2etest build, and each negative
// fixture under testdata/gatebypass fails them.
func TestK_ClusterRouteOnlyThroughGate(t *testing.T) {
	g := newGateImporter(t)
	for _, tags := range [][]string{nil, {"e2etest"}} {
		pkg, info, files, err := g.check(".", serverPath, tags)
		if err != nil {
			t.Fatal(err)
		}
		problems := gateProblems(g, pkg, info, files)
		for _, p := range problems {
			t.Errorf("K-GATE product tags %v: %s", tags, p)
		}
		if len(problems) == 0 {
			t.Logf("K-GATE product tags %v: %d files, 0 problems", tags, len(files))
		}
	}
	dirs, err := os.ReadDir(filepath.Join("testdata", "gatebypass"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"client-in-variable", "client-returned", "constructor-as-value", "direct-handlefunc", "helper-builds-client", "method-value-package-var", "wrapper-type"}
	var got []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		got = append(got, d.Name())
		pkg, info, files, err := g.check(filepath.Join("testdata", "gatebypass", d.Name()), serverPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		problems := gateProblems(g, pkg, info, files)
		if len(problems) == 0 {
			t.Errorf("NEGATIVE-FIXTURE K-GATE %s: the check passed against the defect", d.Name())
			continue
		}
		t.Logf("NEGATIVE-FIXTURE K-GATE %s: fails as required: %s", d.Name(), strings.Join(problems, "; "))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("K-GATE fixtures %v, want %v", got, want)
	}
}
