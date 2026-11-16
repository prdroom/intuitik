package credexec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake credential program (testdata/credfake), built once per test binary, and a
// copy of it under another name as the descendant executable.
var (
	fakeOnce  sync.Once
	fakeProg  string
	fakeChild string
	fakeErr   error
)

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func fakeProgram(t *testing.T) (program, child string) {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "credfake-")
		if err != nil {
			fakeErr = err
			return
		}
		fakeProg = filepath.Join(dir, exeName("credfake"))
		out, err := exec.Command("go", "build", "-o", fakeProg, "./testdata/credfake").CombinedOutput()
		if err != nil {
			fakeErr = fmt.Errorf("go build credfake: %v: %s", err, out)
			return
		}
		raw, err := os.ReadFile(fakeProg)
		if err != nil {
			fakeErr = err
			return
		}
		fakeChild = filepath.Join(dir, exeName("credchild"))
		fakeErr = os.WriteFile(fakeChild, raw, 0o700)
	})
	if fakeErr != nil {
		t.Fatalf("fake program: %v", fakeErr)
	}
	return fakeProg, fakeChild
}

// fakeRun is the directory and environment of one fake program run.
type fakeRun struct {
	dir string
	env []string
}

func newFakeRun(t *testing.T, plan string) *fakeRun {
	t.Helper()
	_, child := fakeProgram(t)
	dir := t.TempDir()
	return &fakeRun{dir: dir, env: []string{"CREDFAKE_DIR=" + dir, "CREDFAKE_CHILD=" + child, "CREDFAKE_RUN=" + plan,
		`KUBERNETES_EXEC_INFO={"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":false}}`}}
}

func (f *fakeRun) lines(name string) []string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, name))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// starts are the recorded starts: PID and parent PID of each.
func (f *fakeRun) starts() [][2]int {
	var out [][2]int
	for _, l := range f.lines("starts") {
		fields := strings.Fields(l)
		if len(fields) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(fields[1])
		ppid, _ := strconv.Atoi(fields[2])
		out = append(out, [2]int{pid, ppid})
	}
	return out
}

func (f *fakeRun) descendants() []int {
	var out []int
	for _, l := range f.lines("descendants") {
		pid, _ := strconv.Atoi(l)
		out = append(out, pid)
	}
	return out
}

// pids are every recorded PID of the run: the program and its descendants.
func (f *fakeRun) pids() []int {
	var out []int
	for _, s := range f.starts() {
		out = append(out, s[0])
	}
	return append(out, f.descendants()...)
}

// waitGone waits until none of pids is alive, for at most limit, and returns the PIDs
// still alive then and how long it took.
func waitGone(pids []int, limit time.Duration) ([]int, time.Duration) {
	start := time.Now()
	for {
		var alive []int
		for _, p := range pids {
			if processAlive(p) {
				alive = append(alive, p)
			}
		}
		if len(alive) == 0 || time.Since(start) > limit {
			return alive, time.Since(start)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// runToken is a credential value made at run time, so that no credential-like literal
// is committed.
func runToken(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("run-%d-%d", os.Getpid(), time.Now().UnixNano())
}

// openAdmit is an Admit that is always open.
func openAdmit(start func() error) error { return start() }
