//go:build windows

package credexec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// HelperMain is the helper entry (E12, D3): args are the arguments after EntryName. It
// joins a job with kill-on-close, writes the control line, runs the program with no
// console, stdin the null device, its own stderr handle and a stdout pipe, writes the
// result frame and returns 0, so that its exit closes the job and ends every process
// left in it. A refused grammar returns EntryRefused and writes nothing.
func HelperMain(args []string, stdout io.Writer) int {
	command, rest, ok := parseEntry(args)
	if !ok {
		return EntryRefused
	}
	sm := seams()
	// RED-STUB E11: the helper reports the job without joining it, so only the program
	// is ended.
	var code syscall.Errno
	joined := true
	if sm.jobError != 0 {
		code, joined = sm.jobError, false
	}
	if !joined {
		fmt.Fprintf(stdout, "%s %d\n", JobRefused, uint32(code))
		return 0
	}
	fmt.Fprintf(stdout, "%s\n", JobOK)

	r, w, err := os.Pipe()
	if err != nil {
		stdout.Write(writeFrame(ClassNotFound, 0, nil))
		return 0
	}
	cmd := exec.Command(command, rest...)
	cmd.Stdout, cmd.Stderr = w, os.Stderr
	// RED-STUB acceptance 6 (E3): the program is started without DETACHED_PROCESS.
	err = cmd.Start()
	w.Close()
	if err != nil {
		stdout.Write(writeFrame(ClassNotFound, 0, nil))
		return 0
	}
	if sm.exitAfterStart {
		os.Exit(3)
	}
	over := make(chan struct{})
	read := make(chan *cappedBuffer, 1)
	go readCapped(r, MaxOutput, over, read)
	exited := make(chan *os.ProcessState, 1)
	go func() {
		st, _ := cmd.Process.Wait()
		exited <- st
	}()
	var st *os.ProcessState
	select {
	case st = <-exited:
	case <-over:
		cmd.Process.Kill()
		<-exited
		r.Close()
		<-read
		stdout.Write(writeFrame(ClassInvalid, 0, nil))
		return 0
	}
	var out *cappedBuffer
	select {
	case out = <-read:
	case <-time.After(pipeGrace):
		r.Close()
		out = <-read
	}
	switch {
	case out.over:
		stdout.Write(writeFrame(ClassInvalid, 0, nil))
	case st.ExitCode() != 0:
		stdout.Write(writeFrame(ClassExit, st.ExitCode(), nil))
	default:
		stdout.Write(writeFrame(ClassOK, 0, out.buf))
	}
	return 0
}
