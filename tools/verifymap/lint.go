package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// indexEntry is one line of verify/features/README.md: `- [<id>](<id>.md): <text>`.
var indexEntry = regexp.MustCompile(`^- \[([a-z0-9][a-z0-9-]*)\]\(([^)]+)\)`)

// Lint checks the verification map under root/verify and prints one line per
// problem. It returns 0 when there is none, 1 otherwise. Checks:
//   - verify/features/README.md (the index) and the feature files match one to one;
//   - every feature file has exactly the four fixed H2 headings, in order;
//   - every "Driving it with <drive id>" heading names a drive id the runner knows,
//     and its ```drive block parses;
//   - every file under verify/ is ASCII.
func Lint(root string, out io.Writer) int {
	l := &linter{root: root, out: out}
	l.check()
	if l.problems > 0 {
		fmt.Fprintf(out, "lint: FAIL %d problem(s)\n", l.problems)
		return 1
	}
	fmt.Fprintf(out, "lint: OK %d feature(s)\n", l.features)
	return 0
}

type linter struct {
	root     string
	out      io.Writer
	problems int
	features int
}

func (l *linter) fail(file, format string, args ...any) {
	l.problems++
	fmt.Fprintf(l.out, "lint: FAIL %s: %s\n", file, fmt.Sprintf(format, args...))
}

func (l *linter) check() {
	verifyDir := filepath.Join(l.root, "verify")
	featuresDir := filepath.Join(verifyDir, "features")
	l.checkASCII(verifyDir)

	indexPath := filepath.Join(featuresDir, "README.md")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		l.fail("verify/features/README.md", "index unreadable: %v", err)
		return
	}
	indexed := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		m := indexEntry.FindStringSubmatch(line)
		if m == nil {
			l.fail("verify/features/README.md", "index line %q is not `- [<id>](<id>.md): <text>`", line)
			continue
		}
		id, target := m[1], m[2]
		if target != id+".md" {
			l.fail("verify/features/README.md", "index entry %s links %q, want %q", id, target, id+".md")
			continue
		}
		if indexed[id] {
			l.fail("verify/features/README.md", "index entry %s repeated", id)
			continue
		}
		indexed[id] = true
	}

	entries, err := os.ReadDir(featuresDir)
	if err != nil {
		l.fail("verify/features", "unreadable: %v", err)
		return
	}
	files := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			l.fail("verify/features/"+e.Name(), "not a feature file (want <feature-id>.md)")
			continue
		}
		files[strings.TrimSuffix(e.Name(), ".md")] = true
	}

	for _, id := range sortedKeys(indexed) {
		if !files[id] {
			l.fail("verify/features/README.md", "index entry has no file: %s.md", id)
		}
	}
	for _, id := range sortedKeys(files) {
		rel := "verify/features/" + id + ".md"
		if !indexed[id] {
			l.fail(rel, "feature file not in index")
		}
		l.checkFeature(filepath.Join(featuresDir, id+".md"), rel)
		l.features++
	}
}

func (l *linter) checkFeature(path, rel string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		l.fail(rel, "unreadable: %v", err)
		return
	}
	var got []string
	harness := ""
	var drivingBody []string
	for _, s := range splitSections(string(raw)) {
		h := s.heading
		if id, ok := strings.CutPrefix(h, drivingPrefix); ok {
			harness = id
			drivingBody = s.body
			h = drivingPrefix + "<drive id>"
		}
		got = append(got, h)
	}
	want := []string{headingSubFeatures, headingHowToGet, drivingPrefix + "<drive id>", headingGotchas}

	present := map[string]int{}
	for _, h := range got {
		present[h]++
	}
	headingsOK := true
	for _, h := range want {
		switch present[h] {
		case 0:
			l.fail(rel, "missing H2 heading %q", h)
			headingsOK = false
		case 1:
		default:
			l.fail(rel, "H2 heading %q repeated", h)
			headingsOK = false
		}
	}
	for _, h := range got {
		if !contains(want, h) {
			l.fail(rel, "unexpected H2 heading %q", h)
			headingsOK = false
		}
	}
	if headingsOK && strings.Join(got, "|") != strings.Join(want, "|") {
		l.fail(rel, "H2 headings out of order: got %q, want %q", got, want)
	}

	if harness == "" {
		return
	}
	if !knownDrives[harness] {
		l.fail(rel, "unknown drive id %q (known: %s)", harness, strings.Join(sortedKeys(knownDrives), ", "))
		return
	}
	block, ok := driveFence(drivingBody)
	if !ok {
		l.fail(rel, "section %q has no ```drive block", drivingPrefix+harness)
		return
	}
	if _, err := parseDriveBlock(harness, block); err != nil {
		l.fail(rel, "%v", err)
	}
}

func (l *linter) checkASCII(dir string) {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(l.root, path)
		for i, b := range raw {
			if b > 0x7f {
				l.fail(filepath.ToSlash(rel), "non-ASCII byte 0x%02x at offset %d", b, i)
				break
			}
		}
		return nil
	})
	if err != nil {
		l.fail("verify", "unreadable: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
