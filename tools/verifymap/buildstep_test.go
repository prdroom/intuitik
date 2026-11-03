package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Criterion 1 is the installed `Build six targets` step of the build job. These
// tests read that step from .github/workflows/ci.yml (read only) and run its script
// with bash against fixture modules, the way the job runs it.

var sixTargets = []string{
	"intuitik-darwin-amd64",
	"intuitik-darwin-arm64",
	"intuitik-linux-amd64",
	"intuitik-linux-arm64",
	"intuitik-windows-amd64.exe",
	"intuitik-windows-arm64.exe",
}

// sha256sum prints `<sha256>  <name>`; in binary mode (Git for Windows) the second
// separator character is `*`.
var sumLine = regexp.MustCompile(`^([0-9a-f]{64}) [ *](intuitik-[a-z0-9]+-[a-z0-9]+(\.exe)?)$`)

const workflowPath = "../../.github/workflows/ci.yml"

// runBuildStep copies a fixture into a fresh git repository, adds its go.mod (no
// go.mod is committed outside the repository root), and runs the step script there.
func runBuildStep(t *testing.T, fixture string) (int, string, string) {
	t.Helper()
	script, env, err := workflowStep(workflowPath, "build", "Build six targets")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "CGO_ENABLED=0 ") {
		t.Fatalf("the step script does not build with CGO_ENABLED=0:\n%s", script)
	}

	repo := t.TempDir()
	copyTree(t, filepath.Join("testdata", "buildstep", fixture), repo)
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module buildstepfixture\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=fixture", "-c", "user.email=fixture", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	scriptFile := filepath.Join(t.TempDir(), "step.sh")
	if err := os.WriteFile(scriptFile, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	runnerTemp := t.TempDir()
	cmd := exec.Command("bash", "-e", scriptFile)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+runnerTemp)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running the step: %v", err)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("Build six targets on fixture %s -> exit %d\n%s", fixture, code, out)
	return code, string(out), runnerTemp
}

// Criterion 1, success case: one artifact per target and one line per target with
// the file name and its sha256.
func TestBuildStepBuildsSixTargets(t *testing.T) {
	code, out, runnerTemp := runBuildStep(t, "ok")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		m := sumLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		names = append(names, m[2])
		raw, err := os.ReadFile(filepath.Join(runnerTemp, "dist", m[2]))
		if err != nil {
			t.Fatalf("artifact %s: %v", m[2], err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != m[1] {
			t.Fatalf("%s: printed sha256 %s, file sha256 %s", m[2], m[1], got)
		}
	}
	if strings.Join(names, " ") != strings.Join(sixTargets, " ") {
		t.Fatalf("printed targets %q, want %q", names, sixTargets)
	}
	sums, err := os.ReadFile(filepath.Join(runnerTemp, "dist", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(sums)), "\n")); n != len(sixTargets) {
		t.Fatalf("SHA256SUMS has %d lines, want %d", n, len(sixTargets))
	}
}

// Criterion 1, failure case: when one target fails to build, the step fails.
func TestBuildStepFailsWhenOneTargetFails(t *testing.T) {
	code, out, _ := runBuildStep(t, "windows-broken")
	if code == 0 {
		t.Fatal("exit 0, want non-zero when the windows targets do not compile")
	}
	if !strings.Contains(out, "undefinedOnWindows") {
		t.Fatal("the output does not show the windows compile error")
	}
	if strings.Contains(out, "intuitik-windows-amd64.exe\n") || strings.Contains(out, "intuitik-windows-arm64.exe\n") {
		t.Fatal("a sha256 line was printed for a target that failed to build")
	}
}

func TestWorkflowStepReadsRunAndEnv(t *testing.T) {
	script, env, err := workflowStep(workflowPath, "build", "Build six targets")
	if err != nil {
		t.Fatal(err)
	}
	if env["VERSION"] != "0.0.0-dev" {
		t.Errorf("env VERSION = %q, want 0.0.0-dev", env["VERSION"])
	}
	if !strings.HasPrefix(script, "set -euo pipefail\n") {
		t.Errorf("script does not start with set -euo pipefail:\n%s", script)
	}
	if _, _, err := workflowStep(workflowPath, "build", "No such step"); err == nil {
		t.Error("workflowStep found a step that does not exist")
	}
	if _, _, err := workflowStep(workflowPath, "no-such-job", "Build six targets"); err == nil {
		t.Error("workflowStep found a job that does not exist")
	}
}
