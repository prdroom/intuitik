// browser-handshake cases (packet M1-T2 S15, S8, S19 and the browser texts of
// section 3.3). Every case opens the real server's page, which runs the real
// web/src/auth module; the test reads the tab's own sessionStorage to make further
// requests with the tab's credentials.
import { chromium, expect, firefox, test, webkit } from '@playwright/test';
import fs from 'node:fs';
import {
  TEXTS,
  countSecret,
  recordingProxy,
  recordingServer,
  sleep,
  squatter,
  startPanel,
  until,
} from './harness.js';

const panels = [];
async function panelFor(opts) {
  const p = await startPanel(opts);
  panels.push(p);
  return p;
}

test.afterEach(async () => {
  while (panels.length) {
    await panels.pop().close();
  }
});

// CSP violation events of the page, collected from the first script on.
async function watchCsp(page) {
  await page.addInitScript(() => {
    window.__csp = [];
    document.addEventListener('securitypolicyviolation', (e) => {
      window.__csp.push(`${e.violatedDirective} ${e.blockedURI}`);
    });
  });
}

// login opens the login address; on failure it prints what the tab showed, its
// console and the panel's responses, so that the reason is in the job log.
async function login(page, panel, token) {
  const seen = [];
  const onConsole = (m) => seen.push(`console ${m.type()}: ${m.text()}`);
  const onResponse = (r) => seen.push(`response ${r.status()} ${r.request().method()} ${r.url().replace(token, '<T>')}`);
  const onFailed = (r) => seen.push(`request failed ${r.method()} ${r.url().replace(token, '<T>')}: ${r.failure()?.errorText}`);
  page.on('console', onConsole);
  page.on('response', onResponse);
  page.on('requestfailed', onFailed);
  const handshakes = [];
  if (page.context().browser()?.browserType().name() === 'chromium') {
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Network.enable');
    cdp.on('Network.webSocketWillSendHandshakeRequest', (e) => {
      const h = Object.fromEntries(Object.entries(e.request.headers).map(([k, v]) => [k.toLowerCase(), v]));
      handshakes.push(
        `WebSocket handshake request header names: ${Object.keys(h).sort().join(', ')}; ` +
          `Origin ${h.origin ?? '(absent)'}; Sec-Fetch-Site ${h['sec-fetch-site'] ?? '(absent)'}`,
      );
    });
    cdp.on('Network.webSocketHandshakeResponseReceived', (e) => {
      handshakes.push(`WebSocket handshake response status ${e.response.status}`);
    });
  }
  try {
    await page.goto(`${panel.origin}/#t=${token}`);
    await page.waitForSelector('#intuitik-app', { state: 'attached', timeout: 15_000 });
    await sleep(300);
    for (const line of handshakes) console.log(`S6 chromium ${line}`);
  } catch (e) {
    await sleep(300);
    for (const line of handshakes) console.log(`S6 chromium ${line}`);
    const body = await page.evaluate(() => document.body?.innerHTML ?? '').catch((err) => `unreadable: ${err}`);
    console.log(`login failed: ${e.message.split('\n')[0]}\n  url ${page.url().replace(token, '<T>')}\n  body ${body}\n  ${seen.join('\n  ')}`);
    throw e;
  } finally {
    page.off('console', onConsole);
    page.off('response', onResponse);
    page.off('requestfailed', onFailed);
  }
}

async function screenLines(page) {
  await page.waitForSelector('#intuitik-auth-screen', { timeout: 15_000 });
  return page.locator('#intuitik-auth-screen p').allTextContents();
}

// tabCalls uses the tab's session: GET /api/session (C again), then POST /api/ping.
async function tabCalls(page) {
  return page.evaluate(async () => {
    const s = window.sessionStorage.getItem('intuitik.session');
    const get = await fetch('/api/session', { headers: { Authorization: `Bearer ${s}` } });
    const csrf = get.ok ? (await get.json()).csrf : '';
    const post = await fetch('/api/ping', {
      method: 'POST',
      headers: { Authorization: `Bearer ${s}`, 'X-CSRF-Token': csrf },
    });
    return { get: get.status, post: post.status, csrf };
  });
}

