# graph-bridges

Behind the login session, `GET /api/graph/containment?namespace=<name>` also answers the
declared references between objects of that namespace as bridges, each with the settings
that made it: Ingress -> Service (host, path, port), Service -> Pod (selector, port ->
targetPort), Pod -> PVC (claim, mount paths, access modes), PVC -> PV (StorageClass,
capacity), Pod -> ConfigMap and Pod -> Secret (key names and env or volume paths, never a
value), HPA -> its target workload (min, max) and HTTPRoute -> Service (hostnames, path,
port). Services, PVCs, Ingresses and HPAs are nodes outside every workload box. An
HTTPRoute bridge carries a text when its Service is not found, cannot be checked or is in
another namespace; routes served only at a version intuitik does not read are nodes with
`Unsupported version <version>` and no bridge (packet FA2-T6 sections 1 and 3, appendix E).

## Sub-features

Each sub-feature names its input, its expected output and the CI test that drives it. The
kind test runs in the `e2e-graph` job (Go, `test/e2e-api/graph`, build tag `kind`, list
`test/e2e-api/graph/REQUIRED_TESTS_KIND`) on the release binary against a kind cluster;
the unit tests run in the `unit` job. Every checked bridge prints one line
`FA2-T6 <criterion> input=<fixture> kind=<bridge kind> from=<kind/name> to=<ns/kind/name uid|null> expected=<settings joined by " | "> actual=<same> expected-to=<group>/<uid|null> actual-to=<same> [expected-text=<...> actual-text=<...>] result=<PASS|FAIL>`,
with U+2192 and U+2190 printed as their escapes, and every graph call of the kind test
prints its audit window (`FA2-T5 G7`).

- Traffic bridges. Input: Ingress with host, Exact path and default backend; Service with
  selector and two ports; HTTPRoute with hostnames, two matches, and one without hostnames
  or matches. Output: settings as section 3.1 (C1, C2, C6). `TestG_Bridges/C1`,
  `TestG_Bridges/C2`, `TestG_Bridges/C6` (job `e2e-graph`); `TestBridges_Ingress`,
  `TestBridges_Service`, `TestBridges_HTTPRoute` (job `unit`).
- Storage and config bridges. Input: StatefulSet with a claim template, read-only mount;
  Pod with env, envFrom, volume items and projected Secret; HPA. Output: settings as
  section 3.1, no value (C3, C4, C5, C14). `TestG_Bridges/C3`, `TestG_Bridges/C4`,
  `TestG_Bridges/C5`, `TestG_Bridges/C14`; `TestBridges_Storage`, `TestBridges_Config`,
  `TestBridges_ConfigOrder`, `TestBridges_OptionalFlag`, `TestBridges_EphemeralClaim`,
  `TestBridges_HPA`, `TestBridges_NoValueNoRead` (job `unit`).
- HTTPRoute failures. Input: no Gateway API; list refused; Service missing; Services not
  readable; other namespace; only v1alpha2. Output: BT-1 to BT-6 (C8 to C13).
  `TestG_Bridges/C8`, `TestG_Bridges/C9`, `TestG_Bridges/C10`, `TestG_Bridges/C11`,
  `TestG_Bridges/C12`, `TestG_Bridges/C12-hpa-group`, `TestG_Bridges/C13`;
  `TestBridges_NoGatewayAPI`, `TestBridges_RouteListRefused`,
  `TestBridges_RouteTargetMissing`, `TestBridges_RouteTargetNotReadable`,
  `TestBridges_RouteTargetOwnerFetched`, `TestBridges_GroupExact`,
  `TestBridges_UnsupportedVersion` (job `unit`).
- Layout facts. Input: Service and PVC owned by a StatefulSet; LIST order shuffled.
  Output: outside boxes; same bytes (C7, C15). `TestG_Bridges/C7`;
  `TestBridges_OutsideBoxes`, `TestBridges_Stable` (job `unit`).

## How to get to it (user POV)

1. Run `intuitik` against a cluster.
2. In the tab, enter a namespace.
3. The tab reads `GET /api/graph/containment?namespace=<name>`; each bridge leaves its
   object's reference row with its settings. (Screen: T9.)

## Driving it with http-get

The route needs a session and a cluster, so it is driven by the CI tests named under
Sub-features in the `e2e-graph` job; its step `e2e-graph tests and list check` fails
unless every name of `test/e2e-api/graph/REQUIRED_TESTS_KIND` ran and passed. This block
is the part that needs neither, as in `graph-containment.md`: the page is served without a
session (the `verifymap run` launch has no kubeconfig).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Tested on kind on ubuntu only.
- ConfigMap, Secret and PV targets carry `to.uid` null until T7a reads them; that is not
  evidence that the target is missing.
- The HTTPRoute success cases run on Gateway API v1.7.0 (bundle checked by its sha256 in
  the test); other bundle versions are not tested.
- Bridges to several Pods are not grouped in the API (T9).
