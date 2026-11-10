// Design system gallery in real browsers (packet DS-1 section 6.2): the built gallery in
// Playwright Chromium, Firefox and WebKit under the panel's CSP. Each check first runs on
// its negative fixture page, which must fail it, and then on the gallery.
import { expect, test as base } from '@playwright/test';
import { GALLERY_CSP, identityLines, startGallery } from './gallery_harness.js';

const test = base.extend({
  gallery: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const gallery = await startGallery();
      try {
        await use(gallery);
      } finally {
        await gallery.close();
      }
    },
    { scope: 'worker' },
  ],
});

const COPY_TEXT = '  kubectl get pods -n shop -l app=web  ';

// Installed before any page script: CSP violation events, style attributes set or added
// after load, and every clipboard call with its outcome (the page sees the real result).
function instrument() {
  const ds = { csp: [], styles: [], clipboard: [], loaded: false };
  window.__ds = ds;
  document.addEventListener('securitypolicyviolation', (e) => ds.csp.push(`${e.violatedDirective} ${e.blockedURI}`));
  window.addEventListener('load', () => {
    ds.loaded = true;
    const styled = (node) => node.nodeType === 1 && (node.hasAttribute('style') || node.querySelector('[style]'));
    new MutationObserver((records) => {
      for (const r of records) {
        if (r.type === 'attributes') {
          ds.styles.push(`${r.target.tagName} style="${r.target.getAttribute('style')}"`);
        }
        for (const n of r.addedNodes ?? []) {
          if (styled(n)) {
            ds.styles.push(`${n.tagName} added with a style attribute`);
          }
        }
      }
    }).observe(document.documentElement, { attributes: true, attributeFilter: ['style'], childList: true, subtree: true });
  });
  if (navigator.clipboard && navigator.clipboard.writeText) {
    const writeText = navigator.clipboard.writeText.bind(navigator.clipboard);
    navigator.clipboard.writeText = (text) => {
      const call = { text, outcome: 'pending' };
      ds.clipboard.push(call);
      return writeText(text).then(
        (v) => {
          call.outcome = 'resolved';
          return v;
        },
        (err) => {
          call.outcome = `rejected ${err && err.name}`;
          throw err;
        },
      );
    };
  }
}

async function preparePage(page) {
  const consoleCsp = [];
  const pageErrors = [];
  page.on('console', (m) => {
    if (/content[- ]security[- ]policy|\bCSP\b/i.test(m.text())) {
      consoleCsp.push(m.text());
    }
  });
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  await page.addInitScript(instrument);
  return { consoleCsp, pageErrors };
}

async function openGallery(page, gallery, query = '') {
  const response = await page.goto(`${gallery.url}${query}`, { waitUntil: 'domcontentloaded' });
  expect(response.headers()['content-security-policy']).toBe(GALLERY_CSP);
  await page.locator('[data-gallery-item="C-43.default"]').waitFor();
}

const logOf = (page, id) => page.locator(`[data-event-log="${id}"] li`).allTextContents();

// Runs an action and returns the new event log entries; a problem unless they equal the
// expected entries (waits for the expected count, then a little longer to catch extras).
async function expectEvents(page, id, label, action, expected) {
  const before = await logOf(page, id);
  await action();
  const deadline = Date.now() + 3000;
  while ((await logOf(page, id)).length < before.length + expected.length && Date.now() < deadline) {
    await page.waitForTimeout(25);
  }
  await page.waitForTimeout(250);
  const added = (await logOf(page, id)).slice(before.length);
  const ok = JSON.stringify(added) === JSON.stringify(expected);
  return { line: `${id} ${label}: events ${JSON.stringify(added)}`, problem: ok ? null : `${id} ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(added)}` };
}

const body = (page, id) => page.locator(`[data-gallery-item="${id}"] .gallery-item__body`);

