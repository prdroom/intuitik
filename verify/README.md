# Verification map

Reviewer does not run Builder code. This map is the reference for "the test walks
the user path": each feature file states how a user reaches the feature and how CI
drives it, and the `verify-map` step of the `build` job runs it end to end on every
PR (`tools/verifymap`, subcommands `lint` and `run <feature-id>`).

Rules, also applied by Reviewer:

- Do not weaken a scenario to make it pass.
- Do not report a skipped entry path as verified through another path.
- Confirm a stored result with a second read.

The index of features is `features/README.md`. A feature file has exactly four H2
headings, in order: `Sub-features`, `How to get to it (user POV)`,
`Driving it with <drive id>`, `Gotchas`. The Drive section holds a fenced `drive`
block with the request and the expected response. Known drive ids: `http-get`.
All files under `verify/` are ASCII.

## Launch

The runner creates a new empty HOME directory for the run and starts the binary
with that HOME and without `KUBECONFIG`, with no arguments. Ready means the binary
prints its listen address on one stdout line, `listening on http://127.0.0.1:<port>`,
and that address answers `GET /` with HTTP 200. The runner polls every 100 ms with
a 30 s limit (runner flags `-poll` and `-ready-timeout`; not a performance
criterion). If the binary is not ready within the limit, the runner prints a Launch
failure reason, runs Cleanup and exits non-zero.

## Doctor

Read only. The runner prints the `commit=` field of `<binary> --version`, the PID,
the listen address and the status of `GET /`.

## Drive

The runner sends the request of the feature's `drive` block to the listen address
and checks the status code, the media type of `Content-Type` and that the body
contains the stated text. Every mismatch is a failure line and a non-zero exit.

## Evidence

The runner writes `<feature-id>-doctor.txt` (Doctor fields) and
`<feature-id>-drive.txt` (request, status line, headers and body) to the
`-evidence` directory and prints each file's path and sha256.

## Cleanup

The runner stops the process with SIGTERM and prints its exit code (non-zero, or no
exit within 10 s, is a failure), checks that the port no longer accepts connections,
deletes the HOME directory and keeps the evidence files. After cleanup it reads each
evidence file again and compares its sha256 with the value recorded at write time
(second read). Then it prints two exact comparisons and fails if either differs:
the `--version` commit against `-expect-commit` (all 40 hex characters), and the
sha256 of the executed binary against its line in the build's `SHA256SUMS`.
