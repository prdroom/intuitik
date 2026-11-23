// The prototype cases of packet FA2-T8 (sections 5 and 6.3 to 6.5). Every case writes its
// lines to the output log and its captures to INTUITIK_PROTO_OUT; the job packs both.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { gunzipSync } from 'node:zlib';
import { expect, test } from '@playwright/test';
import { canonical, derive500 } from '../measure/derive500.js';
import { readTar } from '../measure/tar.js';
import { BRIDGE_SENTENCES, DS, S, SESSION_LINES } from '../strings.js';
import {
  DIST,
  FIXED_LIST_SHA256,
  PROTO,
  REPO,
  clickCard,
  control,
  distFile,
  fixedList,
  fixedTable,
  goScreen,
  hasScreen,
  log,
  openApp,
  pageTexts,
  readRepo,
  screenNow,
  sha256,
  waitScreen,
  writeOut,
} from './helpers.js';

const BASE = 'http://localhost:4317';

test('fixed tables', async () => {
  const listSha = sha256(readRepo('web/prototype/e2e/fixed.sha256'));
  log(`fixed tables: web/prototype/e2e/fixed.sha256 sha256=${listSha} expected=${FIXED_LIST_SHA256}`);
  expect(listSha).toBe(FIXED_LIST_SHA256);
  const list = fixedList();
  expect(Object.keys(list).sort()).toEqual(['web/prototype/data/canaries.json', 'web/prototype/e2e/controls.json', 'web/prototype/e2e/screens.json']);
  for (const [rel, want] of Object.entries(list)) {
    const got = sha256(readRepo(rel));
    log(`fixed tables: ${rel} sha256=${got} expected=${want} ${got === want ? 'ok' : 'FAIL'}`);
    expect(got).toBe(want);
  }
  log('fixed tables: PASS');
});

// ---------------------------------------------------------------------------------------
// C1: every screen of 3.3 captured with the steps that reached it, every 3.7 path followed.

// How the walk moves between two consecutive entries of a 3.7 path. A step is
// [verb, data-control key, data-target or scope, value]. "event" steps use the strip: they
// stand for what the fixed server or the environment does (loading ends, the connection comes
// back, the key file cannot be removed), which no product control causes.
const MOVES = {
  'P48>P02': [['click', 'tile', 'shop']],
  'P48>P49': [['fill', 'open-namespace', null, 'payments'], ['press', 'open-namespace', null, 'Enter']],
  'P01>P02': [['click', 'tile', 'shop']],
  'P02>P06': [['click', 'card', 'shop/Service/web']],
  'P06>P25': [['row', 'card', 'shop/Service/web']],
  'P25>P22': [['click', 'card', 'shop/Service/web'], ['subtab', 'inspector-subtab', 'events']],
  'P06>action:Ask why': [['click', 'ask-why', '.proto-inspector']],
  'action:Ask why>P28': [['review-remove', 'review-remove', 'u-shop-ingress-shop']],
  'P28>P31': [['click', 'send']],
  'P37>P38': [['click', 'test-connection']],
  'P38>action:first Send': [['click', 'ask'], ['fill', 'question-field', null, 'Why is Service web not reaching any Pod?'], ['click', 'send']],
  'action:first Send>P29': [],
  'P46>P01': [['event', 'P01', 'loading finished']],
  'P52>P53': [['event', 'P53', 'reconnect succeeded']],
  'P21>action:Copy (C4)': [['click', 'copy-yaml']],
  'P28>action:exclude Logs': [['uncheck', 'review-checkbox', 'logs']],
  'action:exclude Logs>P32': [['click', 'send']],
  'P37>P40': [['click', 'delete-saved-key']],
  'P37>P41': [['event', 'P41', 'the key file cannot be removed']],
};

async function move(page, [verb, key, target, value]) {
  if (verb === 'event') {
    await page.locator(`[data-control="strip-${key}"]`).click();
    return `event (${target}): strip-${key}`;
  }
  if (verb === 'click' && key === 'card') {
    await clickCard(page, target);
    return `click card ${target}`;
  }
  if (verb === 'row') {
    await page.locator(`[data-control="card"][data-target="${target}"] [data-control="card"][data-target^="row:"]`).first().click();
    return `click row of ${target}`;
  }
  if (verb === 'subtab') {
    await page.locator(`[data-control="${key}"][data-subtab="${target}"]`).click();
    return `click ${key} ${target}`;
  }
  if (verb === 'review-remove') {
    await page.locator(`.ds-review__resource[data-resource-id="${target}"] [data-control="${key}"]`).click();
    return `click ${key} ${target}`;
  }
  if (verb === 'uncheck') {
    await page.locator(`[data-control="${key}"][data-category="${target}"]`).uncheck();
    return `uncheck ${key} ${target}`;
  }
  const scoped = target && target.startsWith('.');
  const el = await control(page, key, scoped ? null : target, scoped ? target : '');
  if (verb === 'fill') {
    await el.fill(value);
    return `fill ${key} "${value}"`;
  }
  if (verb === 'press') {
    await el.press(value);
    return `press ${value} in ${key}`;
  }
  await el.click();
  return `click ${key}${target && !scoped ? ` ${target}` : ''}`;
}

