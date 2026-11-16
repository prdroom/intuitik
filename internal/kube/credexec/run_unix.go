//go:build !windows

package credexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// run starts the program in a new session and process group (Setsid: no controlling
// terminal, so opening /dev/tty fails; E3), with stdin the null device (E2), stderr the
// given handle and stdout a pipe. After the program exits, and at the end of ctx, the
// whole group gets SIGKILL (E11).
func run(ctx context.Context, s Spec, path string, admit Admit) Result {
	r, w, err := os.Pipe()
	if err != nil {
		return Result{Class: ClassNotFound}
	}
	defer r.Close()
	cmd := exec.Command(path, s.Args...)
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdout = w
	if s.Stderr != nil {
		cmd.Stderr = s.Stderr
	}
	// RED-STUB acceptance 6 (E2, E3): stdin passed through and no new session.
	cmd.Stdin = os.Stdin
	err = admit(cmd.Start)
	w.Close()
	if errors.Is(err, ErrClosed) {
		return Result{Closed: true}
	}
	if err != nil {
		return Result{Class: ClassNotFound}
	}
	// RED-STUB E11: only the program is ended, not its group.
	end := func() { cmd.Process.Kill() }

	over := make(chan struct{})
	read := make(chan *cappedBuffer, 1)
	go readCapped(r, MaxOutput, over, read)
	exited := make(chan *os.ProcessState, 1)
	go func() {
		st, _ := cmd.Process.Wait()
		exited <- st
	}()

	res := Result{Starts: 1}
	var st *os.ProcessState
	select {
	case st = <-exited:
	case <-ctx.Done():
		end()
		<-exited
		res.Class = ClassTimeout
	case <-over:
		end()
		<-exited
		res.Class = ClassInvalid
	}
	// The program has exited: end what it left in its group, which also closes a pipe
	// that a descendant held, and stop reading at most pipeGrace later.
	end()
	var out *cappedBuffer
	select {
	case out = <-read:
	case <-time.After(pipeGrace):
		r.Close()
		out = <-read
	}
	if res.Class != "" {
		return res
	}
	if out.over {
		res.Class = ClassInvalid
		return res
	}
	code := st.ExitCode()
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
	}
	if code != 0 {
		res.Class, res.Code = ClassExit, code
		return res
	}
	res.Class, res.Stdout = ClassOK, out.buf
	return res
}
