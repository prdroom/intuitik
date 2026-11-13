package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// pkg is the part of a go list -json package that the rules read.
type pkg struct {
	ImportPath     string
	Dir            string
	Standard       bool
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	Imports        []string
	TestImports    []string
	XTestImports   []string
	Export         string
	Module         *module
	Error          *struct{ Err string }
	DepsErrors     []*struct{ Err string }
}

type module struct {
	Path    string
	Version string
	Main    bool
	Replace *module
}

// runGo runs one go command in dir with env and returns its stdout. The gate never
// adds -modfile or -overlay to a command line.
func runGo(dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("go %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// goList decodes the package stream of go list -e -json. A command that fails without
// output is an error; with -e, package errors are in the packages.
func goList(dir string, env []string, args ...string) ([]*pkg, error) {
	out, err := runGo(dir, env, append([]string{"list", "-e", "-json"}, args...)...)
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return nil, err
	}
	var pkgs []*pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		p := new(pkg)
		if derr := dec.Decode(p); derr == io.EOF {
			break
		} else if derr != nil {
			return nil, fmt.Errorf("go list output: %v", derr)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, err
}

// goListModules decodes go list -m -json all.
func goListModules(dir string, env []string) ([]*module, error) {
	out, err := runGo(dir, env, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	var mods []*module
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		m := new(module)
		if derr := dec.Decode(m); derr == io.EOF {
			break
		} else if derr != nil {
			return nil, derr
		}
		mods = append(mods, m)
	}
	return mods, nil
}

// tagArgs is the -tags argument of a tag set.
func tagArgs(tagset string) []string {
	if tagset == "release" || tagset == "" {
		return nil
	}
	return []string{"-tags", tagset}
}