// B-1: the interactive criteria of specification section 3 on the real elements, by mouse
// and keyboard, read back from each item's event log.
async function driveB1(page) {
  const results = [];
  const run = async (...args) => results.push(await expectEvents(page, ...args));
  // C-04: one event per click, Enter and Space; none when disabled.
  for (const v of ['primary', 'secondary', 'text-link']) {
    const enabled = body(page, `C-04.${v}`).locator('button');
    await run(`C-04.${v}`, 'mouse click', () => enabled.click(), ['click']);
    await run(`C-04.${v}`, 'Enter', async () => {
      await enabled.focus();
      await page.keyboard.press('Enter');
    }, ['click']);
    await run(`C-04.${v}`, 'Space', async () => {
      await enabled.focus();
      await page.keyboard.press('Space');
    }, ['click']);
    const disabled = body(page, `C-04.${v}.disabled`).locator('button');
    await run(`C-04.${v}.disabled`, 'mouse click, Enter, Space (disabled)', async () => {
      // force: Playwright treats aria-disabled as not enabled; the real mouse click is still sent.
      await disabled.click({ force: true });
      await disabled.focus();
      await page.keyboard.press('Enter');
      await page.keyboard.press('Space');
    }, []);
    results.push({ line: `C-04.${v}.disabled aria-disabled=${await disabled.getAttribute('aria-disabled')}`, problem: (await disabled.getAttribute('aria-disabled')) === 'true' ? null : `C-04.${v}.disabled has no aria-disabled` });
  }
  // C-21: stage events from the stage switch and the path, by mouse and keyboard.
  await run('C-21.cluster', 'stage switch click Namespace', () => body(page, 'C-21.cluster').getByRole('button', { name: 'Namespace', exact: true }).click(), ['navigate namespace']);
  await run('C-21.cluster', 'stage switch Enter on Workload', async () => {
    await body(page, 'C-21.cluster').getByRole('button', { name: 'Workload', exact: true }).focus();
    await page.keyboard.press('Enter');
  }, ['navigate workload']);
  await run('C-21.workload', 'path click kind-dev', () => body(page, 'C-21.workload').getByRole('button', { name: 'kind-dev', exact: true }).click(), ['navigate cluster']);
  const current = await body(page, 'C-21.workload').locator('[aria-current="page"]').allTextContents();
  results.push({ line: `C-21.workload current after navigate: ${JSON.stringify(current)}`, problem: JSON.stringify(current) === '["kind-dev"]' ? null : `C-21 current stage ${JSON.stringify(current)}` });
  // C-22: group order, Unknown and Waiting collapsed at start, open and close, selection.
  const groups = body(page, 'C-22.groups');
  const order = await groups.locator('[data-group]').evaluateAll((els) => els.map((e) => `${e.dataset.group}:${e.querySelector('button').getAttribute('aria-expanded')}`));
  const expectedOrder = ['broken:true', 'failing:true', 'warning:true', 'unknown:false', 'waiting:false'];
  results.push({ line: `C-22.groups order at start ${JSON.stringify(order)}`, problem: JSON.stringify(order) === JSON.stringify(expectedOrder) ? null : `C-22 order ${JSON.stringify(order)}` });
  await run('C-22.groups', 'close Broken', () => groups.locator('[data-group="broken"] > button').click(), ['toggle broken closed']);
  await run('C-22.groups', 'open Unknown by Enter', async () => {
    await groups.locator('[data-group="unknown"] > button').focus();
    await page.keyboard.press('Enter');
  }, ['toggle unknown open']);
  await run('C-22.groups', 'select item a3', () => groups.locator('[data-item-id="a3"] .ds-attention-item__select').click(), ['select a3']);
  const selected = await groups.locator('.ds-attention-item--selected').evaluateAll((els) => els.map((e) => e.dataset.itemId));
  results.push({ line: `C-22.groups selected ${JSON.stringify(selected)}`, problem: JSON.stringify(selected) === '["a3"]' ? null : `C-22 selected ${JSON.stringify(selected)}` });
  await run('C-22.groups', 'Ask why on a4', () => groups.locator('[data-item-id="a4"]').getByRole('button', { name: 'Ask why' }).click(), ['ask a4']);
  // C-23: tab switch events; Logs only for Pod and workload kinds.
  const pod = body(page, 'C-23.details.pod');
  await run('C-23.details.pod', 'tab Diagnose', () => pod.locator('[data-tab="diagnose"]').click(), ['tab diagnose']);
  await run('C-23.details.pod', 'tab Details', () => pod.locator('[data-tab="details"]').click(), ['tab details']);
  await run('C-23.details.pod', 'sub tab Logs by Enter', async () => {
    await pod.locator('[data-subtab="logs"]').focus();
    await page.keyboard.press('Enter');
  }, ['subtab logs']);
  const subTabs = async (id) => body(page, id).locator('[data-subtab]').evaluateAll((els) => els.map((e) => e.dataset.subtab));
  const podTabs = await subTabs('C-23.details.pod');
  const serviceTabs = await subTabs('C-23.details.service');
  results.push({ line: `C-23 sub tabs Pod ${JSON.stringify(podTabs)}, Service ${JSON.stringify(serviceTabs)}`, problem: podTabs.includes('logs') && !serviceTabs.includes('logs') && serviceTabs.length === 3 ? null : 'C-23 Logs sub tab' });
  // C-26: zoom events.
  const zoom = body(page, 'C-26.default');
  await run('C-26.default', 'click +', () => zoom.getByRole('button', { name: 'Zoom in' }).click(), ['zoom in']);
  await run('C-26.default', 'Space on \u2212', async () => {
    await zoom.getByRole('button', { name: 'Zoom out' }).focus();
    await page.keyboard.press('Space');
  }, ['zoom out']);
  // C-31: selection change events.
  const review = body(page, 'C-31.default');
  await run('C-31.default', 'uncheck Logs', () => review.locator('[data-category="logs"]').click(), ['selection {"categories":{"summaries":true,"events":true,"logs":false},"excluded":[]}']);
  await run('C-31.default', 'exclude Deployment web', () => review.getByRole('button', { name: 'Exclude Deployment web' }).click(), ['selection {"categories":{"summaries":true,"events":true,"logs":false},"excluded":["r2"]}']);
  return results;
}

