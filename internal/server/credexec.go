package server

import (
	"context"
	"os"

	"github.com/prdroom/intuitik/internal/kube/credexec"
	"github.com/prdroom/intuitik/internal/kube/readonly"
)

// runCredentialProgram is the runner of the cluster's generations (packet M1-T3b D3): it
// runs the credential program through internal/kube/credexec with intuitik's own stderr
// handle, so that what the program writes there reaches the terminal as with kubectl.
func runCredentialProgram(ctx context.Context, p readonly.Program, admit func(start func() error) error) readonly.RunResult {
	r := credexec.Run(ctx, credexec.Spec{Command: p.Command, Args: p.Args, Env: p.Env, Stderr: os.Stderr}, func(start func() error) error {
		started := false
		err := admit(func() error {
			started = true
			return start()
		})
		if err != nil && !started {
			return credexec.ErrClosed
		}
		return err
	})
	return readonly.RunResult{Class: r.Class, Code: r.Code, Stdout: r.Stdout, Closed: r.Closed}
}
