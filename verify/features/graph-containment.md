# graph-containment

Behind the login session, `GET /api/graph/containment?namespace=<name>` answers, for one
namespace, every Pod, ReplicaSet, Deployment, StatefulSet, DaemonSet, Job and CronJob and
every namespaced custom resource found through discovery, and for each one the object it
sits inside, from its ownerReferences, or why its owner is not used: the owner does not
exist, intuitik may not read it, it is cluster-scoped, or the references form a loop. A
lookup that fails for any other reason fails the whole answer with its own text. While the
cluster is not connected the route answers 503 with the connection state (packet FA2-T5
sections 1 and 3, appendix D).

## Sub-features

Each sub-feature names its input, its expected output and the CI test that drives it. The
kind tests run in the `e2e-graph` job (Go, `test/e2e-api/graph`, build tag `kind`, list
`test/e2e-api/graph/REQUIRED_TESTS_KIND`) on the release binary against a kind cluster
whose garbage collector controller is off; the unit tests run in the `unit` job. Every
checked object prints one line
`FA2-T5 <criterion> input=<fixture> expected=<kind/name parent state> actual=<same> result=<PASS|FAIL>`,
and every graph call of the kind tests prints its audit window (`FA2-T5 G7`).

- Owner boxes. Input: Deployment with two revisions; StatefulSet; DaemonSet; CronJob with
  a Job; a test custom resource owning a Deployment and a Pod; a bare Pod and a Job
  without owner. Output: each object's `parent` and `owner.state` as C1 to C5.
  `TestG_Containment` (job `e2e-graph`), `TestContainment_*` (job `unit`).
- Owner missing. Input: a reference to a Deployment that does not exist; an owner
  recreated with another UID; an owner kind that the cluster does not serve. Output:
  `owner-missing`, `Owner <kind>/<name> not found`, the child at namespace level with its
  own children kept. `TestG_OwnerMissing`.
- Owner not readable. Input: a ServiceAccount without read on ReplicaSets (owner existing
  and not existing); without read on Deployments. Output: `owner-forbidden`,
  `Owner not readable U+2014 no permission`, never `not found`. `TestG_OwnerForbidden`.
- Cluster-scoped owner. Input: kube-system static Pods owned by a Node. Output:
  `owner-cluster-scoped`, `Owner Node/<name> is cluster-scoped`; `owner-forbidden` for a
  ServiceAccount that cannot read Nodes. `TestG_ClusterScopedOwner`.
- Secret owner. Input: a Pod owned by a core Secret, one owned by a Secret that does not
  exist, the first again for a ServiceAccount that cannot get Secrets, a Pod owned by a
  custom kind named Secret. Output: contained, `owner-missing`, `owner-forbidden`, the
  custom kind resolved by its own apiVersion; no Secret value in any response or output.
  `TestG_SecretOwner`, `TestContainment_SecretOwner` (job `unit`).
- Custom resources and versions. Input: a ServiceAccount that cannot list CRDs; a failed
  group version document; a group whose kinds are served at different versions. Output:
  the custom resources shown with nothing refused; one `refused` entry with the status
  received; each kind read at its own version. `TestG_CustomWithoutCRDList`,
  `TestG_VersionPerResource`, `TestContainment_GroupDocuments`,
  `TestContainment_VersionPerResource` (job `unit`).
- Lookup errors, several owners, loops. Input: owner read answers 500, 503, no answer;
  two owners with and without a controller; a loop; a 20-owner acyclic chain. Output:
  GA-3 error and no node; one place per object; `owner-loop` on the loop; the long chain
  contained to its top. `TestContainment_LookupError`, `TestContainment_SeveralOwners`,
  `TestContainment_Loop`, `TestContainment_LongChain` (job `unit`),
  `TestGraphRoute_LookupError` (job `unit`, the route against a fake API).
- Not connected, no session, list refused. Output: 503 with the K-text; 401 without
  cluster data; GA-4. `TestG_RouteGate`, `TestG_ListRefused`.
- Graph read with a credential program. Input: a context whose credential program
  refreshes (expiry, 401, rejected again, failed run, switch during refresh). Output: as
  M1-T3b, through this route; one client per cluster operation, so a call sends a
  rejected credential at most once and nothing after a stop or a switch.
  `TestK_ExecRefresh`, `TestK_ExecRefreshBeforeW1`, `TestK_ExecRefreshAfterW1`,
  `TestK_ExecRefreshTimeout`, `TestK_ExecSwitchDuringRefresh`,
  `TestK_ExecCredentialFreeOutput` (job `e2e-kind`, list
  `test/e2e-api/connect/REQUIRED_TESTS_KIND`); `TestGraphCall_*` (job `unit`).

## How to get to it (user POV)

1. Run `intuitik` against a cluster.
2. In the tab, enter a namespace.
3. The tab reads `GET /api/graph/containment?namespace=<name>`; each object is drawn
   inside its owner, or directly under the namespace with the owner text. (Screen: T9.)

## Driving it with http-get

The route needs a session and a cluster, so it is driven by the CI tests named under
Sub-features in the `e2e-graph` job; its step `e2e-graph tests and list check` fails
unless every name of `test/e2e-api/graph/REQUIRED_TESTS_KIND` ran and passed. This block
is the part that needs neither, as in `cluster-connect.md`: the page is served without a
session (the `verifymap run` launch has no kubeconfig).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Tested on kind on ubuntu only.
- The answer is a snapshot of one request; it changes only on a new request until T11.
- Custom resources are found through discovery and the built-in group list B; a failed
  group document shows in `refused` with the status actually received, never as absence.
- The owner-missing kind cases run on a kind cluster whose garbage collector controller
  is off (`test/e2e-api/graph/kind-config.yaml`); each test reads its fixture back before
  the request and fails if it is gone.