// C-05 in one page: click Copy, wait for the component's state, and read the recorded call.
async function copyOnce(page) {
  const item = body(page, 'C-05.default');
  await page.bringToFront();
  await item.getByRole('button', { name: 'Copy' }).click();
  await expect(item.locator('.ds-code')).not.toHaveAttribute('data-copy-state', 'idle');
  const state = await item.locator('.ds-code').getAttribute('data-copy-state');
  const calls = await page.evaluate(() => window.__ds.clipboard.map((c) => ({ ...c })));
  const log = await logOf(page, 'C-05.default');
  return { state, calls, log };
}

// C-05 per engine (packet section 6.2).
async function checkCopy(browser, browserName, gallery) {
  const lines = [];
  const problems = [];
  const matches = (state, outcome) => (outcome === 'resolved' ? state === 'copied' : outcome.startsWith('rejected') ? state === 'failed' : false);
  if (browserName === 'chromium') {
    // Copy with clipboard-read and clipboard-write: the real clipboard holds the exact text.
    const granted = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] });
    try {
      const page = await granted.newPage();
      const { pageErrors } = await preparePage(page);
      await openGallery(page, gallery);
      const r = await copyOnce(page);
      const read = await page.evaluate(() => navigator.clipboard.readText());
      lines.push(`C-05 chromium copy: call ${JSON.stringify(r.calls)}, state ${r.state}, clipboard read back ${JSON.stringify(read)}`);
      if (read !== COPY_TEXT || r.state !== 'copied' || r.calls.length !== 1 || r.calls[0].text !== COPY_TEXT || pageErrors.length) {
        problems.push(`C-05 chromium copy: ${JSON.stringify({ read, state: r.state, calls: r.calls, pageErrors })}`);
      }
    } finally {
      await granted.close();
    }
    // Refusal: the clipboard permission denied for the page's context.
    const denied = await browser.newContext();
    try {
      const page = await denied.newPage();
      const { pageErrors } = await preparePage(page);
      await openGallery(page, gallery, '?clipboard-refusal');
      const cdp = await browser.newBrowserCDPSession();
      const { targetInfos } = await cdp.send('Target.getTargets');
      const target = targetInfos.find((t) => t.type === 'page' && t.url.endsWith('?clipboard-refusal'));
      for (const name of ['clipboard-write', 'clipboard-read']) {
        await cdp.send('Browser.setPermission', { permission: { name }, setting: 'denied', origin: gallery.origin, browserContextId: target.browserContextId });
      }
      await cdp.detach();
      const r = await copyOnce(page);
      lines.push(`C-05 chromium refusal: call ${JSON.stringify(r.calls)}, state ${r.state}, page errors ${pageErrors.length}`);
      if (r.calls.length !== 1 || !r.calls[0].outcome.startsWith('rejected') || r.state !== 'failed' || pageErrors.length) {
        problems.push(`C-05 chromium refusal: ${JSON.stringify({ state: r.state, calls: r.calls, pageErrors })}`);
      }
    } finally {
      await denied.close();
    }
  } else {
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      const { pageErrors } = await preparePage(page);
      await openGallery(page, gallery);
      const r = await copyOnce(page);
      const outcome = r.calls[0]?.outcome ?? 'no call';
      lines.push(`C-05 ${browserName}: call writeText ${JSON.stringify(r.calls.map((c) => c.text))} outcome ${outcome}, state ${r.state}, event log ${JSON.stringify(r.log)}, page errors ${pageErrors.length}; clipboard content not verified in ${browserName}`);
      if (r.calls.length !== 1 || r.calls[0].text !== COPY_TEXT || !matches(r.state, outcome) || pageErrors.length) {
        problems.push(`C-05 ${browserName}: ${JSON.stringify({ state: r.state, calls: r.calls, pageErrors })}`);
      }
    } finally {
      await context.close();
    }
  }
  return { lines, problems };
}

