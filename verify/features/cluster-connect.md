# cluster-connect

intuitik connects to the current context of the user's kubeconfig, found by the
standard loading rules (`KUBECONFIG`, which may list several files, else
`~/.kube/config`). Behind the login session, `GET /api/cluster` reports the active
context, its cluster and API server address, every context of the kubeconfig and
one connection state; `POST /api/cluster/context` switches to another listed
context and `POST /api/cluster/retry` tries again. A failed attempt reports one
cause and one fix, prints the same line on stderr, and the process keeps running.
The kubeconfig files are never written (packet M1-T3a sections 1, 3 and 6).

## Sub-features

Each sub-feature names its input, its expected output and the CI test that drives
it. All run in the `e2e-kind` job (Go, `test/e2e-api/connect`, build tag `kind`)
against a kind cluster, except the two marked job `unit`; their output lines name
the state and text id they check.

- Current context connects. Input: the kind kubeconfig. Output: `connected`, K-OK
  on stdout, the kind context marked current and active. `TestK_States/K-OK`.
- No kubeconfig. Input: `KUBECONFIG` points to a missing file. Output:
  `no-kubeconfig`, K-NK with that path, an empty context list, the process still
  running and `GET /` 200. `TestK_States/K-NK`, `TestK_KubeconfigLoading`.
- Kubeconfig created after start. Input: start as above, write the kind
  kubeconfig at that path, `POST /api/cluster/retry`. Output: `connected`, K-OK.
  `TestK_RetryRereadsKubeconfig`.
- Malformed kubeconfig. Output: `kubeconfig-invalid`, K-IV1. `TestK_States/K-IV1`.
- No current context. Output: `kubeconfig-invalid`, K-IV2; switching to a listed
  context then connects. `TestK_States/K-IV2`.
- Context names a missing cluster entry. Output: `kubeconfig-invalid`, K-IV3.
  `TestK_States/K-IV3`.
- Kubeconfig with no context. Output: `kubeconfig-invalid`, K-IV4.
  `TestK_States/K-IV4`.
- Rejected credentials. Input: a malformed token, and the token of a deleted
  ServiceAccount. Output: `credentials-rejected`, K-AU. `TestK_CredentialsRejected`,
  `TestK_ConnectedNeedsAuthenticatedAnswer`.
- No credentials. Input: a context whose user entry has none. Output:
  `credentials-missing`, K-NC (kind answers the anonymous request with HTTP 403;
  a test server with HTTP 401). `TestK_AnonymousForbidden`,
  `TestK_AnonymousUnauthorized`.
- Credential program connects. Input: a context whose exec entry runs a test
  program that prints a valid credential. Output: `connected`, K-OK.
  `TestK_ExecConnects`.
- Credential program fails. Input: a program that exits 1, a missing command, a
  program that hangs, a program with invalid output, an entry with
  `interactiveMode: Always`. Output: `credential-program-failed`, K-EP with exactly
  one of the five reasons; the hang decided within W1 + 1 s; the `Always` program not
  started. `TestK_ExecTimeout`, `TestK_ExecAlwaysNotRun`, and the K-EP cases named in
  6.4.
- Credential refresh. Input: an expired credential; a 401 after `connected`; a
  rejected new credential; a failed refresh run; two reads at once. Output: still
  `connected`; K-AU; K-EP with the run's reason; one run for both reads.
  `TestE6_RefreshContract` (job `readonly-gate`), `TestK_ExecRefresh`,
  `TestK_ExecRefreshBeforeW1`, `TestK_ExecRefreshAfterW1`, `TestK_ExecRefreshTimeout`.
- Credential program cancelled. Input: a switch while the first run or a refresh run
  is running; a retry. Output: only the newest attempt's line and state; the old
  program ended. `TestK_ExecCancelledBySwitch`, `TestK_ExecSwitchDuringRefresh`,
  `TestK_ExecRetryRunsAgain`, `TestK_ExecLateKubeconfigRead`.
- Kubeconfig read blocked. Input: a kubeconfig read held past W1. Output: the K-UR
  line with `(unknown)` and the UR-K value, once. `TestK_KubeconfigReadBlocked`;
  every text with a server, cluster or context slot prints `(unknown)` for an empty
  value, `TestK_UnknownSlotsFormat` (job `readonly-gate`).
- No terminal input for the program. `TestK_ExecNoStdinOnTerminal`, `TestRun_NoTTY`
  (Linux), `TestRun_NoConsole` (Windows).