// wsTry opens a WebSocket with the given subprotocols and reports whether it opened
// and whether one message came back.
async function wsTry(page, protocols) {
  return page.evaluate(
    (protocols) =>
      new Promise((resolve) => {
        let opened = false;
        const ws = protocols ? new WebSocket(`ws://${location.host}/api/ws`, protocols) : new WebSocket(`ws://${location.host}/api/ws`);
        const done = (echo) => {
          resolve({ opened, echo, protocol: ws.protocol, extensions: ws.extensions });
          ws.close();
        };
        ws.onopen = () => {
          opened = true;
          ws.send('round-trip');
        };
        ws.onmessage = (e) => done(e.data);
        ws.onclose = () => done(null);
        setTimeout(() => done(null), 5000);
      }),
    protocols,
  );
}

async function credentials(page) {
  return page.evaluate(async () => {
    const s = window.sessionStorage.getItem('intuitik.session');
    const r = await fetch('/api/session', { headers: { Authorization: `Bearer ${s}` } });
    return { s, c: (await r.json()).csrf };
  });
}

const w1 = ({ s, c }) => ['intuitik.v1', `csrf.${c}`, `sess.${s}`];

// browserCommandLines reads, from /proc, the command line of the browser processes
// that name this proxy port (S15: the arguments the browser actually received).
function browserCommandLines(proxyPort) {
  const found = [];
  for (const pid of fs.readdirSync('/proc').filter((d) => /^\d+$/.test(d))) {
    let cmd;
    try {
      cmd = fs.readFileSync(`/proc/${pid}/cmdline`, 'utf8').split('\0').join(' ').trim();
    } catch {
      continue;
    }
    if (cmd.includes(`127.0.0.1:${proxyPort}`) && !cmd.includes('--type=')) {
      found.push(cmd);
    }
  }
  return found.length ? found.join(' || ') : 'no process names the proxy port (the engine takes the proxy from its settings)';
}

