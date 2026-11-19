# graph-states

Behind the login session, every bridge of `GET /api/graph/containment?namespace=<name>` now
says whether it holds: `connected`, `waiting`, `broken`, `warning`, `unknown` (cannot be
checked) or `optional` (an optional reference whose target is missing), and a bridge that
is not connected carries one fixed text that names what it looks for or waits for. A
bridge never disappears because its target is missing or unreadable. The namespace answer
says whether the namespace is empty, does not exist or cannot be read at all
(`namespaceState`). `GET /api/graph/cluster` answers every Namespace and the cluster band
(Nodes and how many are Ready, StorageClasses with the default one marked, the PV count);
when the namespace list is refused it names the namespace of the kubeconfig context
instead of an empty cluster (packet FA2-T7a sections 1 and 3, appendix F).

## Sub-features

Each sub-feature names its input, its expected output and the CI test that drives it. The
kind test runs in the `e2e-graph` job (Go, `test/e2e-api/graph`, build tag `kind`, list
`test/e2e-api/graph/REQUIRED_TESTS_KIND`) on the release binary against a kind cluster; the
unit tests run in the `unit` job. Every checked bridge prints one line
`FA2-T7a <criterion> input=<fixture> kind=<bridge kind> from=<kind/name> to=<ns/kind/name uid|null> state=<expected>/<actual> expected-text=<...> actual-text=<...> result=<PASS|FAIL>`
and every case a count line `FA2-T7a <criterion> input=<fixture> counts connected=<n> waiting=<n> broken=<n> warning=<n> unknown=<n> optional=<n>`.

- Cluster and namespace answers. Input: list allowed; namespace list refused; band
  refused; empty, missing and locked namespace. Output: section 3.4 values (C1 to C5).
  `TestG_States/C1` to `TestG_States/C5` (job `e2e-graph`); `TestStates_Cluster`,
  `TestStates_NamespacesRefused`, `TestStates_BandRefused`, `TestStates_EmptyNamespace`,
  `TestStates_NamespaceMissingAndLocked`, `TestNamespaceGet_Ending` (job `unit`).
- Waiting and optional. Input: WFFC claim without and with a consumer; optional ConfigMap
  and Secret missing. Output: waiting then connected; optional (C6, C8).
  `TestG_States/C6-waiting`, `TestG_States/C6-connected`, `TestG_States/C8`;
  `TestStates_WaitingClaim`, `TestStates_Optional` (job `unit`).
- Broken and warning. Input: selector without Pods, named and numeric targetPort, missing
  ConfigMap, Secret, StorageClass, Service, port, HPA target. Output: section 3.2 texts
  (C7, C10 to C14). `TestG_States/C7`, `TestG_States/C10` to `TestG_States/C14`;
  `TestStates_NumericTargetPort`, `TestStates_SelectorNoPods`, `TestStates_NamedTargetPort`,
  `TestStates_ConfigMissing`, `TestStates_StorageClassMissing`, `TestStates_TrafficTargets`
  (job `unit`).
- Unknown. Input: view ServiceAccount; refused LISTs. Output: unknown, never not found (C9,
  C15). `TestG_States/C9`, `TestG_States/C15`; `TestStates_ViewRole`,
  `TestStates_RefusalNeverMissing` (job `unit`).

## How to get to it (user POV)

1. Run `intuitik` against a cluster.
2. The tab reads the cluster answer (`GET /api/graph/cluster`), then
   `GET /api/graph/containment?namespace=<name>` when a namespace is entered.
3. Each bridge's state and text show on its reference row. (Screen: T9; band and empty
   state: FA-3 U7a.)

## Driving it with http-get

The routes need a session and a cluster, so they are driven by the CI tests named under
Sub-features in the `e2e-graph` job; its step `e2e-graph tests and list check` fails
unless every name of `test/e2e-api/graph/REQUIRED_TESTS_KIND` ran and passed. This block
is the part that needs neither, as in `graph-bridges.md`: the page is served without a
session (the `verifymap run` launch has no kubeconfig).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Tested on kind on ubuntu only.
- `kube-root-ca.crt` is not counted for `empty`.
- Pod readiness is not a bridge state.
- The waiting -> connected test polls the route; the screen update time (N-3) is U11.
- An HTTPRoute port is a number, so the port-name check (I-4) is shown through an Ingress
  (T7a-C14-PORT).