// B-2 comparison for one card: the reported port centres against the rows measured in the
// page, with the card's top-left corner as origin, row ids matched.
async function portProblems(page, id, tolerance = 0.05) {
  const r = await page.evaluate((itemId) => {
    const item = document.querySelector(`[data-gallery-item="${itemId}"]`);
    const card = item.querySelector('.ds-card');
    const top = card.getBoundingClientRect().top;
    const measured = {};
    for (const row of card.querySelectorAll('.ds-card__rows > [data-row-id]')) {
      const b = row.getBoundingClientRect();
      measured[row.dataset.rowId] = b.top - top + b.height / 2;
    }
    const h = card.querySelector('.ds-card__header').getBoundingClientRect();
    const text = item.querySelector(`[data-port-centres="${itemId}"]`).textContent;
    return { measured, measuredIn: h.top - top + h.height / 2, reported: text ? JSON.parse(text) : null };
  }, id);
  const problems = [];
  if (!r.reported) {
    return { r, problems: [`${id}: no reported centres`] };
  }
  const ids = Object.keys(r.measured);
  if (JSON.stringify(Object.keys(r.reported.out).sort()) !== JSON.stringify([...ids].sort())) {
    problems.push(`${id}: row ids ${JSON.stringify(Object.keys(r.reported.out))} vs ${JSON.stringify(ids)}`);
  }
  for (const rowId of ids) {
    if (!(Math.abs(r.reported.out[rowId] - r.measured[rowId]) <= tolerance)) {
      problems.push(`${id} row ${rowId}: reported ${r.reported.out[rowId]} measured ${r.measured[rowId]}`);
    }
  }
  if (!(Math.abs(r.reported.in - r.measuredIn) <= tolerance)) {
    problems.push(`${id} in port: reported ${r.reported.in} measured ${r.measuredIn}`);
  }
  return { r, problems };
}

