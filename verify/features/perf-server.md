# perf-server

The server performance harness (packet FA2-T12): kwok clusters of the PRD sizes S, L and
XL, the release binary measured against them, and the judge `tools/perfjudge` printing,
for each measured quantity, the raw values, the computed value and PASS or FAIL against
the PRD number. Nobody reads a graph or adds up a log. Quantities: N-5 backend peak RSS
on L (maximum of 5 runs, limit 600,000,000 bytes); N-6 read pattern per graph call (list
requests per resource and their page size, from the API server audit log); N-6 / V-6a
idle window (list requests of the panel identity with their reasons); the L graph answer
file with its sha256. A harness PASS is not an N-5 or N-6 product PASS.

## Sub-features

The Drive of every sub-feature is the `perf-server` job: Go, `test/perf/server`, build
tag `kind`, list `test/perf/server/REQUIRED_TESTS_KIND`, test `TestPerfServer`, on the
release binary. The test creates its own kind cluster (`test/perf/server/kind-config.yaml`,
audit policy `test/perf/server/audit-policy.yaml`: Metadata for the ServiceAccounts
`intuitik-perf/panel` and `intuitik-perf/control` only), runs kwok v0.8.0 out of cluster
with `stage-fast.yaml` for the fake nodes, builds the composition of packet 6.2 (generator
`test/perf/server/gen`), and calls the judge once with what it recorded; the job log holds
every `PERF` line as printed. On pull_request the scale is S (one run, 60 s windows); on
workflow_dispatch it is L (5 runs, 10 minute windows) and then XL (information only).

- C1 Cluster size. Input: the harness at S and L. Expected: per kind
  `PERF count scale=<S|L> kind=<kind> want=<n> fixture=<n> read=<n>` and
  `PERF scale=<S|L> ok`; a read-back count different from the generator's table is
  `PERF ERROR count ...` (judge exit 2). Harness line `PERF-HARNESS check=C1-exact-counts`.
- C3 RSS sampler. Input: VmRSS of the binary every 1 s and VmHWM after the window.
  Expected: `PERF rss run=<i> samples=<n> interval=1s ... peak=<n>` with every sample above
  0 and at most VmHWM, and VmHWM at least 5,000,000 bytes. Harness line
  `PERF-HARNESS check=C3-band`. Unit: `TestRSSSampler`.
- C5 Identity filter. Input: the audit log of the cluster call and of the measured
  namespace call (S `perf-001`, L `perf-big`) and the served resources read with the
  control identity. Expected: `PERF C5 call=<call> user=<panel> matched=<n> K=<k> missing=none result=PASS`
  with n >= k. Harness line `PERF-HARNESS check=C5-set call=<call>`. Unit:
  `TestIdentityFilter`.
- C6 Idle detector. Input: window (2), the control client listing Pods of `perf-001` at a
  fixed interval (S 60 s every 6 s, L 10 minutes every 60 s); window (3a), row a (a watch
  with `resourceVersion=1` ending in ERROR 410, then a list); window (3b), rows a to d.
  Expected: `PERF C6 window=noisy ... expected=10 periodic=<10 +-1> ... result=FAIL`,
  `PERF C6 window=410-positive ... relist-410=1 unverified=0 periodic=0 result=PASS`,
  `PERF C6 window=410-mixed ... relist-410=1 unverified=2 periodic=1 result=FAIL`. Harness
  lines `PERF-HARNESS check=C6-noisy`, `check=C6-410-positive`, `check=C6-410-mixed`.
  Unit: `TestIdleDetector`.
- C7 Product measurement on L, 5 runs (dispatch run). Expected:
  `PERF N-5 runs=<5 peaks> max=<n>MB limit=600MB result=PASS|FAIL`;
  `PERF N-6 call=<cluster|perf-big> resource=<r> list=<n> limit=<v|none> continue=<n> get=<n> result=PASS|FAIL handled-in=T11`;
  `PERF N-6 idle window=product run=<i> length=600 ... info=PASS|FAIL judged-in=T11`;
  `PERF file cluster.json bytes=<n> sha256=<h>` and `PERF file l-graph.tar.gz bytes=<n> sha256=<h>`;
  the last line `PERF verdict=PASS|FAIL <failed items>`.
- C8 XL (dispatch run, information). Expected: the counts, `PERF rss run=1 ...`, and
  `PERF call run=1 name=<call> status=<n> bytes=<n> ms=<n>` for every call.

`map N/A: C2: N-5 judge over recorded peaks, unit only (TestN5Judge); its live line is C7.`
`map N/A: C4: page judge over recorded audit lines, unit only (TestPageJudge); its live lines are C7 and the S run.`

## How to get to it (user POV)

1. Point `intuitik` at a team cluster.
2. Open the cluster and every namespace (`GET /api/graph/cluster`, then
   `GET /api/graph/containment?namespace=<name>`).
3. The memory the binary holds on the user's PC and the reads it makes on the user's API
   server are what this harness measures; there is no screen for them (screen performance
   is U12).

## Driving it with http-get

The measurement needs a kind cluster, kwok and the audit log, so it is driven by
`TestPerfServer` in the `perf-server` job; its step `perf-server tests and list check`
fails unless every name of `test/perf/server/REQUIRED_TESTS_KIND` ran and passed, and the
test fails on judge exit 2 and on every harness check. This block is the part that needs
neither: the page is served without a session (the `verifymap run` launch has no
kubeconfig).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Tested on kind on ubuntu only; kwok fakes the nodes and the Pods, so no container runs.
- The product lists every kind in one request without `limit` today, so the N-6 page lines
  print FAIL with `limit=none` and `handled-in=T11` (P-T12-2). The product idle line is
  information until T11 (`judged-in=T11`, P-T12-1): before T11 nothing in the server
  reads periodically.
- At S the N-5 line is information; the limit applies to L.
- The L and XL lines come from one workflow_dispatch run bound to the PR head; a new head
  voids them.
