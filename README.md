# intuitik

## Contexts that sign in with a credential program

Many cloud kubeconfigs (for example those written by `aws eks update-kubeconfig`,
`gcloud container clusters get-credentials` and `kubelogin convert-kubeconfig`) name a
credential program in the user entry. intuitik runs that program, as kubectl does,
to get a credential, and runs it again when the credential expires or the cluster
rejects it.

- The program must give a credential without input. Log in with your cloud's login
  command first (for example `aws sso login`, `gcloud auth login`, `az login`).
- A user entry with `interactiveMode: Always` is not run: intuitik reports
  `needs input from a terminal`.
- If the user entry also holds a token, a token file, a username and password, or a
  client certificate and key, intuitik uses that credential and does not run the
  program, as kubectl does.
- intuitik gives the program no terminal and no input. A program that asks for a code
  or a password then fails, or, if it keeps waiting, is ended after 10 s with
  `no answer within 10 s`. A program that waits for a browser or device-code login is
  ended the same way.
- If logging in first does not help (for example a login that asks for input every
  time, or a login cache that your system does not keep), the context does not connect
  in intuitik. Check the authentication settings of the user entry in your kubeconfig
  and your cloud CLI's documentation for a login mode that works without input.
- Connecting, including the program's run, takes at most 10 s. A failure prints one
  line that names the program, the context and the reason.
- What the program writes to its own error output, if anything, appears in the
  terminal where you started intuitik, as with kubectl. intuitik never shows the
  credential the program prints in its output, API or browser page, and never writes
  it to disk. What the program itself stores (its own login cache) is up to that
  program.
- Provider compatibility is not verified. intuitik is tested with test programs that
  speak the standard client.authentication.k8s.io protocol, not with the AWS, Google
  Cloud or Azure programs themselves.

Limits:
- The program is your own kubeconfig command. intuitik does not sandbox it, as kubectl
  does not.
- When a run ends, intuitik ends the program and the processes it started. On Linux
  and macOS a process that left the program's process group on its own is not ended.
  Process cleanup on macOS is not verified yet.
- Where Windows does not let intuitik put the program in a job object, only the
  program itself is ended, not the processes it started.
