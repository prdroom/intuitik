package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository's own map must lint clean.
func TestLintRepository(t *testing.T) {
	var out bytes.Buffer
	code := Lint(filepath.Join("..", ".."), &out)
	t.Logf("lint repository -> exit %d\n%s", code, out.String())
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

// Each failure fixture differs from testdata/lint/ok in one way and must give a
// non-zero exit and a line naming that reason.
func TestLintFixtures(t *testing.T) {
	for _, c := range []struct {
		fixture string
		reason  string // empty: the fixture must pass
	}{
		{"ok", ""},
		{"missing-heading", "missing H2 heading"},
		{"heading-order", "H2 headings out of order"},
		{"index-entry-without-file", "index entry has no file"},
		{"file-not-in-index", "feature file not in index"},
		{"unknown-drive", "unknown drive id"},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			var out bytes.Buffer
			code := Lint(filepath.Join("testdata", "lint", c.fixture), &out)
			t.Logf("lint %s -> exit %d\n%s", c.fixture, code, out.String())
			if c.reason == "" {
				if code != 0 {
					t.Fatalf("exit %d, want 0", code)
				}
				return
			}
			if code == 0 {
				t.Fatal("exit 0, want non-zero")
			}
			if !hasLineContaining(out.String(), c.reason) {
				t.Fatalf("no output line names %q", c.reason)
			}
		})
	}
}

// The non-ASCII fixture is made at run time from the ok fixture, so that no
// committed file holds a non-ASCII byte; the byte is written as an escape here.
func TestLintNonASCII(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "lint", "ok"), root)
	feature := filepath.Join(root, "verify", "features", "sample-page.md")
	raw, err := os.ReadFile(feature)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("Caf\u00e9\n")...)
	if err := os.WriteFile(feature, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := Lint(root, &out)
	t.Logf("lint non-ascii -> exit %d\n%s", code, out.String())
	if code == 0 {
		t.Fatal("exit 0, want non-zero")
	}
	if !hasLineContaining(out.String(), "non-ASCII byte") {
		t.Fatal(`no output line names "non-ASCII byte"`)
	}
}

func hasLineContaining(out, s string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