test('C1 screen list', async ({ page }) => {
  const table = fixedTable('web/prototype/e2e/screens.json', 'C1');
  expect(table, 'screens.json equals the fixed table').not.toBeNull();
  const ids = table.screens.map((s) => s.id);
  log(`C1 screens.json ids=${ids.length} paths=${table.paths.length}`);
  expect(ids.length).toBe(65);
  expect(table.paths.length).toBe(10);
  const fails = [];
  const missing = new Set();
  let captured = 0;
  for (const id of ids) {
    let steps = `open index.html (P01) > strip-${id}`;
    let reached;
    if (id === 'P54') {
      reached = (await goScreen(page, 'P03')) && (await hasScreen(page, 'P54'));
      if (reached) {
        writeOut('P54-before.png', await page.screenshot());
        await page.locator('[data-control="strip-P54"]').click();
        await waitScreen(page, 'P54');
        steps = 'open index.html (P01) > strip-P03 (before: P54-before.png) > strip-P54 (event: the Pod was replaced)';
      }
    } else {
      reached = await goScreen(page, id);
    }
    if (!reached) {
      missing.add(id);
      continue;
    }
    writeOut(`${id}.png`, await page.screenshot());
    writeOut(`${id}.dom.html`, await page.content());
    captured++;
    log(`C1 ${id} capture=${id}.png dom=${id}.dom.html steps=${steps}`);
  }
  for (const id of missing) {
    const journeys = table.paths.filter((p) => [...p.success, ...p.failure].some((list) => list.includes(id))).map((p) => p.journey);
    const line = `FAIL C1 missing=${id} path=${journeys.length ? journeys.join(',') : 'none'}`;
    log(line);
    fails.push(line);
  }
  let followed = 0;
  for (const p of table.paths) {
    let ok = true;
    for (const [variant, lists] of [['success', p.success], ['failure', p.failure]]) {
      for (const list of lists) {
        if (list.some((e) => missing.has(e))) {
          ok = false;
          continue;
        }
        const first = list[0];
        await goScreen(page, first);
        const trail = [`${first} (strip-${first})`];
        for (let i = 1; i < list.length; i++) {
          const key = `${list[i - 1]}>${list[i]}`;
          const moves = MOVES[key];
          if (!moves) {
            const line = `FAIL C1 path=${p.journey} ${variant} no move for ${key}`;
            log(line);
            fails.push(line);
            ok = false;
            break;
          }
          const done = [];
          for (const m of moves) {
            done.push(await move(page, m));
          }
          if (!list[i].startsWith('action:')) {
            try {
              await waitScreen(page, list[i]);
            } catch {
              const line = `FAIL C1 path=${p.journey} ${variant} expected=${list[i]} got=${(await screenNow(page)) || 'none'} after ${done.join(', ')}`;
              log(line);
              fails.push(line);
              ok = false;
              break;
            }
          }
          trail.push(`${done.join(', ') || 'shown'} > ${list[i]}`);
        }
        log(`C1 path=${p.journey} ${variant} ${trail.join(' > ')}`);
      }
    }
    if (ok) {
      followed++;
    }
  }
  const result = fails.length === 0 && captured === 65 && followed === 10 ? 'PASS' : 'FAIL';
  log(`screens expected=65 captured=${captured} paths=10/${followed} result=${result}`);
  expect(fails).toEqual([]);
  expect(result).toBe('PASS');
});

// ---------------------------------------------------------------------------------------
// C2: the strings of UI.md 4.4, 8 and 7 errors, the FA2-T7b Pod texts of the data and the
// M1-T2 lines, each in the dictionary and in the DOM of its screen; no prototype source but
// strings.js writes a dictionary string.

function dictionaryValue(key) {
  const [kind, name, line] = key.split(':');
  if (kind === 'bridge') {
    return (BRIDGE_SENTENCES.find((b) => b.id === name) ?? {}).sentence;
  }
  if (kind === 'S') {
    return S[name];
  }
  if (kind === 'ds') {
    return DS[name];
  }
  if (kind === 'session') {
    return SESSION_LINES[name][Number(line)];
  }
  return undefined;
}

function sources(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) {
      if (!['dist', 'e2e', 'data', 'tools', 'node_modules'].includes(name)) {
        sources(full, out);
      }
    } else if (/\.(js|jsx)$/.test(name) && name !== 'strings.js' && !name.endsWith('.test.js')) {
      out.push(full);
    }
  }
  return out;
}