- Processes ended. `TestRun_*` of 6.4 (Linux in `readonly-gate`, Windows in
  `credexec-windows`).
- Static credential wins. Input: a user entry with an exec entry and a token, token
  file, basic auth or certificate pair. Output: the program is not run; the server
  receives the static credential. `TestE10_AuthPrecedence` (job `readonly-gate`).
- Unsupported auth provider. Input: `auth-provider: {name: gcp}`. Output:
  `auth-provider-unsupported`, K-AP. `TestK_States/K-AP`.
- Certificate mismatch. Input: the kind server with a CA that is not its own.
  Output: `certificate-mismatch`, K-TL. `TestK_States/K-TL`.
- Unreachable. Input: a closed local port; a server that never completes the TLS
  handshake; a server that holds the HTTP answer; a token file that gives the token
  only 6 s after the attempt opens it, then the held server. Output: `unreachable`,
  K-UR with the OS error, or with `no answer within 10 s` within W1 plus 2 s of the
  start of the attempt. `TestK_UnreachableTimeout`, `TestK_States/K-UR`.
- Not the Kubernetes API. Input: a server that answers HTTP 500. Output:
  `unreachable`, K-HS. `TestK_States/K-HS`. A redirect answer (301, 302, 303, 307,
  308 with a Location) is not followed: K-HS with its own status, one request
  (`TestK_RedirectNotFollowed`, job `unit`).
- 403 with credentials. Output: `connected`. `TestK_ForbiddenWithCredentialsIsConnected`.
- Switch and back. Input: switch to an unreachable context, then back. Output:
  K-UR, then K-OK; every kubeconfig file keeps its sha256 and mtime.
  `TestK_KubeconfigUntouched`, `TestK_SwitchCancelsRunningAttempt`.
- Unknown context name. Output: 400 with K-UC, state unchanged.
  `TestK_SwitchUnknownContext`.
- Switch and retry answer. Output: 202 with state `connecting`, also when the attempt
  has finished before the answer is written. `TestK_APIShape`,
  `TestK_ConnectingAnswer` (job `unit`).
- Two files in `KUBECONFIG`. Output: the contexts of both files; the current
  context comes from the first file that sets one. `TestK_KubeconfigLoading`.
- No session. Output: every T3 route answers 401 with no context, cluster or
  server name in the body. `TestK_NoSessionNoClusterData`.
- Cluster routes behind the gate. Output: 503 with the state and text in every
  failure state, the handler not called. `TestK_ClusterRouteGate`.
- No credential in any output. `TestK_CredentialFreeOutput`,
  `TestK_ExecCredentialFreeOutput`, `TestK_ExecStderrPassesThrough`.

## How to get to it (user POV)

1. Make sure `kubectl config current-context` names the cluster you want.
2. Run `intuitik` in a terminal.
3. It prints `listening on http://127.0.0.1:<port>`, then
   `Connected to cluster <cluster> (context <context>).`, or one line on stderr
   that names the cause and the fix. For a context with a credential program, log
   in with your cloud's login command first.
4. The panel tab reads the state from `GET /api/cluster`; a context switch or a
   retry starts a new attempt.

## Driving it with http-get

The cluster paths need a login session and a kind cluster, so they are driven by
the CI tests named under Sub-features in the `e2e-kind` job; its step
`e2e-kind tests and list check` fails unless every name of
`test/e2e-api/connect/REQUIRED_TESTS_KIND` ran and passed. This block is the part
that needs neither: the page is served without a session while the connection
state is not connected (the `verifymap run` launch has no kubeconfig).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Contexts that sign in with a credential program are tested with test programs of
  the standard protocol, on ubuntu (kind) and on Windows (process cleanup). The AWS,
  Google Cloud and Azure programs themselves are not run; macOS process cleanup is not
  verified yet.
- A program that waits for input or a browser login does not connect; see the
  README section.
- The `KUBECONFIG` list separator is tested on linux (`:`) only; the Windows
  separator (`;`) is not tested in T3.
- Real token expiry over time is not waited for; a token the server rejects stands
  in (item D4), because intuitik sees the same 401 in both cases.
- `credentials-missing` against a cluster with anonymous authentication off is
  shown with a local TLS test server that answers 401, not with a second kind
  cluster.
- The product writes no access log; the credential search covers stdout, stderr
  and every raw T3 API response of the e2e-kind run.
- `KUBECONFIG` is read from the process environment at start; a change made in
  another shell needs a restart. A file created or changed after start is read by
  the next attempt.