// Compares, allowing the callback one layout pass to report (up to 2 s).
async function settledPortProblems(page, id) {
  const deadline = Date.now() + 2000;
  let last = await portProblems(page, id);
  while (last.problems.length && Date.now() < deadline) {
    await page.waitForTimeout(50);
    last = await portProblems(page, id);
  }
  return last;
}

const PORT_ITEMS = ['C-14.compact.long-reason', 'C-14.compact.folded'];
const fontState = (page) => page.evaluate(() => ({ geist: document.fonts.check('12px Geist'), mono: document.fonts.check("12px 'Geist Mono'") }));

// B-2: ports after layout, with the fonts not loaded, then loaded, then after expanding and
// collapsing; the reason text fully visible. The page must be opened with the fonts held.
async function driveB2(page, gallery) {
  const lines = [];
  const problems = [];
  const deadline = Date.now() + 15_000;
  while (gallery.heldFonts() < 2 && Date.now() < deadline) {
    await page.waitForTimeout(25);
  }
  const before = await fontState(page);
  lines.push(`fonts held: ${gallery.heldFonts()} font request(s) pending, document.fonts.check Geist ${before.geist}, Geist Mono ${before.mono}`);
  if (gallery.heldFonts() !== 2 || before.geist || before.mono) {
    problems.push(`not-loaded state not reached: ${gallery.heldFonts()} pending, ${JSON.stringify(before)}`);
  }
  const compare = async (phase) => {
    for (const id of PORT_ITEMS) {
      const { r, problems: p } = await settledPortProblems(page, id);
      lines.push(`${phase} ${id}: reported ${JSON.stringify(r.reported)} measured ${JSON.stringify({ in: r.measuredIn, out: r.measured })}`);
      problems.push(...p.map((x) => `${phase}: ${x}`));
    }
  };
  await compare('fonts not loaded');
  gallery.releaseFonts();
  await page.evaluate(() => document.fonts.ready.then(() => true));
  const after = await fontState(page);
  lines.push(`fonts released: document.fonts.check Geist ${after.geist}, Geist Mono ${after.mono}`);
  if (!after.geist || !after.mono) {
    problems.push(`loaded state not reached: ${JSON.stringify(after)}`);
  }
  await compare('fonts loaded');
  const toggle = body(page, 'C-14.compact.folded').getByRole('button', { name: /^(Expand|Collapse)$/ });
  await toggle.click();
  await expect(body(page, 'C-14.compact.folded').locator('.ds-card')).toHaveAttribute('data-card-mode', 'expanded');
  await compare('expanded');
  await toggle.click();
  await expect(body(page, 'C-14.compact.folded').locator('.ds-card')).toHaveAttribute('data-card-mode', 'compact');
  await compare('collapsed');
  // The reason text is fully visible.
  const reasons = await page.locator('[data-gallery-item="C-14.compact.long-reason"] .ds-refrow__reason').evaluateAll((els) =>
    els.map((e) => {
      const s = getComputedStyle(e);
      return { text: e.textContent.length, scrollHeight: e.scrollHeight, clientHeight: e.clientHeight, lineClamp: s.webkitLineClamp ?? s.getPropertyValue('-webkit-line-clamp'), maxHeight: s.maxHeight, overflowY: s.overflowY, textOverflow: s.textOverflow };
    }),
  );
  for (const x of reasons) {
    lines.push(`reason element ${JSON.stringify(x)}`);
    if (x.scrollHeight !== x.clientHeight || !['none', '', undefined].includes(x.lineClamp) || x.maxHeight !== 'none' || x.overflowY !== 'visible') {
      problems.push(`reason hidden: ${JSON.stringify(x)}`);
    }
  }
  if (!reasons.some((x) => x.text === 120)) {
    problems.push('no 120-character reason element');
  }
  return { lines, problems };
}

