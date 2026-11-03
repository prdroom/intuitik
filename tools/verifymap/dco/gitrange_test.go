package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dcoBinary is the checker built once by TestMain; the integration tests execute it
// and check its real exit code.
var dcoBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dco-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "dco"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dcoBinary = filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", dcoBinary, ".")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building the dco binary:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type identity struct {
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	Signoff     string `json:"signoff"`
}

// scratchRepo is a fresh repository in a scratch directory with a fixed author.
type scratchRepo struct {
	t   *testing.T
	dir string
	env []string
	id  identity
}

func newScratchRepo(t *testing.T) *scratchRepo {
	t.Helper()
	raw, err := os.ReadFile("testdata/gitrange.json")
	if err != nil {
		t.Fatal(err)
	}
	var id identity
	if err := json.Unmarshal(raw, &id); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(emptyConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &scratchRepo{t: t, dir: dir, id: id, env: append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+emptyConfig,
		"GIT_AUTHOR_NAME="+id.AuthorName,
		"GIT_AUTHOR_EMAIL="+id.AuthorEmail,
		"GIT_COMMITTER_NAME="+id.AuthorName,
		"GIT_COMMITTER_EMAIL="+id.AuthorEmail,
	)}
	r.git("init", "-q")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *scratchRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit records an empty commit and returns its SHA; signed adds the sign-off
// paragraph of testdata/gitrange.json.
func (r *scratchRepo) commit(subject string, signed bool) string {
	r.t.Helper()
	args := []string{"commit", "-q", "--allow-empty", "-m", subject}
	if signed {
		args = append(args, "-m", r.id.Signoff)
	}
	r.git(args...)
	return r.git("rev-parse", "HEAD")
}

// runDCO executes the built checker in the repository and returns its exit code
// and output.
func (r *scratchRepo) runDCO(args ...string) (int, string) {
	r.t.Helper()
	cmd := exec.Command(dcoBinary, args...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			r.t.Fatalf("running the dco binary: %v", err)
		}
		code = exitErr.ExitCode()
	}
	r.t.Logf("dco %s -> exit %d\n%s", strings.Join(args, " "), code, out)
	return code, string(out)
}

func linesWithPrefix(out, prefix string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func TestRangeThreeSignedCommits(t *testing.T) {
	r := newScratchRepo(t)
	base := r.commit("base", true)
	r.commit("one", true)
	r.commit("two", true)
	head := r.commit("three", true)

	code, out := r.runDCO("-base", base, "-head", head)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if got := linesWithPrefix(out, "DCO FAIL"); len(got) != 0 {
		t.Fatalf("FAIL lines %q, want none", got)
	}
	if got := lastLine(out); got != "DCO RESULT checked=3 failed=0" {
		t.Fatalf("last line %q, want %q", got, "DCO RESULT checked=3 failed=0")
	}
}

func TestRangeLastCommitUnsigned(t *testing.T) {
	r := newScratchRepo(t)
	base := r.commit("base", true)
	r.commit("one", true)
	r.commit("two", true)
	head := r.commit("three unsigned", false)

	code, out := r.runDCO("-base", base, "-head", head)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	want := []string{"DCO FAIL " + head + " missing-signoff"}
	if got := linesWithPrefix(out, "DCO FAIL"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("FAIL lines %q, want %q", got, want)
	}
	if got := lastLine(out); got != "DCO RESULT checked=3 failed=1" {
		t.Fatalf("last line %q, want %q", got, "DCO RESULT checked=3 failed=1")
	}
}

func TestRangeMiddleCommitUnsigned(t *testing.T) {
	r := newScratchRepo(t)
	base := r.commit("base", true)
	r.commit("one", true)
	middle := r.commit("two unsigned", false)
	head := r.commit("three", true)

	code, out := r.runDCO("-base", base, "-head", head)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	want := []string{"DCO FAIL " + middle + " missing-signoff"}
	if got := linesWithPrefix(out, "DCO FAIL"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("FAIL lines %q, want %q", got, want)
	}
	if got := lastLine(out); got != "DCO RESULT checked=3 failed=1" {
		t.Fatalf("last line %q, want %q", got, "DCO RESULT checked=3 failed=1")
	}
}

func TestRangeUnsignedMergeCommitIsSkipped(t *testing.T) {
	r := newScratchRepo(t)
	base := r.commit("base", true)
	mainBranch := r.git("symbolic-ref", "--short", "HEAD")
	r.commit("main side", true)
	r.git("checkout", "-q", "-b", "side", base)
	r.commit("side branch", true)
	r.git("checkout", "-q", mainBranch)
	r.git("merge", "-q", "--no-ff", "--no-edit", "-m", "Merge branch side", "side")
	merge := r.git("rev-parse", "HEAD")

	code, out := r.runDCO("-base", base, "-head", merge)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	want := []string{"DCO SKIP " + merge + " merge-commit"}
	if got := linesWithPrefix(out, "DCO SKIP"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("SKIP lines %q, want %q", got, want)
	}
	if got := linesWithPrefix(out, "DCO FAIL"); len(got) != 0 {
		t.Fatalf("FAIL lines %q, want none", got)
	}
	if got := lastLine(out); got != "DCO RESULT checked=2 failed=0" {
		t.Fatalf("last line %q, want %q", got, "DCO RESULT checked=2 failed=0")
	}
}

func TestRangeBaseDoesNotExist(t *testing.T) {
	r := newScratchRepo(t)
	r.commit("base", true)
	head := r.commit("one", true)

	code, _ := r.runDCO("-base", "0123456789abcdef0123456789abcdef01234567", "-head", head)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestRangeBaseEqualsHead(t *testing.T) {
	r := newScratchRepo(t)
	r.commit("base", true)
	head := r.commit("one", true)

	code, _ := r.runDCO("-base", head, "-head", head)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestRangeArgumentNotFortyHex(t *testing.T) {
	r := newScratchRepo(t)
	r.commit("base", true)
	head := r.commit("one", true)

	code, _ := r.runDCO("-base", "abc123", "-head", head)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