test('C2 strings', async ({ page }) => {
  const expected = JSON.parse(readFileSync(path.join(PROTO, 'e2e', 'expected-strings.json'), 'utf8')).strings;
  const fails = [];
  let dict = 0;
  let dictOk = 0;
  for (const e of expected.filter((x) => !x.key.startsWith('data:'))) {
    dict++;
    if (dictionaryValue(e.key) === e.text) {
      dictOk++;
    } else {
      fails.push(`FAIL C2 string=${e.key} dictionary differs`);
      log(fails[fails.length - 1]);
    }
  }
  const files = sources(PROTO);
  let literals = 0;
  for (const e of expected.filter((x) => !x.key.startsWith('data:'))) {
    const forms = [e.text, JSON.stringify(e.text).slice(1, -1).replace(/[\u0080-\uffff]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`)];
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      if (forms.some((form) => src.includes(`'${form}'`) || src.includes(`"${form}"`) || src.includes(`\`${form}\``) || src.includes(`>${form}<`))) {
        literals++;
        fails.push(`FAIL C2 string=${e.key} literal in ${path.relative(REPO, f).replace(/\\/g, '/')}`);
        log(fails[fails.length - 1]);
      }
    }
  }
  let dom = 0;
  const byScreen = new Map();
  for (const e of expected) {
    byScreen.set(e.screen, [...(byScreen.get(e.screen) ?? []), e]);
  }
  for (const [screen, list] of byScreen) {
    if (!(await goScreen(page, screen))) {
      for (const e of list) {
        fails.push(`FAIL C2 string=${e.key} screen=${screen} not reached`);
        log(fails[fails.length - 1]);
      }
      continue;
    }
    const texts = await pageTexts(page);
    for (const e of list) {
      if (texts.some((t) => t.includes(e.text))) {
        dom++;
        log(`C2 string=${e.key} screen=${screen} source=${e.source} found`);
      } else {
        fails.push(`FAIL C2 string=${e.key} screen=${screen} not in DOM`);
        log(fails[fails.length - 1]);
      }
    }
  }
  log(`C2 strings=${expected.length} dictionary=${dictOk}/${dict} dom=${dom}/${expected.length} literals=${literals} result=${fails.length === 0 ? 'PASS' : 'FAIL'}`);
  expect(fails).toEqual([]);
});

// ---------------------------------------------------------------------------------------
// C3: every interactive element of every screen has a key of the 3.8 table, none is of class
// cluster change, and every row's control runs once with no request outside the build.

const INTERACTIVE = [
  'button',
  'a[href]',
  'input:not([type="hidden"])',
  'select',
  'textarea',
  '[role="button"]',
  '[role="link"]',
  '[role="menuitem"]',
  '[role="checkbox"]',
  '[role="textbox"]',
  '[role="combobox"]',
  '[role="tab"]',
  '[tabindex]:not([tabindex^="-"])',
].join(', ');

async function elementsOf(page) {
  return page.evaluate((sel) => {
    return Array.from(document.querySelectorAll(sel)).map((el) => ({
      key: el.getAttribute('data-control') ?? '',
      role: el.getAttribute('role') ?? el.tagName.toLowerCase(),
      label: (el.getAttribute('aria-label') || el.textContent || el.getAttribute('value') || '').replace(/\s+/g, ' ').trim().slice(0, 60),
    }));
  }, INTERACTIVE);
}

async function routed(context) {
  const outside = [];
  await context.route('**/*', (route) => {
    const req = route.request();
    const url = new URL(req.url());
    if (url.origin === BASE && req.method() === 'GET' && distFile(url.pathname)) {
      return route.continue();
    }
    outside.push(`${req.method()} ${req.url()}`);
    return route.abort();
  });
  return outside;
}