// B-3 state of a page: violation events, CSP console messages, style attributes after load.
async function cspProblems(page, consoleCsp) {
  const ds = await page.evaluate(() => ({ csp: window.__ds.csp, styles: window.__ds.styles }));
  const problems = [...ds.csp.map((c) => `securitypolicyviolation ${c}`), ...consoleCsp.map((c) => `console ${c}`), ...ds.styles.map((s) => `style after load: ${s}`)];
  return { line: `securitypolicyviolation events ${ds.csp.length}, CSP console messages ${consoleCsp.length}, style attributes after load ${ds.styles.length}`, problems };
}

// B-4 state: requests to another origin and responses other than 200.
function requestRecorder(page, origin) {
  const requests = [];
  const statuses = new Map();
  page.on('request', (r) => requests.push(r.url()));
  page.on('response', (r) => statuses.set(r.url(), r.status()));
  page.on('requestfailed', (r) => statuses.set(r.url(), `failed ${r.failure()?.errorText}`));
  return () => {
    const other = requests.filter((u) => new URL(u).origin !== origin);
    const bad = requests.filter((u) => new URL(u).origin === origin && statuses.get(u) !== 200).map((u) => `${new URL(u).pathname} ${statuses.get(u)}`);
    return { requests, other, bad };
  };
}

function printAll(lines) {
  for (const line of lines) {
    console.log(line);
  }
}

async function header(browser, browserName) {
  printAll([...identityLines(), `browser ${browserName} ${browser.version()}`]);
}

