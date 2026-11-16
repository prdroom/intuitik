//go:build windows

package credexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// detachedProcess is DETACHED_PROCESS: the started process has no console, so opening
// CONIN$ fails (E3).
const detachedProcess = 0x00000008

// run starts the helper entry of the intuitik executable, which puts itself and the
// program into a job object with kill-on-close and runs the program (D3). intuitik
// reads the helper's stdout to its end; at the end of ctx it terminates the helper,
// whose exit closes the job and ends every process in it. After "job refused <code>"
// the helper has exited without starting the program, and intuitik starts the program
// itself (O-T3b-1 (b)).
func run(ctx context.Context, s Spec, path string, admit Admit) Result {
	helper := s.Helper
	if helper == "" {
		exe, err := os.Executable()
		if err != nil {
			return Result{Class: ClassNotFound}
		}
		helper = exe
	}
	cmd := exec.Command(helper, append([]string{EntryName}, helperArgs(path, s.Args)...)...)
	cmd.Env = append(append(os.Environ(), s.Env...), s.HelperEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	res, out := start(ctx, cmd, s.Stderr, frameLimit, admit)
	if res.Closed || (res.Class != "" && res.Class != ClassExit) {
		res.Starts = 0
		return res
	}
	// The helper's own exit code is not an outcome: only its frame is (E7).
	f, ok := parseFrame(out)
	forced := false
	if !ok {
		// RED-STUB E7: a broken frame is handled as a refused job, so the program is
		// started again.
		f.refused, forced = true, true
	}
	if f.refused && !forced {
		// RED-STUB O-T3b-1 (b): a refused job ends the run as invalid without a start.
		return Result{Class: ClassInvalid, Job: JobRefused}
	}
	if !f.refused {
		r := Result{Class: f.class, Code: f.code, Starts: 1, Job: JobOK}
		if f.class == ClassOK {
			r.Stdout = f.stdout
		}
		return r
	}
	// The job was refused: the program runs without a job, started the same way the
	// helper starts it, under the same open check. Only the program itself is ended.
	prog := exec.Command(path, s.Args...)
	prog.Env = append(os.Environ(), s.Env...)
	prog.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	res, out = start(ctx, prog, s.Stderr, MaxOutput, admit)
	res.Job = JobRefused
	if res.Class != "" || res.Closed {
		return res
	}
	res.Class, res.Stdout = ClassOK, out
	return res
}

// start starts cmd through admit with stdin the null device, the given stderr and a
// stdout pipe, and reads at most limit bytes of stdout. It returns a result with a class
// for a timeout, an output over limit, a failed start or a non-zero exit (with the
// output read), or an empty class and the output when the process exited 0. Reading
// ends at most pipeGrace after the exit.
func start(ctx context.Context, cmd *exec.Cmd, stderr *os.File, limit int, admit Admit) (Result, []byte) {
	r, w, err := os.Pipe()
	if err != nil {
		return Result{Class: ClassNotFound}, nil
	}
	defer r.Close()
	cmd.Stdout = w
	if stderr != nil {
		cmd.Stderr = stderr
	}
	err = admit(cmd.Start)
	w.Close()
	if errors.Is(err, ErrClosed) {
		return Result{Closed: true}, nil
	}
	if err != nil {
		return Result{Class: ClassNotFound}, nil
	}
	over := make(chan struct{})
	read := make(chan *cappedBuffer, 1)
	go readCapped(r, limit, over, read)
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
		cmd.Process.Kill()
		<-exited
		res.Class = ClassTimeout
	case <-over:
		cmd.Process.Kill()
		<-exited
		res.Class = ClassInvalid
	}
	var out *cappedBuffer
	select {
	case out = <-read:
	case <-time.After(pipeGrace):
		r.Close()
		out = <-read
	}
	if res.Class != "" {
		return res, nil
	}
	if out.over {
		res.Class = ClassInvalid
		return res, nil
	}
	if code := st.ExitCode(); code != 0 {
		res.Class, res.Code = ClassExit, code
	}
	return res, out.buf
}