test('C3 controls', async ({ browser }) => {
  const table = fixedTable('web/prototype/e2e/controls.json', 'C3');
  expect(table, 'controls.json equals the fixed table').not.toBeNull();
  const screens = fixedTable('web/prototype/e2e/screens.json', 'C3');
  const rows = new Map(table.controls.map((r) => [r.key, r]));
  const context = await browser.newContext({ baseURL: BASE });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE });
  const outside = await routed(context);
  const page = await context.newPage();
  const fails = [];
  const firstScreen = new Map();
  const unlisted = new Set();
  let total = 0;
  let present = 0;
  for (const { id } of screens.screens) {
    if (!(await goScreen(page, id))) {
      continue;
    }
    present++;
    const els = await elementsOf(page);
    total += els.length;
    let bad = 0;
    for (const el of els) {
      const row = rows.get(el.key);
      if (!row) {
        bad++;
        const k = `${el.key}|${el.label}`;
        if (!unlisted.has(k)) {
          unlisted.add(k);
          fails.push(`FAIL C3 unlisted screen=${id} control=${el.label}`);
          log(fails[fails.length - 1]);
        }
        continue;
      }
      if (row.class === 'cluster change') {
        fails.push(`FAIL C3 cluster-change screen=${id} control=${el.label}`);
        log(fails[fails.length - 1]);
      }
      if (!firstScreen.has(el.key) || (row.screens.includes(id) && !row.screens.includes(firstScreen.get(el.key)))) {
        firstScreen.set(el.key, id);
      }
    }
    log(`C3 screen=${id} elements=${els.length} unlisted=${bad}`);
  }
  let exercised = 0;
  for (const row of table.controls) {
    const id = row.key === 'esc' ? 'P03' : firstScreen.get(row.key);
    if (!id) {
      log(`C3 not-rendered key=${row.key} (no screen shows it)`);
      continue;
    }
    const before = outside.length;
    await goScreen(page, id);
    const url = page.url();
    let clip = '';
    if (row.key === 'esc') {
      await page.keyboard.press('Escape');
    } else {
      const el = page.locator(`[data-control="${row.key}"]`).first();
      const tag = await el.evaluate((n) => `${n.tagName.toLowerCase()}:${n.getAttribute('type') ?? ''}`);
      if (tag === 'select:') {
        const values = await el.locator('option').evaluateAll((os) => os.map((o) => o.value));
        await el.selectOption(values[values.length - 1]);
      } else if (tag === 'input:checkbox' || tag === 'input:radio') {
        await el.click();
      } else if (tag.startsWith('input:') || tag === 'textarea:') {
        await el.fill(row.key === 'open-namespace' ? 'payments' : 'web');
        await el.press(row.key === 'open-namespace' ? 'Enter' : 'Tab');
      } else {
        await el.click();
      }
      if (row.class === 'copy') {
        await page.waitForTimeout(300);
        clip = await page.evaluate(() => navigator.clipboard.readText());
        if (!clip) {
          fails.push(`FAIL C3 copy key=${row.key} clipboard empty`);
          log(fails[fails.length - 1]);
        }
      }
    }
    await page.waitForTimeout(150);
    const left = !page.url().startsWith(BASE);
    if (left || (row.class === 'local' && page.url() !== url)) {
      fails.push(`FAIL C3 navigation key=${row.key} url=${page.url()}`);
      log(fails[fails.length - 1]);
    }
    const out = outside.length - before;
    if (out > 0) {
      fails.push(`FAIL C3 network key=${row.key} requests-outside=${out} first=${outside[before]}`);
      log(fails[fails.length - 1]);
    }
    exercised++;
    log(`C3 exercised key=${row.key} class=${row.class} screen=${id} requests-outside=${out}${row.class === 'copy' ? ` clipboard-bytes=${Buffer.byteLength(clip)}` : ''}`);
  }
  await context.close();
  const cluster = table.controls.filter((r) => r.class === 'cluster change').length;
  log(`C3 screens=${present} elements=${total} unlisted=${unlisted.size} cluster-change=${cluster} exercised=${exercised}/${table.controls.length} network-outside=${outside.length} result=${fails.length === 0 ? 'PASS' : 'FAIL'}`);
  expect(fails).toEqual([]);
});

// ---------------------------------------------------------------------------------------
// C4: no Secret value in any screen, YAML view, copy, send preview or Review.

// selectCard is how C4 selects a card (decision T8-RED-R2): as a user does, the card is first
// brought inside the visible map area with the map's zoom-out control when it is not there,
// then its header gets a normal click, and the Inspector must show that same object.
async function inMap(page, locator) {
  const b = await locator.boundingBox();
  const c = await page.locator('.proto-map__canvas').boundingBox();
  return Boolean(b && c && b.x >= c.x && b.y >= c.y && b.x + b.width <= c.x + c.width && b.y + b.height <= c.y + c.height);
}

async function selectCard(page, target) {
  const [ns, kind, name] = target.split('/');
  await page.locator('.proto[data-ready="1"]').waitFor();
  const header = page.locator(`[data-control="card"][data-target="${target}"] .ds-card__header`);
  let zoomedOut = 0;
  while (!(await inMap(page, header)) && zoomedOut < 5) {
    await page.locator('[data-control="zoom-out"]').click();
    zoomedOut++;
  }
  await header.click();
  const shown = await page.locator('.proto-inspector .ds-inspector').first().evaluate((el) => {
    const row = Array.from(el.querySelectorAll('.ds-deflist__row')).find((r) => r.querySelector('dt').textContent === 'Namespace');
    return {
      kind: el.getAttribute('data-kind'),
      name: (el.querySelector('.ds-inspector__name') || {}).textContent,
      ns: row ? row.querySelector('dd').textContent : null,
    };
  });
  log(`C4 selected ${target} zoom-out=${zoomedOut} inspector=${shown.ns}/${shown.kind}/${shown.name}`);
  if (shown.ns !== ns || shown.kind !== kind || shown.name !== name) {
    throw new Error(`C4 selection of ${target} shows ${shown.ns}/${shown.kind}/${shown.name} in the Inspector`);
  }
}

