// Shared parts of the prototype cases (packet FA2-T8 6.5): the fixed tables, the output
// directory (INTUITIK_PROTO_OUT, packed by the job), the output log and the screen helpers.
import { createHash } from 'node:crypto';
import { appendFileSync, existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const E2E = fileURLToPath(new URL('.', import.meta.url));
export const PROTO = path.join(E2E, '..');
export const DIST = path.join(PROTO, 'dist');
export const REPO = path.join(PROTO, '..', '..');
export const OUT = process.env.INTUITIK_PROTO_OUT || path.join(tmpdir(), 'intuitik-prototype-out');
mkdirSync(OUT, { recursive: true });

// The delivery's value of web/prototype/e2e/fixed.sha256 (FA2-T8 delivery 0b3c3842).
export const FIXED_LIST_SHA256 = 'f279fc6a1758218a6df044e52564e62876b06cc2259166a14a035db3dcd50a05';

export const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');

export function readRepo(rel) {
  return readFileSync(path.join(REPO, rel));
}

// fixedList returns the constants of fixed.sha256 as { path: sha256 }.
export function fixedList() {
  const out = {};
  for (const line of readRepo('web/prototype/e2e/fixed.sha256').toString('utf8').split('\n')) {
    const m = line.match(/^([0-9a-f]{64}) {2}(\S+)$/);
    if (m) {
      out[m[2]] = m[1];
    }
  }
  return out;
}

// fixedTable reads one fixed table after checking its sha256 against fixed.sha256 and the
// list's own sha256 against the delivery's value; it returns null and logs FAIL otherwise.
export function fixedTable(rel, tag) {
  const listSha = sha256(readRepo('web/prototype/e2e/fixed.sha256'));
  const bytes = readRepo(rel);
  const got = sha256(bytes);
  const want = fixedList()[rel];
  log(`${tag} table ${rel} sha256=${got} expected=${want} list=${listSha}`);
  if (listSha !== FIXED_LIST_SHA256 || got !== want) {
    log(`FAIL ${tag} table ${rel} differs from the fixed value`);
    return null;
  }
  return JSON.parse(bytes.toString('utf8'));
}

export function log(line) {
  console.log(line);
  appendFileSync(path.join(OUT, 'prototype-output.txt'), `${line}\n`);
}

export function writeOut(name, content) {
  const file = path.join(OUT, name);
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, content);
  return name;
}

// distFile says whether a request path is a file of the prototype build.
export function distFile(pathname) {
  const rel = decodeURIComponent(pathname).replace(/^\/+/, '') || 'index.html';
  const file = path.join(DIST, rel);
  return file.startsWith(DIST) && existsSync(file) && statSync(file).isFile();
}

export async function openApp(page) {
  await page.goto('/index.html');
  await page.locator('.proto[data-ready="1"]').waitFor({ timeout: 30_000 });
}

export async function hasScreen(page, id) {
  return (await page.locator(`[data-control="strip-${id}"]`).count()) > 0;
}

export async function waitScreen(page, id, timeout = 20_000) {
  await page.locator(`.proto[data-screen="${id}"][data-ready="1"]`).waitFor({ timeout });
}

export async function screenNow(page) {
  return page.locator('.proto').getAttribute('data-screen');
}

// goScreen opens the app and shows a screen through the strip; false when the prototype has
// no such screen.
export async function goScreen(page, id) {
  const frame = { P60: 1024, P61: 720, P62: 1024, P63: 720, P64: 1024, P65: 720 }[id];
  await page.setViewportSize({ width: frame ?? 1440, height: 900 });
  await openApp(page);
  if (!(await hasScreen(page, id))) {
    return false;
  }
  await page.locator(`[data-control="strip-${id}"]`).click();
  await waitScreen(page, id);
  return true;
}

export async function control(page, key, target, scope = '') {
  const sel = `${scope ? `${scope} ` : ''}[data-control="${key}"]${target ? `[data-target="${target}"]` : ''}`;
  return page.locator(sel).first();
}

// textNodes returns the text of every text node of the document and of every form field.
export async function pageTexts(page) {
  return page.evaluate(() => {
    const out = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) {
      out.push(walker.currentNode.data);
    }
    for (const el of document.querySelectorAll('input, textarea')) {
      out.push(el.value);
    }
    for (const el of document.querySelectorAll('*')) {
      if (el.children.length > 0 && el.textContent) {
        out.push(el.textContent);
      }
    }
    return out;
  });
}

// clickCard clicks a map card on its header, never on one of its reference rows (a row
// selects the link, the header selects the resource).
// The point is the header's centre height (22 of its 44 px) at the map's current zoom, taken
// from the card's drawn width against its 272 px.
export async function clickCard(page, target) {
  const card = page.locator(`[data-control="card"][data-target="${target}"]`);
  const box = await card.boundingBox();
  const zoom = box ? box.width / 272 : 1;
  await card.click({ position: { x: Math.max(4, 60 * zoom), y: Math.max(2, 22 * zoom) } });
}
