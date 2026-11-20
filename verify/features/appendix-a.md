# appendix-a

The six Appendix A faults (A-1 to A-6) and the healthy baseline A-0 are built on a kind
cluster and judged from the graph API by a script, which prints one line per expectation,
one per scenario, one per stage total, the A-0 line and the total line; no person counts
anything. Every core Pod node of `GET /api/graph/containment?namespace=<name>` now carries
`nodeState`: `healthy`, `progressing`, `failing` or `unknown`, with one fixed text when it
is not healthy, for example `CrashLoopBackOff \u00b7 3 restarts` (`\u00b7` is U+00B7 with one
space on each side; packet FA2-T7b sections 1 and 3, appendix G).

## Sub-features

The Drive of every sub-feature is the `appendix-a` job: Go, `test/e2e-api/appendix-a`,
build tag `kind`, list `test/e2e-api/appendix-a/REQUIRED_TESTS_KIND`, test
`TestA_AppendixA`, on the release binary against the job's own kind cluster (kind's
default configuration). The test applies `scenarios/appendix-a/*.yaml` in three stages
(faults, negative, baseline) and calls the judge `tools/hk2judge` once over the three;
the job log holds every `HK2` line as printed. A line per expectation reads
`HK2 stage=<faults|negative> <id> check=<name> expected=<value> actual=<value> result=PASS|FAIL`.

- A-1 CrashLoopBackOff. Input: Deployment `crash` in `hk2-a1`, command `exit 1`
  (`a1.yaml`). Expected: the Pod node `failing`, and its text equal to the form of the
  current state of container `crash` at the kubectl read just before or just after the
  call (same Pod UID): waiting `CrashLoopBackOff` gives `CrashLoopBackOff \u00b7 <n> restarts`,
  terminated gives `<reason> \u00b7 <n> restarts` (`exit <exitCode> \u00b7 <n> restarts` when the
  reason is empty), `<n>` that read's restartCount, n >= 1, `restart` for 1 (rules N-1 and
  N-1b). `lastState` is never a candidate. The log prints every A-1 poll and read (time,
  state, reason, exitCode, restartCount) and each attempt. Line:
  `HK2 stage=faults A-1 result=PASS` (C2).
- A-2 image pull. Input: Deployment `pull` in `hk2-a2`, image
  `registry.k8s.io/pause:intuitik-a2-missing` (`a2.yaml`). Expected: the Pod node
  `failing`, text `ImagePullBackOff \u00b7 registry.k8s.io/pause:intuitik-a2-missing` or
  `ErrImagePull \u00b7 registry.k8s.io/pause:intuitik-a2-missing`. Line:
  `HK2 stage=faults A-2 result=PASS` (C3).
- A-3 selector. Input: Deployment `web` with template label `app=web-v2`, Service `web`
  selecting `app=web` in `hk2-a3` (`a3.yaml`). Expected: one `service-pod` bridge from
  Service `web` to a Pod with an empty name, `broken`,
  `selector app=web matches 0 Pods`. Line: `HK2 stage=faults A-3 result=PASS` (C4).
- A-4 named port. Input: Service `web` `targetPort: http`, container port named `web` in
  `hk2-a4` (`a4.yaml`). Expected: the `service-pod` bridge to the Pod `broken`,
  `targetPort "http": no container port with this name`. Line:
  `HK2 stage=faults A-4 result=PASS` (C5).
- A-5 missing ConfigMap. Input: Deployment `app` with `envFrom` ConfigMap `app-config`,
  which is absent, in `hk2-a5` (`a5.yaml`). Expected: the `pod-configmap` bridge `broken`,
  `ConfigMap app-config not found`, and the Pod node `failing`,
  `CreateContainerConfigError`. Line: `HK2 stage=faults A-5 result=PASS` (C6).
- A-6 missing StorageClass. Input: PVC `data` with `storageClassName: missing-class` and
  Deployment `db` mounting it in `hk2-a6` (`a6.yaml`). Expected: `pvc-storageclass` and
  `pvc-pv` bridges `broken`, `StorageClass "missing-class" not found (claim cannot bind)`;
  `pod-pvc` `connected`. Line: `HK2 stage=faults A-6 result=PASS` (C7).
- Total. Input: the six faults. Expected: `HK2 stage=faults total=6/6` and the final line
  `HK2 total=6/6 negative=PASS a0=PASS verdict=PASS` with the judge's exit 0 (C8).
- Negative flip. Input: the faults with A-3 switched to `a3-healthy.yaml` (template label
  `app=web`). Expected: `HK2 stage=negative A-3 result=FAIL`, the other five PASS,
  `HK2 stage=negative total=5/6 expected=5/6 result=PASS` (C10).
- A-0 baseline. Input: every scenario in its `-healthy.yaml` version, the cluster's own
  components and `a0.yaml` (PVC `idle` on the default StorageClass, no Pod). Expected:
  broken 0 over every namespace the cluster answer lists, warning 0 and no failing Pod in
  `hk2-a1` to `hk2-a6`, no refused entry, and the `idle` bridge `waiting`,
  `idle waits for a consumer Pod`. Line:
  `HK2 stage=baseline A-0 broken=0 warning=0 failing=0 waiting=<n> optional=<n> unknown=<n> result=PASS` (C9).

`map N/A: C1: Pod state rules over constructed objects, unit only; the user path is C2, C3, C6.`
`map N/A: C11, C12, C13: judge behaviour on recorded answers, no cluster state a user sets up.`
`map N/A: C14: unchanged earlier checks, already mapped in their feature files.`
`map N/A: C15: checks the map file itself, no user action.`

## How to get to it (user POV)

1. Run `intuitik` against a cluster with one of the faults.
2. Open the namespace (`GET /api/graph/containment?namespace=<name>`).
3. The Pod node or the bridge shows the FA2-T7b 3.1 or FA2-T7a state and text. (Screen:
   T9.)

## Driving it with http-get

The routes need a session and a cluster, so the scenarios are driven by
`TestA_AppendixA` in the `appendix-a` job; its step `appendix-a tests and list check`
fails unless every name of `test/e2e-api/appendix-a/REQUIRED_TESTS_KIND` ran and passed,
and the test fails unless the judge exits 0 with the final line
`HK2 total=6/6 negative=PASS a0=PASS verdict=PASS`. This block is the part that needs
neither: the page is served without a session (the `verifymap run` launch has no
kubeconfig).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Tested on kind on ubuntu only.
- A-1 needs at least one restart before CrashLoopBackOff or N-1b shows; between restarts
  the kubelet may report the container `terminated` (a hypothesis the A-1 prints record).
  The faults stage waits at most 180 s for a waiting `CrashLoopBackOff` or terminated
  container with restartCount >= 1. An attempt (kubectl read, API call, kubectl read) with
  no such read, or whose API answer is the N-9 text of a running read, is printed and
  repeated after 500 ms, at most 20 attempts within the stage clock; an attempt with such
  a read is never repeated. A wait that ends without its condition is printed and the
  scenario then FAILs on the API answer.
- A-2 alternates between `ErrImagePull` and `ImagePullBackOff`; both pass.
- What A-2 proves is that the panel shows the waiting reason and the exact image of a Pod
  that cannot pull. It does not prove why the pull fails: a registry outage gives the same
  reasons and the same text, and the judge cannot tell the two apart. That the tag does
  not exist is a fact of the scenario input (manifest GET 404 measured by Devgru, packet
  6.5), not of the judge.
