# local-login

The server listens only on `127.0.0.1`, on a random port (49152-65535). At start it
opens the browser at a one-time login address `http://127.0.0.1:<port>/#t=<token>`.
The tab exchanges the token once for its own session, kept only in the tab's
`sessionStorage`. Enter in the terminal opens a new login address; tabs already open
keep working. When the process ends, open tabs show the stop screen and do not
reconnect (packet M1-T2 sections 1 and 3, S1 to S19).

## Sub-features

Each sub-feature names its input, its expected output, the CI test that drives it
and the packet id it checks. API tests run in the `e2e-api` job (Go,
`test/e2e-api/server`); browser cases run in the `browser-handshake` job
(Playwright Chromium, Firefox and WebKit, `web/e2e`), with the real server and the
real `web/src/auth` module.

- Open and exchange. Input: start `intuitik`; the opener receives
  `http://127.0.0.1:<port>/#t=<token>`; the tab opens it. Output: the tab holds a
  session, the WebSocket round-trips. `browser-handshake/ws-roundtrip`,
  `TestS3_TokenExchangeOnce` (S3, S6, S15).
- Reload keeps the session. Input: reload the logged-in tab. Output: authenticated
  API 200, no `#t=` in the address bar, CSRF POST 200.
  `browser-handshake/reload-keeps-session` (S19).
- A new tab typed by hand. Input: open `http://127.0.0.1:<port>/` in a new tab.
  Output: the B-NS screen, API 401. `browser-handshake/new-tab-no-session` (B-NS).
- A used or expired address. Input: open a used address, or one older than 60 s.
  Output: the B-RF screen; the 403 body is B-RF. `browser-handshake/login-refused-screen`,
  `TestText_BRF_RefusalBody` (B-RF, S17).
- Enter opens tab B, tab A keeps working. Input: Enter in the terminal. Output: S-R
  on stderr, tab B logs in, tab A keeps API, CSRF POST and its WebSocket.
  `browser-handshake/enter-keeps-tab-a`, `TestText_SR_Enter`,
  `TestS19_ReconnectKeepsExistingTab` (S-R, S19).
- Stop shows B-ST. Input: interrupt or kill the process. Output: the B-ST screen, no
  reload, no request to a later occupant of the port. `browser-handshake/stop-screen`,
  `TestS8_StopNotice` (B-ST, S8).
- A port in use is skipped. Input: the first port candidate taken. Output: the
  panel starts on the next one. `TestS9_PortInUse` (S9).
- A non-terminal stdin. Input: start with stdin not a terminal. Output: S-E2 on
  stdout. `TestText_SE2_NotTerminal` (S-E2).

## How to get to it (user POV)

1. Run `intuitik` in a terminal.
2. It prints `listening on http://127.0.0.1:<port>` and
   `Press Enter to open a new login address in your browser.`
3. The browser opens the login address and the panel loads in that tab.
4. For another tab, press Enter: a new login address opens in the browser.

## Driving it with http-get

The login and session paths are driven by the CI tests named under Sub-features;
their output lines name the S or B id they check. This block is the part that
needs no login: the page itself is served to a tab without a session (S2).

```drive
path: /
status: 200
content-type: text/html
body-contains: intuitik-embedded-page
```

CI steps: `e2e-api tests and list check` in the `e2e-api` job and
`browser-handshake tests and list check` in the `browser-handshake` job; each
fails unless every name of its required list (`test/e2e-api/server/REQUIRED_TESTS_LINUX`,
`web/e2e/REQUIRED_TESTS`) ran and passed.

## Gotchas

- The login and WebSocket flow is checked only with Playwright Chromium, Firefox
  and WebKit on ubuntu. Not checked: Safari on macOS, Chrome and Edge on Windows,
  Chrome and Firefox on macOS, Firefox on Windows.
- Whether the operating system's way of opening a URL keeps the `#t=` part is not
  checked. A recording stand-in for the opener proves only what intuitik passed to
  it.
- The token in the real browser process command line is not checked on any OS;
  only intuitik's own opener argument is checked (`TestS18_ArgvExposure`, linux).
- Proxy behavior is checked only for the engines and settings listed in
  `browser-handshake/proxy-loopback-bypass`; other engines, PAC files and OS proxy
  settings are not checked. The recorded rows of that case (arrivals per
  configuration, engine version and the launch arguments) are in the job log of
  each run and in the M1-T2 PR body.
- `sessionStorage` isolation per origin (including the port) is relied on and not
  measured. Service Worker pre-registration on many ports is possible by the
  Chromium and Gecko documentation model, not measured, and not blocked (hoan
  decision 6949c427).
- The S16 Windows socket test runs on every PR; its run on release tags belongs to
  T20a and is unverified until then.
- `verifymap run` in the `build` job drives only `embedded-page`; it knows the
  drive id `http-get` only, so the login paths above are driven by the e2e jobs, not
  by `verifymap`.
- The product writes no access log; the secret checks of stderr cover all the
  terminal output the product writes.
