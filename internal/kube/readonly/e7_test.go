package readonly

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// classCase is one run of the fake program and the K-EP reason it must give.
type classCase struct {
	name, plan, command, reason string
	deadline                    time.Duration
}

// runClass runs one case through credexec in a generation of its own (the runs of
// internal/server) and returns what the source made of it, the K-EP text and the
// directory of the program's records.
func runClass(t *testing.T, c classCase) (obtained, string, string) {
	t.Helper()
	prog, _ := fakeProgram(t)
	dir := t.TempDir()
	command := prog
	if c.command != "" {
		command = c.command
	}
	p, _ := execProgramFor(command, fakeEnv(t, dir, c.plan))
	g := NewGeneration(context.Background(), credexecRunner(helperBinary(t), nil), nil)
	t.Cleanup(g.Close)
	g.setProgram(p, execV1, command)
	ctx, cancel := context.WithTimeout(context.Background(), c.deadline)
	defer cancel()
	got := g.current(ctx)
	return got, kEP(command, "e7-context", got.fail), dir
}

// TestE7_OutcomeClasses (packet M1-T3b E7, EP-R): each class of a run gives its K-EP
// reason; the class comes from the run itself. ok gives the credential and no reason.
func TestE7_OutcomeClasses(t *testing.T) {
	missing := filepath.Join(os.TempDir(), "intuitik-no-such-credential-program")
	for _, c := range []classCase{
		{"ok", "token=e7-token", "", "", 10 * time.Second},
		{"exit", "exit=1", "", "exit code 1", 10 * time.Second},
		{"not-found", "", missing, "not found", 10 * time.Second},
		{"timeout", "hang", "", "no answer within 10 s", 2 * time.Second},
		{"invalid", "version=client.authentication.k8s.io/v1beta1;token=e7-token", "", "no valid credential in its output", 10 * time.Second},
	} {
		got, line, _ := runClass(t, c)
		t.Logf("E7 class %s: credential %v, reason %q (want %q); K-EP %q", c.name, got.cred != nil, got.fail, c.reason, line)
		if c.reason == "" {
			if got.cred == nil || got.fail != "" {
				t.Errorf("E7 class %s: credential %v reason %q, want the credential and no reason", c.name, got.cred != nil, got.fail)
			}
			continue
		}
		want := text(textEP, func() string {
			if c.command != "" {
				return c.command
			}
			p, _ := fakeProgram(t)
			return p
		}(), "e7-context", c.reason)
		if got.fail != c.reason || line != want {
			t.Errorf("E7 class %s: reason %q, K-EP\n  actual   %q\n  expected %q", c.name, got.fail, line, want)
		}
	}
}