test.describe('browser-handshake', () => {
  test('ws-roundtrip', async ({ page }) => {
    const panel = await panelFor();
    await watchCsp(page);
    await login(page, panel, await panel.token());
    const cred = await credentials(page);
    const good = await wsTry(page, w1(cred));
    console.log(`S15 ws-roundtrip valid: opened ${good.opened}, echo ${good.echo}, protocol ${good.protocol}, extensions "${good.extensions}"`);
    expect(good).toMatchObject({ opened: true, echo: 'round-trip', protocol: 'intuitik.v1', extensions: '' });
    const bad = {
      'no subprotocol': null,
      'wrong sess.': ['intuitik.v1', `csrf.${cred.c}`, `sess.${'A'.repeat(43)}`],
      'wrong csrf.': ['intuitik.v1', `csrf.${'A'.repeat(43)}`, `sess.${cred.s}`],
    };
    let upgrades = 0;
    for (const [name, protocols] of Object.entries(bad)) {
      const r = await wsTry(page, protocols);
      console.log(`S15 ws-roundtrip ${name}: opened ${r.opened}`);
      if (r.opened) upgrades += 1;
    }
    console.log(`S15 ws-roundtrip refused cases: 101 responses ${upgrades}, want 0`);
    expect(upgrades).toBe(0);
    const csp = await page.evaluate(() => window.__csp);
    console.log(`S15 ws-roundtrip CSP violations on the normal path: ${csp.length}`);
    expect(csp).toEqual([]);
  });

  test('csp-connect-blocked', async ({ page }) => {
    const panel = await panelFor();
    const rec = await recordingServer();
    await watchCsp(page);
    await login(page, panel, await panel.token());
    await page.evaluate((q) => {
      fetch(`http://127.0.0.1:${q}/fetch`).catch(() => {});
      const img = new Image();
      img.src = `http://127.0.0.1:${q}/img`;
      try {
        new WebSocket(`ws://127.0.0.1:${q}/ws`);
      } catch {
        // blocked
      }
    }, rec.port);
    await sleep(1500);
    const csp = await page.evaluate(() => window.__csp);
    console.log(`S15 csp-connect-blocked: arrivals ${rec.arrivals.length} (want 0), violations ${csp.length} (want >= 1): ${csp.join(' | ')}`);
    expect(rec.arrivals).toEqual([]);
    expect(csp.length).toBeGreaterThanOrEqual(1);
    await page.goto(`http://127.0.0.1:${rec.port}/navigation`);
    console.log(`S15 csp-connect-blocked positive control: direct navigation arrivals ${rec.arrivals.length} (want 1)`);
    expect(rec.arrivals).toEqual(['GET /navigation']);
    await rec.close();
  });

  test('csp-form-blocked', async ({ page }) => {
    const panel = await panelFor();
    const rec = await recordingServer();
    await watchCsp(page);
    await login(page, panel, await panel.token());
    const submit = await page
      .evaluate((q) => {
        const form = document.createElement('form');
        form.method = 'POST';
        form.action = `http://127.0.0.1:${q}/form`;
        const input = document.createElement('input');
        input.name = 'x';
        input.value = '1';
        form.appendChild(input);
        document.body.appendChild(form);
        try {
          form.submit();
          return 'submit returned';
        } catch (e) {
          return `submit threw ${e.name}`;
        }
      }, rec.port)
      .catch((e) => `evaluate rejected: ${e.message}`);
    console.log(`S15 csp-form-blocked: ${submit}`);
    await sleep(1500);
    const csp = await page.evaluate(() => window.__csp).catch(() => ['page navigated']);
    console.log(`S15 csp-form-blocked: arrivals ${rec.arrivals.length} (want 0), violations ${csp.length} (want >= 1): ${csp.join(' | ')}`);
    expect(rec.arrivals).toEqual([]);
    expect(csp.length).toBeGreaterThanOrEqual(1);
    await rec.close();
  });

  test('reload-keeps-session', async ({ page }) => {
    const panel = await panelFor();
    let firstLoad = null;
    page.on('response', (r) => {
      if (r.url() === `${panel.origin}/` && firstLoad === null) firstLoad = r.headers()['clear-site-data'] ?? '';
    });
    await login(page, panel, await panel.token());
    await page.reload();
    await page.waitForSelector('#intuitik-app', { state: 'attached', timeout: 15_000 });
    const calls = await tabCalls(page);
    console.log(`S15 reload-keeps-session: first load Clear-Site-Data ${firstLoad}; after reload URL ${page.url()}, GET /api/session ${calls.get}, CSRF POST ${calls.post}`);
    expect(page.url()).not.toContain('#t=');
    expect(firstLoad).toBe('"cache"');
    expect(calls).toMatchObject({ get: 200, post: 200 });
  });

  test('new-tab-no-session', async ({ page, context }) => {
    const panel = await panelFor();
    await login(page, panel, await panel.token());
    const tab = await context.newPage();
    await tab.goto(`${panel.origin}/`);
    const lines = await screenLines(tab);
    const api = await tab.evaluate(async () => (await fetch('/api/session')).status);
    const stored = await tab.evaluate(() => window.sessionStorage.length);
    console.log(`S15 new-tab-no-session: B-NS expected ${JSON.stringify(TEXTS.noSession)}`);
    console.log(`S15 new-tab-no-session: B-NS actual   ${JSON.stringify(lines)}; API ${api}; sessionStorage keys ${stored}`);
    expect(lines).toEqual(TEXTS.noSession);
    expect(api).toBe(401);
    expect(stored).toBe(0);
  });

  test('enter-keeps-tab-a', async ({ page, context }) => {
    const panel = await panelFor();
    await login(page, panel, await panel.token());
    const cred = await credentials(page);
    await page.evaluate(
      (protocols) =>
        new Promise((resolve) => {
          window.__wsA = new WebSocket(`ws://${location.host}/api/ws`, protocols);
          window.__wsA.onopen = resolve;
        }),
      w1(cred),
    );
    const t2 = await panel.enter();
    const tabB = await context.newPage();
    await login(tabB, panel, t2);
    const aStill = await page.evaluate(
      () =>
        new Promise((resolve) => {
          if (window.__wsA.readyState !== WebSocket.OPEN) return resolve(false);
          window.__wsA.onmessage = (e) => resolve(e.data === 'still-open');
          window.__wsA.send('still-open');
          setTimeout(() => resolve(false), 5000);
        }),
    );
    const a = await tabCalls(page);
    const b = await tabCalls(tabB);
    const aWs = await wsTry(page, w1(await credentials(page)));
    const bWs = await wsTry(tabB, w1(await credentials(tabB)));
    const aScreen = await page.locator('#intuitik-app').count();
    console.log(`S19 enter-keeps-tab-a: tab A API ${a.get} POST ${a.post} new WS ${aWs.echo} existing WS round trip ${aStill} app ${aScreen}; tab B API ${b.get} POST ${b.post} WS ${bWs.echo}; C same ${a.csrf === b.csrf}`);
    expect(a).toMatchObject({ get: 200, post: 200 });
    expect(b).toMatchObject({ get: 200, post: 200 });
    expect(aWs.echo).toBe('round-trip');
    expect(bWs.echo).toBe('round-trip');
    expect(aStill).toBe(true);
    expect(aScreen).toBe(1);
  });

  test('storage-scope', async ({ page, context }) => {
    const panel = await panelFor();
    await login(page, panel, await panel.token());
    const cookies = await context.cookies();
    const store = await page.evaluate(async () => ({
      local: window.localStorage.length,
      session: Object.keys(window.sessionStorage),
      indexedDB: indexedDB.databases ? (await indexedDB.databases()).length : 'not supported',
      documentCookie: document.cookie,
    }));
    console.log(`S8 storage-scope: cookies ${cookies.length}, localStorage keys ${store.local}, sessionStorage keys ${JSON.stringify(store.session)}, IndexedDB databases ${store.indexedDB}, document.cookie "${store.documentCookie}"`);
    expect(cookies).toEqual([]);
    expect(store.local).toBe(0);
    expect(store.session).toEqual(['intuitik.session']);
    expect(store.documentCookie).toBe('');
    if (store.indexedDB !== 'not supported') expect(store.indexedDB).toBe(0);
  });

  test('token-not-in-request-target', async ({ page }) => {
    const panel = await panelFor();
    const token = await panel.token();
    const requests = [];
    page.on('request', async (r) => {
      requests.push({ url: r.url(), method: r.method(), headers: await r.allHeaders(), body: r.postData() ?? '' });
    });
    await login(page, panel, token);
    await page.reload();
    await page.waitForSelector('#intuitik-app', { state: 'attached', timeout: 15_000 });
    const cred = await credentials(page);
    await tabCalls(page);
    await sleep(500);
    let tokenInTarget = 0;
    let tokenInBody = 0;
    let secretInUrl = 0;
    for (const r of requests) {
      if (!r.url.startsWith(panel.origin) && !r.url.startsWith(`ws://127.0.0.1:${panel.port}`)) continue;
      const target = `${r.method} ${r.url.replace(/#.*$/, '')} ${JSON.stringify(r.headers)}`;
      tokenInTarget += countSecret(target, token);
      tokenInBody += countSecret(r.body, token);
      secretInUrl += countSecret(r.url, cred.s) + countSecret(r.url, cred.c);
    }
    const stderr = panel.stderr();
    const inStderr = countSecret(stderr, token) + countSecret(stderr, cred.s) + countSecret(stderr, cred.c);
    const positive = countSecret(`POST /api/session {"t":"${token}"}`, token);
    console.log(`S15 token-not-in-request-target: ${requests.length} requests; T in request lines and headers ${tokenInTarget} (want 0); T in the exchange body ${tokenInBody} (want 1); S or C in a URL ${secretInUrl} (want 0); T, S, C in stderr ${inStderr} (want 0; no access log exists); searcher positive control ${positive} (want >= 1)`);
    expect(tokenInTarget).toBe(0);
    expect(tokenInBody).toBe(1);
    expect(secretInUrl).toBe(0);
    expect(inStderr).toBe(0);
    expect(positive).toBeGreaterThanOrEqual(1);
  });

  test('v6-squatter-ignored', async ({ page }) => {
    const sq = await squatter();
    const panel = await panelFor({ ports: [sq.port] });
    await login(page, panel, await panel.token());
    const calls = await tabCalls(page);
    console.log(`S1 v6-squatter-ignored: panel port ${panel.port} (squatter [::1]:${sq.port}); API ${calls.get}; squatter accepted ${sq.accepted()} (want 0)`);
    expect(panel.port).toBe(sq.port);
    expect(calls.get).toBe(200);
    expect(sq.accepted()).toBe(0);
    await sq.close();
  });

  test('proxy-loopback-bypass', async ({ browserName }) => {
    const control = 'http://control.invalid/';
    const rows = [];
    const measure = async (name, launcher, options, judged) => {
      const proxy = await recordingProxy();
      const opts = typeof options === 'function' ? options(proxy.port) : options;
      const browser = await launcher.launch(opts);
      const args = browserCommandLines(proxy.port);
      const page = await browser.newPage();
      await page.goto(control, { timeout: 15_000 }).catch(() => {});
      const controlArrived = proxy.arrivals.some((a) => a.includes('control.invalid'));
      const panel = await panelFor();
      const before = proxy.arrivals.length;
      await page.goto(`${panel.origin}/#t=${await panel.token()}`).catch(() => {});
      await page.waitForSelector('#intuitik-app', { state: 'attached', timeout: 10_000 }).catch(() => {});
      await wsTry(page, w1(await credentials(page).catch(() => ({ s: '', c: '' })))).catch(() => {});
      const loopback = proxy.arrivals.slice(before).filter((a) => a.includes(`127.0.0.1:${panel.port}`)).length;
      const row = { name, engine: `${browserName} ${browser.version()}`, args, controlArrived, loopbackArrivals: loopback, judged };
      rows.push(row);
      console.log(`S15 proxy-loopback-bypass ${judged ? 'judged' : 'recorded'} "${name}" ${row.engine}: control arrived ${controlArrived}; loopback arrivals ${loopback}; launch arguments: ${args}`);
      await browser.close();
      await proxy.close();
      return row;
    };
    if (browserName === 'chromium') {
      const def = await measure('Chromium browser default', chromium, (p) => ({ args: [`--proxy-server=http://127.0.0.1:${p}`] }), true);
      if (def.args.includes('<-loopback>')) {
        throw new Error(`STOP-4: the Chromium default row is not a measurement of the browser default: ${def.args}`);
      }
      expect(def.loopbackArrivals).toBe(0);
      await measure('Chromium through the Playwright proxy option', chromium, (p) => ({ proxy: { server: `http://127.0.0.1:${p}` } }), false);
      await measure('Chromium with an explicit <-loopback>', chromium, (p) => ({ args: [`--proxy-server=http://127.0.0.1:${p}`, '--proxy-bypass-list=<-loopback>'] }), false);
    } else {
      const launcher = browserName === 'firefox' ? firefox : webkit;
      const label = browserName === 'firefox' ? 'Firefox' : 'WebKit';
      await measure(`${label} through the Playwright proxy option`, launcher, (p) => ({ proxy: { server: `http://127.0.0.1:${p}` } }), false);
    }
    for (const row of rows) {
      expect(row.controlArrived, `configuration "${row.name}" is unmeasured: the control address did not arrive`).toBe(true);
    }
  });

  // stopRun logs in, stops the panel (interrupt or kill) and checks B-ST, then (after
  // a kill) a recording server on the same port for 10 s.
  async function stopRun(page, how, reloadFixture) {
    const panel = await panelFor();
    if (reloadFixture) {
      // Negative fixture: a stop screen that reloads.
      await page.addInitScript(() => {
        new MutationObserver(() => {
          if (document.querySelector('[data-screen="B-ST"]') && !window.__reloadPlanned) {
            window.__reloadPlanned = true;
            setTimeout(() => window.location.reload(), 2000);
          }
        }).observe(document, { subtree: true, childList: true });
      });
    }
    await login(page, panel, await panel.token());
    const url = page.url();
    if (how === 'interrupt') await panel.interrupt();
    else await panel.kill();
    const lines = await screenLines(page).catch(() => []);
    let arrivals = [];
    if (how === 'kill') {
      const rec = await recordingServer(panel.port);
      await sleep(10_000);
      arrivals = [...rec.arrivals];
      await rec.close();
    }
    return { lines, arrivals, urlSame: page.url() === url };
  }

  test('stop-screen', async ({ page, context }) => {
    for (const how of ['interrupt', 'kill']) {
      const tab = how === 'interrupt' ? page : await context.newPage();
      const r = await stopRun(tab, how, false);
      console.log(`S8 stop-screen ${how}: B-ST expected ${JSON.stringify(TEXTS.stopped)}`);
      console.log(`S8 stop-screen ${how}: B-ST actual   ${JSON.stringify(r.lines)}; recording server arrivals ${r.arrivals.length} (want 0); URL unchanged ${r.urlSame}`);
      expect(r.lines).toEqual(TEXTS.stopped);
      expect(r.arrivals).toEqual([]);
      expect(r.urlSame).toBe(true);
    }
    const fx = await stopRun(await context.newPage(), 'kill', true);
    const fails = fx.arrivals.length > 0;
    console.log(`NEGATIVE-FIXTURE stop-screen reloads: recording server arrivals ${fx.arrivals.length} -> the case ${fails ? 'fails as required' : 'DOES NOT FAIL'}`);
    expect(fails).toBe(true);
  });

  test('clear-site-data-first-load', async ({ page, context }) => {
    const panel = await panelFor();
    const docs = [];
    context.on('response', (r) => {
      if (r.url() === `${panel.origin}/`) docs.push(r.headers()['clear-site-data'] ?? '');
    });
    await login(page, panel, await panel.token());
    const t2 = await panel.enter();
    const tabB = await context.newPage();
    await login(tabB, panel, t2);
    await page.reload();
    await page.waitForSelector('#intuitik-app', { state: 'attached', timeout: 15_000 });
    const a = await tabCalls(page);
    const b = await tabCalls(tabB);
    console.log(`S8 clear-site-data-first-load: Clear-Site-Data per document load ${JSON.stringify(docs)} (want "cache" on the first only); after Enter and reload tab A API ${a.get} POST ${a.post}, tab B API ${b.get}`);
    expect(docs[0]).toBe('"cache"');
    expect(docs.slice(1).every((v) => v === '')).toBe(true);
    expect(a).toMatchObject({ get: 200, post: 200 });
    expect(b.get).toBe(200);
  });

  // lateResponse: tab B's first-load GET / is held before the first exchange; tab A
  // logs in; then B's response arrives with the header decided before the session.
  async function lateResponse(context, fixture) {
    const panel = await panelFor({ hold: true, fixture });
    const tabB = await context.newPage();
    let bHeader = null;
    tabB.on('response', (r) => {
      if (r.url() === `${panel.origin}/`) bHeader = r.headers()['clear-site-data'] ?? '';
    });
    const bLoad = tabB.goto(`${panel.origin}/`).catch(() => {});
    await sleep(1000);
    const tabA = await context.newPage();
    await login(tabA, panel, await panel.token());
    const before = await tabA.evaluate(() => window.sessionStorage.getItem('intuitik.session'));
    panel.release();
    await bLoad;
    await until('held response', 15_000, () => bHeader !== null);
    await sleep(500);
    const after = await tabA.evaluate(() => window.sessionStorage.getItem('intuitik.session'));
    const api = await tabA.evaluate(async (s) => (await fetch('/api/session', { headers: { Authorization: `Bearer ${s}` } })).status, after ?? '');
    return { bHeader, kept: before !== null && after === before, api };
  }

  test('clear-site-data-late-response', async ({ context, browser }) => {
    const r = await lateResponse(context);
    console.log(`S8 clear-site-data-late-response: held response Clear-Site-Data ${r.bHeader} (want "cache"); tab A sessionStorage kept ${r.kept}; tab A API ${r.api}`);
    expect(r.bHeader).toBe('"cache"');
    expect(r.kept).toBe(true);
    expect(r.api).toBe(200);
    const fxContext = await browser.newContext();
    const fx = await lateResponse(fxContext, 'clear-site-data-storage');
    const fails = !(fx.kept && fx.api === 200);
    console.log(`NEGATIVE-FIXTURE clear-site-data-late-response clear-site-data-storage: header ${fx.bHeader}; tab A kept ${fx.kept}, API ${fx.api} -> the case ${fails ? 'fails as required' : 'DOES NOT FAIL'}`);
    await fxContext.close();
    expect(fails).toBe(true);
  });

  test('login-refused-screen', async ({ page, context }) => {
    const panel = await panelFor({ clock: true });
    const token = await panel.token();
    await login(page, panel, token);
    const used = await context.newPage();
    const refusal = used.waitForResponse((r) => r.url() === `${panel.origin}/api/session` && r.request().method() === 'POST');
    await used.goto(`${panel.origin}/#t=${token}`);
    const body = await (await refusal).text();
    const usedLines = await screenLines(used);
    const t2 = await panel.enter();
    const t3 = await panel.enter();
    panel.setClock(61);
    const expired = await context.newPage();
    await expired.goto(`${panel.origin}/#t=${t3}`);
    const expiredLines = await screenLines(expired);
    void t2;
    console.log(`B-RF login-refused-screen expected ${JSON.stringify(TEXTS.refused)}`);
    console.log(`B-RF login-refused-screen used address actual    ${JSON.stringify(usedLines)}`);
    console.log(`B-RF login-refused-screen expired address actual ${JSON.stringify(expiredLines)}`);
    console.log(`B-RF login-refused-screen 403 body equals the lines joined with \\n: ${body === TEXTS.refused.join('\n')}`);
    expect(usedLines).toEqual(TEXTS.refused);
    expect(expiredLines).toEqual(TEXTS.refused);
    expect(body).toBe(TEXTS.refused.join('\n'));
  });
});
