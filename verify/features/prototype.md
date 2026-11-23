# prototype

The clickable relation map prototype (packet FA2-T8): the screens of PRD section 9 and
UI.md section 8 in fixed appendix A data (`web/prototype/data`, the graph answer shape of
the merged server), every navigation path of the packet's journey table, and the renderer
measurement on the T12 L graph file `test/e2e/prototype/testdata/l-graph.tar.gz`
(sha256 6a1bd4382805a3a66e78ac8d3a101bbfdf4943eca386b47a1cd84db07d793634). PMC publishes
the job's evidence pack as a private Artifact for hoan check 2; the prototype is not part
of the product build.

## Sub-features

The Drive of every sub-feature is the `prototype` job: it checks the three fixed tables
against `web/prototype/e2e/fixed.sha256`, builds the product and fails on a prototype
marker in it, builds the prototype (`web/prototype/vite.config.js`), runs the Playwright
cases of `web/prototype/e2e/REQUIRED_TESTS` (project chromium, retries 0) against
`vite preview` on port 4317, packs the output and uploads it under the guard of the job.
The Go test `test/e2e/prototype/shape_test.go` (job `unit`) and the Vitest test
`web/prototype/measure/derive500.test.js` (job `build`) check the data.

- fixed tables. Input: `screens.json`, `controls.json`, `canaries.json` and
  `fixed.sha256`. Expected: each sha256 equal to its constant and the list's own sha256
  equal to the delivery's value; `fixed tables: PASS`.
- C1 screen list. Input: the 65 screen ids and 10 journey paths of `screens.json`.
  Expected: one line per id with its capture `<id>.png`, its DOM `<id>.dom.html` and the
  steps that reached it, one line per followed path, and
  `screens expected=65 captured=65 paths=10/10 result=PASS`; a missing screen prints
  `FAIL C1 missing=<id> path=<journeys>`.
- C2 strings. Input: `web/prototype/e2e/expected-strings.json` (UI.md 4.4, 6, 7, 8 strings,
  the FA2-T7b Pod texts of the data, the M1-T2 browser lines). Expected: each equal to its
  value in `web/prototype/strings.js`, found in the DOM of its screen, and written as a
  literal in no other prototype source; `C2 ... result=PASS`.
- C3 controls. Input: every element with role button, link, menuitem, checkbox, textbox,
  combobox, tab or tabindex >= 0 on every screen, and the rows of `controls.json`.
  Expected: each element's `data-control` key is a row and no row is of class cluster
  change; each row's control run once on a screen that shows it, with requests outside
  the prototype build = 0; `C3 ... unlisted=0 cluster-change=0 ... result=PASS`.
- C4 secret canary. Input: `canaries.json`. Expected: YAML, Copy, send preview and Review
  run once for each readable Secret (`C4 executions set=2 yaml=2 copy=2 preview=2
  review=2`); the unreadable Secret never requested, shown or built; `C4 canary-hits=0`.
- C5 renderer measurement, input mismatch refused, comparison scenes. Input: the L file
  by sha256. Expected: the `PROTO-MEASURE` lines of packet 6.4 for R1 (@xyflow/react with
  @dagrejs/dagre) and R2 (sigma with graphology), the derived 500-Pod input line, the
  threshold proposal, `input sha256 mismatch expected=<a> got=<b>` with exit 1 for a
  changed file, and one `PROTO-SCENE` line per candidate and scene S-A to S-E. The numbers
  are information; a missing line, an ERROR or a measured changed file fails the case.
- C6 keyboard path and grayscale. Expected: Cluster, Namespace, Workload, Details, Ask why
  and Send reached by keyboard with a visible focus outline, captured per step; the six
  link states and four node states distinct in grayscale captures.

## How to get to it (user POV)

1. Open the pull request's CI run of the `prototype` job.
2. Read the step `prototype tests and list check` and the `PROTO-PACK` line of `Evidence
   pack`.
3. hoan sees the pack as a private Artifact (PMC); the prototype itself is opened from
   the pack's `dist`, never from a local checkout.

## Driving it with http-get

The prototype is served by `vite preview` inside the `prototype` job, not by the
binary, and its cases run there. This block is the part the `verifymap run` launch can
drive: the product page, which must hold no prototype marker (the job's marker check).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

## Gotchas

- Long-task and timing numbers vary on shared runners; they are information for the
  renderer choice (FA-2 227), never a verdict.
- R2 needs WebGL2; the Playwright project starts Chromium with its software renderer.
- The strip at the top of the prototype is prototype-only and is not part of the A-F
  verdicts.