test.describe('gallery', () => {
  test('B-1 wired behaviour', async ({ page, browser, browserName, gallery }) => {
    await header(browser, browserName);
    // Negative fixture: an element whose handler emits its event twice.
    await page.goto(gallery.negative('b1-double-event.html'));
    const neg = await expectEvents(page, 'NEGATIVE', 'mouse click', () => body(page, 'NEGATIVE').locator('button').click(), ['click']);
    console.log(`NEGATIVE-FIXTURE B-1 b1-double-event.html: failed conditions [${neg.problem}] -> the check fails as required`);
    expect(neg.problem, 'the B-1 check must fail on its negative fixture').not.toBeNull();

    const { pageErrors } = await preparePage(page);
    await openGallery(page, gallery);
    const results = await driveB1(page);
    printAll(results.map((r) => `B-1 ${browserName} ${r.line}`));
    const copy = await checkCopy(browser, browserName, gallery);
    printAll(copy.lines.map((l) => `B-1 ${browserName} ${l}`));
    const problems = [...results.map((r) => r.problem).filter(Boolean), ...copy.problems, ...pageErrors.map((e) => `page error ${e}`)];
    expect(problems).toEqual([]);
  });

  test('B-2 ports after layout', async ({ page, browser, browserName, gallery }) => {
    await header(browser, browserName);
    // Negative fixture: a card that reports port centres shifted by one row gap.
    await page.goto(gallery.negative('b2-shifted-ports.html'));
    await expect(page.locator('[data-port-centres="NEGATIVE"]')).not.toHaveText('');
    const neg = await portProblems(page, 'NEGATIVE');
    console.log(`NEGATIVE-FIXTURE B-2 b2-shifted-ports.html: failed conditions ${JSON.stringify(neg.problems)} -> the check fails as required`);
    expect(neg.problems.length, 'the B-2 check must fail on its negative fixture').toBeGreaterThan(0);

    const { pageErrors } = await preparePage(page);
    gallery.holdFonts();
    try {
      await openGallery(page, gallery);
      const b2 = await driveB2(page, gallery);
      printAll(b2.lines.map((l) => `B-2 ${browserName} ${l}`));
      expect([...b2.problems, ...pageErrors]).toEqual([]);
    } finally {
      gallery.releaseFonts();
    }
  });

  test('B-3 CSP at run time', async ({ page, browser, browserName, gallery }) => {
    await header(browser, browserName);
    // Negative fixture: a page that sets a style attribute after load.
    const negLog = await preparePage(page);
    await page.goto(gallery.negative('b3-style-after-load.html'), { waitUntil: 'load' });
    await page.waitForFunction(() => window.__ds.styles.length > 0, null, { timeout: 5000 }).catch(() => {});
    const neg = await cspProblems(page, negLog.consoleCsp);
    console.log(`NEGATIVE-FIXTURE B-3 b3-style-after-load.html: failed conditions ${JSON.stringify(neg.problems)} -> the check fails as required`);
    expect(neg.problems.length, 'the B-3 check must fail on its negative fixture').toBeGreaterThan(0);

    const fresh = await page.context().newPage();
    const { consoleCsp, pageErrors } = await preparePage(fresh);
    gallery.holdFonts();
    try {
      await openGallery(fresh, gallery);
      const loading = await cspProblems(fresh, consoleCsp);
      console.log(`B-3 ${browserName} during loading: ${loading.line}`);
      const b1 = await driveB1(fresh);
      const b2 = await driveB2(fresh, gallery);
      const after = await cspProblems(fresh, consoleCsp);
      console.log(`B-3 ${browserName} during B-1 (${b1.length} checks) and B-2 (${b2.lines.length} lines): ${after.line}`);
      expect([...after.problems, ...pageErrors]).toEqual([]);
    } finally {
      gallery.releaseFonts();
    }
  });

  test('B-4 requests', async ({ page, browser, browserName, gallery }) => {
    await header(browser, browserName);
    // Negative fixture: a page that requests a URL on another origin.
    const negReport = requestRecorder(page, gallery.origin);
    await page.goto(gallery.negative('b4-other-origin.html'));
    await page.waitForEvent('request', { predicate: (r) => new URL(r.url()).origin !== gallery.origin, timeout: 5000 }).catch(() => {});
    const neg = negReport();
    console.log(`NEGATIVE-FIXTURE B-4 b4-other-origin.html: failed conditions ${JSON.stringify(neg.other.map((u) => `other origin ${u}`))} -> the check fails as required`);
    expect(neg.other.length, 'the B-4 check must fail on its negative fixture').toBeGreaterThan(0);

    const fresh = await page.context().newPage();
    const report = requestRecorder(fresh, gallery.origin);
    const { pageErrors } = await preparePage(fresh);
    gallery.holdFonts();
    try {
      await openGallery(fresh, gallery);
      await driveB1(fresh);
      await driveB2(fresh, gallery);
    } finally {
      gallery.releaseFonts();
    }
    // The licence link answers too (HTML, CSS, JS, woff2 and OFL.txt served together).
    const oflStatus = await fresh.evaluate(async () => (await fetch(document.querySelector('.gallery-footer a').href)).status);
    await fresh.waitForTimeout(200);
    const r = report();
    const kinds = ['.html', '.css', '.js', '.woff2', 'OFL.txt'].map((k) => `${k} ${r.requests.filter((u) => u.endsWith(k)).length}`);
    console.log(`B-4 ${browserName} requests ${r.requests.length}, other origin ${r.other.length}, not 200 ${r.bad.length}; by kind ${kinds.join(', ')}; OFL.txt ${oflStatus}`);
    for (const u of r.requests) {
      console.log(`B-4 ${browserName} request ${new URL(u).pathname}`);
    }
    expect(r.other).toEqual([]);
    expect(r.bad).toEqual([]);
    expect(oflStatus).toBe(200);
    for (const k of ['.html', '.css', '.js', '.woff2', 'OFL.txt']) {
      expect(r.requests.some((u) => u.endsWith(k)), k).toBe(true);
    }
    expect(pageErrors).toEqual([]);
  });
});
