// Package credexec runs the credential program of a kubeconfig user entry (packet M1-T3b
// D3). It knows nothing of the ExecCredential protocol, which internal/kube/readonly
// implements, and imports no client-go package (R1).
//
// A run gives the program no input (stdin is the null device), no controlling terminal
// or console, intuitik's own stderr handle and a stdout pipe. It ends the program and
// the processes it started when the program exits or the run's context ends: on Linux
// and macOS through the program's process group, on Windows through a job object that
// the helper entry (intuitik __credexec-run) creates for itself and the program.
package credexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// Classes of a run's outcome (E7). Each comes from the process result of the run itself.
const (
	ClassOK       = "ok"        // the program exited 0; Stdout holds its output
	ClassExit     = "exit"      // the program exited with the non-zero code Code
	ClassNotFound = "not-found" // the command could not be found or started
	ClassTimeout  = "timeout"   // the run's context ended before the program did
	ClassInvalid  = "invalid"   // the output was over MaxOutput, or the helper's frame broke E7
)

// MaxOutput is the most stdout a run keeps (1 MiB). More is class invalid and the
// program is ended.
const MaxOutput = 1 << 20

// pipeGrace bounds the read of stdout after the program exited: a descendant that holds
// the pipe cannot keep the run open (E11).
const pipeGrace = 200 * time.Millisecond

// Job results of a Windows run; empty elsewhere.
const (
	JobOK      = "job ok"
	JobRefused = "job refused"
)

// ErrClosed is returned by an Admit whose owner is closed: the program was not started.
var ErrClosed = errors.New("credexec: closed")

// Spec is one run of a credential program.
type Spec struct {
	Command string   // the exec entry's command; a bare name is looked up in intuitik's PATH
	Args    []string // the exec entry's arguments
	Env     []string // added to intuitik's environment, as NAME=value
	Stderr  *os.File // the program's stderr handle; nil is the null device
	// Helper is the intuitik executable whose helper entry runs the program on Windows;
	// "" is the running executable. HelperEnv is added to the helper's environment
	// only. Both are ignored on other systems.
	Helper    string
	HelperEnv []string
}

// Result is the outcome of one run.
type Result struct {
	Class  string
	Code   int    // the program's exit code for ClassExit
	Stdout []byte // the program's stdout for ClassOK
	Starts int    // how many times this run started the program (0 or 1)
	Job    string // JobOK or JobRefused on Windows; "" elsewhere and when nothing started
	Closed bool   // Admit refused the start: the run's owner was closed
}

// Admit is the caller's start gate (E5): it runs start inside the caller's lock after
// the caller checked that it is still open, and returns ErrClosed without calling start
// when it is not. The process is started inside start and only there.
type Admit func(start func() error) error

// Run runs the program of s under ctx and returns its outcome. The program is started
// only through admit.
func Run(ctx context.Context, s Spec, admit Admit) Result {
	path, err := lookPath(s.Command)
	if err != nil {
		return Result{Class: ClassNotFound}
	}
	return run(ctx, s, path, admit)
}

// lookPath resolves a bare command name in intuitik's PATH, as kubectl does with its own
// PATH; a command with a path separator is used as it is.
func lookPath(command string) (string, error) {
	if command == "" {
		return "", exec.ErrNotFound
	}
	path, err := exec.LookPath(command)
	if errors.Is(err, exec.ErrDot) {
		err = nil
	}
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", exec.ErrNotFound
	}
	return path, nil
}

// cappedBuffer keeps at most max bytes and reports when more arrived.
type cappedBuffer struct {
	max  int
	buf  []byte
	over bool
}

func (c *cappedBuffer) add(p []byte) {
	if c.over {
		return
	}
	if len(c.buf)+len(p) > c.max {
		c.over = true
		return
	}
	c.buf = append(c.buf, p...)
}

// readCapped reads r until EOF or an error into a buffer of at most max bytes. over is
// closed as soon as more than max bytes arrived; done is closed when reading ends.
func readCapped(r *os.File, max int, over chan<- struct{}, done chan<- *cappedBuffer) {
	b := &cappedBuffer{max: max}
	chunk := make([]byte, 32*1024)
	signalled := false
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			b.add(chunk[:n])
			if b.over && !signalled {
				signalled = true
				close(over)
			}
		}
		if err != nil {
			break
		}
	}
	done <- b
}