test('C4 secret canary', async ({ browser }) => {
  const table = fixedTable('web/prototype/data/canaries.json', 'C4');
  expect(table, 'canaries.json equals the fixed table').not.toBeNull();
  const secrets = new Set(table.rows.map((r) => `${r.namespace}/${r.secret}`));
  const namespaces = new Set(table.rows.map((r) => r.namespace));
  log(`C4 table rows=${table.rows.length} secrets=${secrets.size} namespaces=${namespaces.size}`);
  expect(secrets.size).toBeGreaterThanOrEqual(3);
  expect(namespaces.size).toBeGreaterThanOrEqual(2);
  const detect = table.rows.flatMap((r) => [
    { s: r.canary, secret: `${r.namespace}/${r.secret}`, key: r.key, form: 'raw' },
    { s: r.base64, secret: `${r.namespace}/${r.secret}`, key: r.key, form: 'base64' },
  ]);
  const opened = [...new Set(table.rows.filter((r) => r.readable).map((r) => `${r.namespace}/${r.secret}`))];
  const hits = new Map();
  const scan = (place, text) => {
    for (const d of detect) {
      if (text.includes(d.s)) {
        hits.set(`${place}|${d.secret}|${d.key}|${d.form}`, { place, ...d });
      }
    }
  };
  const context = await browser.newContext({ baseURL: BASE });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE });
  const requests = [];
  context.on('request', (r) => requests.push(r.url()));
  const page = await context.newPage();
  const fails = [];
  const counts = { yaml: 0, copy: 0, preview: 0, review: 0 };
  const referrer = { 'shop/db-credentials': ['shop', 'shop/Deployment/api'], 'media/media-tls': ['media', 'media/Deployment/transcoder'] };
  for (const pair of opened) {
    const [ns, name] = pair.split('/');
    // YAML view and its Copy: every Secret of the opened set is selected by a normal click on
    // its card header with the Inspector identity check (decision 48b9d689, correction 51d86b2b).
    await goScreen(page, 'P01');
    await page.locator(`[data-control="tile"][data-target="${ns}"]`).click();
    await selectCard(page, `${ns}/Secret/${name}`);
    await page.locator('[data-control="inspector-subtab"][data-subtab="yaml"]').click();
    await page.locator('.proto[data-ready="1"] .proto-yaml').waitFor();
    const place = `yaml ${pair} DOM`;
    await page.locator('.proto-yaml .ds-code').waitFor();
    const yamlText = await page.locator('.proto-yaml').innerText();
    counts.yaml++;
    const keys = table.rows.filter((r) => `${r.namespace}/${r.secret}` === pair).map((r) => r.key);
    const masked = keys.every((k) => yamlText.includes(`${k}: ${S.hidden}`)) && yamlText.includes(S.secretNotice);
    if (!masked) {
      fails.push(`FAIL C4 yaml-masking secret=${pair}`);
      log(fails[fails.length - 1]);
    }
    scan(place, (await pageTexts(page)).join('\n'));
    await page.locator('[data-control="copy-yaml"]').click();
    await page.waitForTimeout(300);
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    counts.copy++;
    scan(`clipboard ${pair}`, clip);
    // Send preview and Review for a resource that references the Secret.
    const [refNs, refTarget] = referrer[pair];
    await goScreen(page, 'P01');
    await page.locator(`[data-control="tile"][data-target="${refNs}"]`).click();
    await selectCard(page, refTarget);
    await page.locator('.proto-inspector [data-control="ask-why"]').click();
    await page.locator('[data-send-preview]').waitFor();
    const preview = await page.locator('[data-send-preview]').innerText();
    const summary = await page.locator('.ds-send-summary').innerText();
    counts.preview++;
    scan(`send preview ${refTarget}`, `${summary}\n${preview}`);
    const review = await page.locator('.ds-review').innerText();
    counts.review++;
    scan(`review ${refTarget}`, review);
    if (!review.includes(`Secret ${name}`)) {
      fails.push(`FAIL C4 review of ${refTarget} does not list Secret ${name}`);
      log(fails[fails.length - 1]);
    }
  }
  log(`C4 executions set=${opened.length} yaml=${counts.yaml} copy=${counts.copy} preview=${counts.preview} review=${counts.review}`);
  for (const [k, v] of Object.entries(counts)) {
    if (v !== opened.length || v === 0) {
      fails.push(`FAIL C4 executions ${k}=${v} set=${opened.length}`);
      log(fails[fails.length - 1]);
    }
  }
  // Every screen's DOM.
  const screens = fixedTable('web/prototype/e2e/screens.json', 'C4');
  let scanned = 0;
  for (const { id } of screens.screens) {
    if (await goScreen(page, id)) {
      scan(`${id} DOM`, (await pageTexts(page)).join('\n'));
      scanned++;
    }
  }
  log(`C4 screens scanned=${scanned}`);
  // P59: the Secret that cannot be read is never requested or shown, and is not in the build.
  const unreadable = table.rows.filter((r) => !r.readable);
  requests.length = 0;
  await goScreen(page, 'P59');
  const p59 = (await pageTexts(page)).join('\n');
  const unknownShown = p59.includes(BRIDGE_SENTENCES.find((b) => b.id === 'unknown-secrets').sentence);
  let p59Hits = 0;
  let distHits = 0;
  let objectRequests = 0;
  for (const r of unreadable) {
    objectRequests += requests.filter((u) => u.includes(`objects/${r.namespace}/Secret/${r.secret}.json`)).length;
    p59Hits += [r.canary, r.base64].filter((s) => p59.includes(s)).length;
    const walk = (dir) => {
      for (const name of readdirSync(dir)) {
        const full = path.join(dir, name);
        if (statSync(full).isDirectory()) {
          walk(full);
        } else {
          const text = readFileSync(full).toString('latin1');
          distHits += [r.canary, r.base64].filter((s) => text.includes(s)).length;
        }
      }
    };
    walk(DIST);
  }
  log(`C4 P59 unknown-shown=${unknownShown} secret-object-requests=${objectRequests} page-hits=${p59Hits} build-hits=${distHits}`);
  if (!unknownShown || objectRequests > 0 || p59Hits > 0 || distHits > 0) {
    fails.push('FAIL C4 P59 the unreadable Secret was requested, shown or built');
    log(fails[fails.length - 1]);
  }
  await context.close();
  for (const h of hits.values()) {
    log(`C4 hit place=${h.place} secret=${h.secret} key=${h.key} form=${h.form}`);
  }
  if (hits.size > 0) {
    const places = [...new Set([...hits.values()].map((h) => h.place))];
    fails.push(`FAIL C4 canary-hits=${hits.size} places=${places.join(',')}`);
    log(fails[fails.length - 1]);
  } else {
    log(`C4 canary-hits=0 detected-set=${detect.length} result=${fails.length === 0 ? 'PASS' : 'FAIL'}`);
  }
  expect(fails).toEqual([]);
});

