# embedded-page

The Go server embeds the frontend build output (`web/`, React, Vite) and serves it.
The page is empty; it proves the build and embed path (packet M1-T1 criterion 4).

## Sub-features

- `GET /` returns the embedded `index.html` with HTTP 200, media type `text/html`,
  and the embed marker `intuitik-embedded-page` (a `<meta name="intuitik-embed-marker">`
  element in `web/index.html`).

## How to get to it (user POV)

1. Run `intuitik` with no arguments.
2. It prints `listening on http://127.0.0.1:<port>` on stdout.
3. Open that address in a browser: the empty page loads.

## Driving it with http-get

The runner starts the linux/amd64 binary built in the same `build` job (Launch in
`verify/README.md`), then sends this request and checks the response:

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

CI step: `verify-map` in the `build` job, which runs
`verifymap run -binary <dist>/intuitik-linux-amd64 -sums <dist>/SHA256SUMS
-expect-commit <git rev-parse HEAD> -evidence <dir> embedded-page`.

## Gotchas

- Drive runs only the linux/amd64 binary, on the `ubuntu-latest` runner. The
  embedded page on the other five targets (darwin/amd64, darwin/arm64,
  linux/arm64, windows/amd64, windows/arm64) is built but not run: it stays
  unverified until the T20a RC smoke (rules A7).
- This HTTP path does not satisfy F-13. Address form, port selection and token
  belong to T2, which changes Launch and Doctor in `verify/README.md`.
- For a `pull_request` run, the `--version` commit is `github.sha`, the test merge
  commit, not the PR head; the comparison uses `git rev-parse HEAD` of the same
  checkout.
- Map-wide note on the DCO check (map N/A for DCO itself; rules A7): M1 has no
  integration evidence that a real unsigned PR fails in CI. Unverified until then:
  the base and head values of the GitHub event, the history range of the merge-ref
  checkout, real PR commit collection, the job call, and the propagation of a tool
  failure to a required-job failure and a refused merge. A positive success does not
  prove failure propagation. One real unsigned PR whose `dco` job fails is on the
  public-release check list (PRD section 4, public switch condition 7); on that PR
  the merge refusal by the required-job gate is observed and recorded.
