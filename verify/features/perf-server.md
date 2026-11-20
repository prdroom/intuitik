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
release binary. The test creates two kind clusters, one after the other, with the same
audit policy `test/perf/server/audit-policy.yaml` (Metadata for the ServiceAccounts
`intuitik-perf/panel` and `intuitik-perf/control` only), each with its own kubeconfig,
tokens, node, audit log read and cause records:
- the measurement cluster `intuitik-perf` (`test/perf/server/kind-config.yaml`, etcd
  compaction and watch cache at kube-apiserver defaults): kwok v0.8.0 out of cluster with
  `stage-fast.yaml` for the fake nodes, the composition of packet 6.2 (generator
  `test/perf/server/gen`), the runs, the product and noisy windows, and the judge
  (`perfjudge.Judge`); it is deleted when its measurements are done;
- the 410 reproduction cluster `intuitik-perf-410` (`test/perf/server/kind-config-410.yaml`,
  etcd compaction every 30 s, watch cache off for batch cronjobs), created only after every
  measurement: namespaces `perf-001` and `perf-002`, no workload, the setup probe, windows
  3a and 3b, and the judge (`perfjudge.JudgeRepro`).

Every `PERF` line names its cluster (`cluster=intuitik-perf` or `cluster=intuitik-perf-410`);
the job log holds every `PERF` line as printed. On pull_request the scale is S (one run,
60 s windows), then the reproduction cluster; on workflow_dispatch it is L (5 runs,
10 minute windows) and then XL (information only), then the reproduction cluster.

- C1 Cluster size. Input: the harness at S and L. Expected: per kind
  `PERF count scale=<S|L> kind=<kind> want=<n> fixture=<n> read=<n>` and
  `PERF scale=<S|L> ok`; a read-back count different from the generator's table is
  `PERF ERROR count ...` (judge exit 2). Harness line `PERF-HARNESS check=C1-exact-counts`.
- C3 RSS sampler. Input: VmRSS of the binary every 1 s and VmHWM after the window.
  Expected: `PERF rss run=<i> samples=<n> interval=1s ... peak=<n>` with at least one
  sample, every sample above 0, no read error, and VmHWM at least 5,000,000 bytes; the peak
  is max(VmHWM, samples), so a sample above VmHWM is no FAIL (decision C3-BAND-T12).
  Harness line `PERF-HARNESS check=C3-band`. Unit: `TestRSSSampler`, `TestRSSBand`.
- C5 Identity filter. Input: the audit log of the cluster call and of the measured
  namespace call (S `perf-001`, L `perf-big`) and the served resources read with the
  control identity. Expected: `PERF C5 call=<call> user=<panel> matched=<n> K=<k> missing=none result=PASS`
  with n >= k. Harness line `PERF-HARNESS check=C5-set call=<call>`. Unit:
  `TestIdentityFilter`.
- C6 Idle detector. Input: in the measurement cluster, window (2), the control client
  listing Pods of `perf-001` at a fixed interval (S 60 s every 6 s, L 10 minutes every
  60 s); in the reproduction cluster, on batch/v1 cronjobs, window (3a), row a (a watch of
  `perf-001` with `resourceVersion=1` ending in ERROR 410, then a list), and window (3b),
  rows a to d. Expected: `PERF C6 window=noisy ... expected=10 periodic=<10 +-1> ...
  result=FAIL cluster=intuitik-perf`,
  `PERF C6 window=410-positive ... relist-410=1 unverified=0 periodic=0 result=PASS cluster=intuitik-perf-410`,
  `PERF C6 window=410-mixed ... relist-410=1 unverified=2 periodic=1 result=FAIL cluster=intuitik-perf-410`;
  for each row a watch, `PERF-ROWA` lines with the client's ERROR 410 event and the audit
  lines of its Audit-Id. Harness lines `PERF-HARNESS check=C6-noisy`, `check=C6-410-positive`,
  `check=C6-410-mixed`. Unit: `TestIdleDetector` (also: a 410 record of batch cronjobs
  binds no list of core pods or of group apps, and no list of another cluster).
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
- Every N-5, N-6, C1 to C5 and noisy value comes from the measurement cluster, which
  keeps the kube-apiserver defaults of etcd compaction and the watch cache (the test stops
  if an `--etcd-compaction-interval` or `--watch-cache` option is applied there); its other
  settings are the audit flags and the controller-manager QPS 200 and burst 400. The job
  prints, for both clusters, the sha256 of the config file and every applied
  kube-apiserver and kube-controller-manager option from the node's static Pod manifests
  (`PERF-CLUSTER`), and `free -m` before and after each cluster is deleted.
- Only C6 windows 3a and 3b use the reproduction cluster (`--etcd-compaction-interval=30s`,
  `--watch-cache-sizes=cronjobs.batch#0`), because Pods watches from `resourceVersion=1`
  streamed MODIFIED events and no 410 in the red runs R2 and R3 (inferred, not measured:
  served by the watch cache). Before window 3a a setup probe lists the CronJobs of
  `perf-001` at `resourceVersion=1` (`resourceVersionMatch=Exact`) until it answers 410 (at
  most 120 s; `PERF-SETUP` lines, outside every window, never counted); that shows only
  that the version is no longer readable, never that a watch ends with 410.
- On workflow_dispatch the job runs L and XL as two invocations of the test, so the
  reproduction cluster runs in the XL invocation, after XL's measurement.
- The product lists every kind in one request without `limit` today, so the N-6 page lines
  print FAIL with `limit=none` and `handled-in=T11` (P-T12-2). The product idle line is
  information until T11 (`judged-in=T11`, P-T12-1): before T11 nothing in the server
  reads periodically.
- At S the N-5 line is information; the limit applies to L.
- The L and XL lines come from one workflow_dispatch run bound to the PR head; a new head
  voids them.