// ---------------------------------------------------------------------------------------
// C5: the renderer measurement of 6.4 (numbers are information; a missing line, an ERROR or a
// refused input fails the case).

const input = JSON.parse(readFileSync(path.join(PROTO, 'measure', 'input.json'), 'utf8'));

async function measure(browser, query) {
  const context = await browser.newContext({ baseURL: BASE, viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  await page.goto(`/measure/index.html?${query}`);
  await page.waitForFunction(() => window.__measure && window.__measure.status !== 'running', null, { timeout: 180_000 });
  const result = await page.evaluate(() => ({ ...window.__measure }));
  return { context, page, result };
}

async function actions(page) {
  let n = 0;
  const cx = 720;
  const cy = 450;
  await page.mouse.move(cx, cy);
  for (let i = 0; i < 5; i++) {
    await page.mouse.wheel(0, i % 2 === 0 ? -240 : 240);
    n++;
    await page.waitForTimeout(80);
  }
  for (let i = 0; i < 5; i++) {
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    await page.mouse.move(cx + 120 * (i % 2 === 0 ? 1 : -1), cy + 60, { steps: 6 });
    await page.mouse.up();
    n++;
    await page.waitForTimeout(80);
  }
  return n;
}

test('C5 renderer measurement', async ({ browser }) => {
  const fails = [];
  const fail = (line) => {
    fails.push(line);
    log(line);
  };
  const gz = readFileSync(path.join(REPO, 'test', 'e2e', 'prototype', 'testdata', 'l-graph.tar.gz'));
  const sha = sha256(gz);
  log(`PROTO-MEASURE input sha256=${sha} bytes=${gz.length}`);
  if (sha !== input.sha256) {
    fail(`input sha256 mismatch expected=${input.sha256} got=${sha}`);
    expect(fails).toEqual([]);
  }
  const member = readTar(gunzipSync(gz)).get(input.perfBig.member);
  const derived = derive500(JSON.parse(Buffer.from(member).toString('utf8')));
  const bytes = Buffer.from(canonical(derived), 'utf8');
  const pods = derived.nodes.filter((n) => n.kind === 'Pod').length;
  const dsha = sha256(bytes);
  log(`PROTO-MEASURE derived pods=${pods} nodes=${derived.nodes.length} bridges=${derived.bridges.length} bytes=${bytes.length} sha256=${dsha}`);
  if (dsha !== input.derived.sha256 || pods !== input.derived.pods || derived.nodes.length !== input.derived.nodes || derived.bridges.length !== input.derived.bridges) {
    fail('FAIL C5 renderer measurement: the derived 500-Pod input differs from 6.4');
  }
  const renderNs = {};
  for (const r of ['R1', 'R2']) {
    const toFar = [];
    const render = [];
    for (let i = 0; i < 5; i++) {
      const { context, result } = await measure(browser, `r=${r}&view=far`);
      await context.close();
      if (result.status !== 'done' || result.sha256 !== input.sha256) {
        fail(`ERROR C5 renderer=${r} far run=${i + 1} status=${result.status} ${result.error ?? result.line ?? ''}`);
        continue;
      }
      toFar.push(Math.round(result.pageToFar));
      render.push(Math.round(result.renderFar));
    }
    log(`PROTO-MEASURE renderer=${r} page-to-far runs=${toFar.length} max=${Math.max(...toFar)} values=${toFar.join(',')}`);
    log(`PROTO-MEASURE renderer=${r} render-far runs=${render.length} max=${Math.max(...render)} values=${render.join(',')}`);
    for (const [view, query] of [['far', 'view=far'], ['perf-big', 'view=ns&input=perf-big'], ['pods500', 'view=ns&input=pods500']]) {
      const { context, page, result } = await measure(browser, `r=${r}&${query}`);
      if (result.status !== 'done') {
        fail(`ERROR C5 renderer=${r} view=${view} status=${result.status} ${result.error ?? result.line ?? ''}`);
        await context.close();
        continue;
      }
      const t0 = await page.evaluate(() => window.__now());
      const n = await actions(page);
      const lt = await page.evaluate((since) => window.__longtasks(since), t0);
      const t1 = await page.evaluate(() => window.__now());
      await page.evaluate(() => window.__busy(300));
      const control = await page.evaluate((since) => window.__longtasks(since), t1);
      await context.close();
      log(`PROTO-MEASURE renderer=${r} longtask supported=${result.supported ? 'yes' : 'no'} view=${view} actions=${n} over200=${lt.over200} longest=${lt.longest} control over200=${control.over200}`);
      if (!result.supported || n !== 10 || control.over200 < 1) {
        fail(`ERROR C5 renderer=${r} view=${view} longtask observer check failed`);
      }
    }
    for (const view of ['pods500', 'perf-big']) {
      for (const cap of [25, 50, 100, 200]) {
        const { context, page, result } = await measure(browser, `r=${r}&view=ns&input=${view}&cap=${cap}`);
        if (result.status !== 'done') {
          fail(`ERROR C5 renderer=${r} render-ns view=${view} cap=${cap} status=${result.status} ${result.error ?? ''}`);
          await context.close();
          continue;
        }
        const lt = await page.evaluate(([a, b]) => window.__longtasks(a, b), [result.parsed, result.drawn]);
        await context.close();
        const ms = Math.round(result.renderFar);
        renderNs[`${r}|${view}|${cap}`] = { ms, over200: lt.over200 };
        log(`PROTO-MEASURE renderer=${r} render-ns view=${view} cap=${cap} ms=${ms} over200=${lt.over200} nodes=${result.nodes} links=${result.edges}`);
      }
    }
  }
  const fit = [25, 50, 100, 200].filter((c) => {
    const m = renderNs[`R1|pods500|${c}`];
    return m && m.ms <= 1500 && m.over200 === 0;
  });
  log(`PROTO-MEASURE threshold proposal=${fit.length ? Math.max(...fit) : 'none'} renderer=R1`);
  expect(fails).toEqual([]);
});

test('C5 input mismatch refused', async ({ browser }) => {
  const context = await browser.newContext({ baseURL: BASE });
  const gz = readFileSync(path.join(REPO, 'test', 'e2e', 'prototype', 'testdata', 'l-graph.tar.gz'));
  const changed = Buffer.from(gz);
  changed[1000] ^= 0x01;
  await context.route('**/measure/l-graph.bin', (route) => route.fulfill({ status: 200, contentType: 'application/gzip', body: changed }));
  const page = await context.newPage();
  await page.goto('/measure/index.html?r=R1&view=far');
  await page.waitForFunction(() => window.__measure && window.__measure.status !== 'running', null, { timeout: 60_000 });
  const result = await page.evaluate(() => ({ ...window.__measure }));
  await context.close();
  log(result.line ?? `C5 mismatch case status=${result.status}`);
  const measured = result.parsed !== undefined || result.drawn !== undefined;
  log(`C5 input mismatch refused status=${result.status} exit=${result.exit} measured=${measured ? 'yes' : 'no'} changed-sha256=${sha256(changed)}`);
  expect(result.status).toBe('refused');
  expect(result.exit).toBe(1);
  expect(result.line).toBe(`input sha256 mismatch expected=${input.sha256} got=${sha256(changed)}`);
  expect(measured).toBe(false);
});

test('C5 comparison scenes', async ({ browser }) => {
  const fails = [];
  for (const r of ['R1', 'R2']) {
    for (const scene of ['S-A', 'S-B', 'S-C', 'S-D', 'S-E']) {
      const context = await browser.newContext({ baseURL: BASE, viewport: { width: 1440, height: 900 } });
      const page = await context.newPage();
      await page.goto(`/measure/compare.html?r=${r}&scene=${scene}`);
      try {
        await page.waitForFunction(() => window.__scene && window.__scene.status === 'done', null, { timeout: 120_000 });
      } catch {
        fails.push(`ERROR C5 scene renderer=${r} scene=${scene} not drawn`);
        log(fails[fails.length - 1]);
        await context.close();
        continue;
      }
      const facts = await page.evaluate(() => window.__scene.facts);
      const capture = writeOut(`scene-${r}-${scene}.png`, await page.screenshot());
      const snapshot = writeOut(`scene-${r}-${scene}.aria.yml`, await page.locator('#scene').ariaSnapshot());
      const stops = [];
      await page.locator('body').click({ position: { x: 2, y: 2 } });
      for (let i = 0; i < 40; i++) {
        await page.keyboard.press('Tab');
        const d = await page.evaluate(() => {
          const el = document.activeElement;
          if (!el || el === document.body) {
            return 'body';
          }
          return el.getAttribute('data-target') || el.getAttribute('aria-label') || el.tagName.toLowerCase();
        });
        if (stops.length > 0 && d === stops[0]) {
          break;
        }
        stops.push(d);
      }
      const keys = writeOut(`scene-${r}-${scene}.keys.txt`, `${stops.join('\n')}\n`);
      await context.close();
      log(`PROTO-SCENE renderer=${r} scene=${scene} capture=${capture} snapshot=${snapshot} keys=${keys} tab-stops=${stops.filter((s) => s !== 'body').length} facts=${JSON.stringify(facts)}`);
    }
  }
  expect(fails).toEqual([]);
});

// ---------------------------------------------------------------------------------------
// C6 E: the keyboard path Cluster -> Namespace -> Workload -> Details -> Ask why -> Send, and
// the six link states and four node states in grayscale.

async function tabTo(page, selector, limit = 250) {
  for (let i = 0; i < limit; i++) {
    await page.keyboard.press('Tab');
    if (await page.evaluate((sel) => document.activeElement && document.activeElement.matches(sel), selector)) {
      return i + 1;
    }
  }
  throw new Error(`focus never reached ${selector}`);
}

test('C6 keyboard path', async ({ page }) => {
  await openApp(page);
  await waitScreen(page, 'P01');
  const steps = [
    ['cluster', '[data-control="tile"][data-target="shop"]', 'Enter', '.proto[data-level="namespace"][data-ready="1"]'],
    ['namespace', '[data-control="card"][data-target="shop/Deployment/cart"]', 'Shift+Enter', '.proto[data-level="workload"][data-ready="1"]'],
    ['workload', '[data-control="card"][data-target="shop/Deployment/cart"]', 'Enter', '.proto-inspector .ds-inspector[data-kind="Deployment"]'],
    ['details', '.proto-inspector [data-control="ask-why"]', 'Enter', '.proto-inspector [data-control="send"]'],
    ['ask-why', '.proto-inspector [data-control="send"]', 'Enter', '.proto-answer'],
  ];
  let n = 0;
  for (const [name, target, key, after] of steps) {
    const presses = await tabTo(page, target);
    const outline = await page.evaluate(() => getComputedStyle(document.activeElement).outlineStyle);
    n++;
    const capture = writeOut(`C6-${n}-${name}.png`, await page.screenshot());
    log(`C6 step=${n} ${name} focus=${target} tabs=${presses} outline=${outline} capture=${capture}`);
    expect(outline).not.toBe('none');
    await page.keyboard.press(key);
    await page.locator(after).first().waitFor({ timeout: 20_000 });
  }
  n++;
  const capture = writeOut(`C6-${n}-send.png`, await page.screenshot());
  log(`C6 step=${n} send answer shown capture=${capture}`);
  log(`C6 keyboard path steps=${n} result=PASS`);
});

test('C6 grayscale', async ({ page }) => {
  const links = [];
  for (const id of ['P10', 'P11', 'P12', 'P13', 'P14', 'P15']) {
    await goScreen(page, id);
    await page.addStyleTag({ content: 'html { filter: grayscale(1); }' });
    const capture = writeOut(`C6-gray-${id}.png`, await page.screenshot());
    const link = await page.evaluate(() => {
      const g = document.querySelector('.proto-link--selected');
      const line = g ? g.querySelector('.ds-link__line') : null;
      const pill = document.querySelector('.proto-link-details .ds-pill');
      return {
        state: g ? g.getAttribute('class').match(/ds-link--(\w+)/)[1] : 'none',
        dash: line ? line.getAttribute('stroke-dasharray') ?? 'solid' : 'none',
        width: line ? line.getAttribute('stroke-width') : 'none',
        end: g ? Boolean(g.querySelector('.ds-link__end')) : false,
        glyph: pill ? pill.querySelector('[data-icon]').getAttribute('data-icon') : 'none',
      };
    });
    links.push(link);
    log(`C6 gray ${id} link=${link.state} dash=${link.dash} width=${link.width} end-mark=${link.end} glyph=${link.glyph} capture=${capture}`);
  }
  const nodes = new Map();
  for (const id of ['P04', 'P11']) {
    await goScreen(page, id);
    await page.addStyleTag({ content: 'html { filter: grayscale(1); }' });
    const capture = writeOut(`C6-gray-${id}-nodes.png`, await page.screenshot());
    const found = await page.evaluate(() => {
      const out = [];
      for (const el of document.querySelectorAll('.react-flow__node .proto-pod, .react-flow__node .ds-card')) {
        const tab = el.querySelector(':scope > .ds-status-tab');
        const state = tab ? tab.getAttribute('data-status') : 'healthy';
        const border = tab ? (tab.className.match(/ds-tab-border-(\w+)/) ?? [, 'none'])[1] : 'none';
        const glyph = tab ? tab.querySelector('[data-icon]').getAttribute('data-icon') : 'none';
        out.push({ state, border, glyph });
      }
      return out;
    });
    for (const f of found) {
      if (!nodes.has(f.state)) {
        nodes.set(f.state, f);
        log(`C6 gray ${id} node=${f.state} tab-border=${f.border} glyph=${f.glyph} capture=${capture}`);
      }
    }
  }
  const linkKeys = new Set(links.map((l) => `${l.dash}|${l.width}|${l.end}|${l.glyph}`));
  const nodeKeys = new Set([...nodes.values()].map((n) => `${n.border}|${n.glyph}`));
  const want = ['healthy', 'progressing', 'failing', 'unknown'];
  log(`C6 grayscale links distinct=${linkKeys.size}/6 nodes distinct=${nodeKeys.size}/4 node-states=${[...nodes.keys()].sort().join(',')}`);
  expect(links.map((l) => l.state)).toEqual(['connected', 'waiting', 'broken', 'warning', 'unknown', 'optional']);
  expect(linkKeys.size).toBe(6);
  expect(want.every((s) => nodes.has(s))).toBe(true);
  expect(nodeKeys.size).toBe(4);
});
