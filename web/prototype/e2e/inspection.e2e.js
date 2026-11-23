// The inspection cases K1-K4 of the FA2-T8 corrective decision (hoan 67a05f5d): accessible
// names, clipped labels, Inspector overflow and the Inspector state. Each case visits all 65
// screens after the load-complete wait of C3 and document.fonts.ready, collects every failure
// before it asserts, prints one FAIL line per failure and one summary line.
import { expect, test } from '@playwright/test';
import { fixedTable, goScreen, log, readRepo } from './helpers.js';

const ROLE_TARGETS = [
  'button',
  'a[href]',
  'input:not([type="hidden"])',
  'select',
  'textarea',
  '[role="button"]',
  '[role="link"]',
  '[role="textbox"]',
  '[role="checkbox"]',
  '[role="radio"]',
  '[role="tab"]',
  '[role="menuitem"]',
  '[role="combobox"]',
  '[role="switch"]',
  '[data-control]',
].join(', ');

const TOLERANCE = 1;

function screenIds() {
  const table = fixedTable('web/prototype/e2e/screens.json', 'K');
  expect(table, 'screens.json equals the fixed table').not.toBeNull();
  return table.screens.map((s) => s.id);
}

// visit shows a screen and waits for the load-complete mark of C3 and for the fonts.
async function visit(page, id) {
  if (!(await goScreen(page, id))) {
    return false;
  }
  await page.evaluate(() => document.fonts.ready.then(() => true));
  return true;
}

// targets marks every visible K1 target of the screen with data-k-id and returns, per
// target, its stable name (data-control key, data-target and the index among equal keys or
// the tag and role) and whether it is a form field, a named-by-attribute control and its
// rendered text.
async function targets(page) {
  return page.evaluate((sel) => {
    const seen = new Map();
    const out = [];
    let n = 0;
    for (const el of document.querySelectorAll(sel)) {
      el.removeAttribute('data-k-id');
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      if (r.width === 0 || r.height === 0 || cs.visibility === 'hidden' || cs.display === 'none') {
        continue;
      }
      const key = el.getAttribute('data-control');
      const base = key
        ? `[data-control="${key}"]${el.getAttribute('data-target') ? `[data-target="${el.getAttribute('data-target')}"]` : ''}`
        : `${el.tagName.toLowerCase()}${el.getAttribute('role') ? `[role="${el.getAttribute('role')}"]` : ''}`;
      const i = seen.get(base) ?? 0;
      seen.set(base, i + 1);
      el.setAttribute('data-k-id', String(n));
      const tag = el.tagName.toLowerCase();
      out.push({
        k: n,
        name: `${base}#${i}`,
        field: tag === 'input' || tag === 'select' || tag === 'textarea',
        labelled: el.hasAttribute('aria-label') || el.hasAttribute('aria-labelledby'),
        text: (el.innerText ?? '').replace(/\s+/g, ' ').trim(),
      });
      n++;
    }
    return out;
  }, ROLE_TARGETS);
}

// accessibleName reads the name Playwright computes for the element (its ARIA snapshot head).
async function accessibleName(page, k) {
  // Playwright 1.64.1 (playwright-core/lib/coreBundle.js) writes a head line as "- " + key, then
  // nothing, ":" (children) or ": " + value (renderAriaSnapshotAsYaml, L789-L800). The key is the
  // role, then " " + JSON.stringify(name) when the name is not empty, then the attributes of
  // createKey (L741-L781). yamlEscapeKeyIfNeeded (L670-L674) single-quotes a key that needs
  // quotes (yamlStringNeedsQuotes, L699-L723; ": " is one reason) and doubles each inner "'";
  // a key is never double-quoted. The page runs the same code (injected source, L20944). Any
  // other head is a test tool error.
  const el = page.locator(`[data-k-id="${k}"]`);
  const snap = await el.ariaSnapshot();
  const head = snap.split('\n')[0];
  const broken = async () => {
    const screen = await page.locator('.proto').getAttribute('data-screen');
    const target = await el.evaluate((e) => `${e.tagName.toLowerCase()}[data-control="${e.getAttribute('data-control')}"][data-target="${e.getAttribute('data-target')}"]`);
    return new Error(`K1 snapshot head not parsed screen=${screen} target=${target} head=${JSON.stringify(head)}`);
  };
  if (!head.startsWith('- ')) {
    throw await broken();
  }
  let rest = head.slice(2);
  let key;
  if (rest.startsWith("'")) {
    const quoted = rest.match(/^'((?:[^']|'')*)'(?=$|:)/);
    if (!quoted) {
      throw await broken();
    }
    key = quoted[1].replace(/''/g, "'");
    rest = rest.slice(quoted[0].length);
  } else if (rest.startsWith('"')) {
    throw await broken();
  } else {
    const end = rest.search(/:( |$)/);
    key = end < 0 ? rest : rest.slice(0, end);
    rest = end < 0 ? '' : rest.slice(end);
  }
  if (rest !== '' && rest !== ':' && !rest.startsWith(': ')) {
    throw await broken();
  }
  const attribute = String.raw`(?: \[(?:checked=mixed|checked|disabled|expanded|active|invalid=grammar|invalid=spelling|invalid|level=\d+|pressed=mixed|pressed|selected|aria-hidden|ref=[^\]\s]+|cursor=pointer|box=-?[\d.]+,-?[\d.]+,-?[\d.]+,-?[\d.]+)\])*`;
  const parts = key.match(new RegExp(String.raw`^[a-z]+(?: ("(?:[^"\\]|\\.)*"))?${attribute}$`));
  if (!parts) {
    throw await broken();
  }
  if (parts[1] === undefined) {
    return '';
  }
  let name;
  try {
    name = JSON.parse(parts[1]);
  } catch {
    throw await broken();
  }
  return name.replace(/\s+/g, ' ').trim();
}

function summary(k, visited, checked, fails) {
  log(`${k} screens=${visited} elements=${checked} failures=${fails.length} result=${fails.length === 0 ? 'PASS' : 'FAIL'}`);
}

function fail(fails, line) {
  fails.push(line);
  log(line);
}

// K1: every visible control has a non-empty accessible name; a control named by its content
// shows that same text, and a control named by an attribute still shows a visible label.
test('K1 accessible names', async ({ page }) => {
  const fails = [];
  let visited = 0;
  let checked = 0;
  for (const id of screenIds()) {
    if (!(await visit(page, id))) {
      fail(fails, `FAIL K1 screen=${id} target=screen expected=visited actual=not-visited`);
      continue;
    }
    visited++;
    const list = await targets(page);
    if (list.length === 0) {
      fail(fails, `FAIL K1 screen=${id} target=screen expected=elements>0 actual=0`);
    }
    for (const t of list) {
      checked++;
      const name = await accessibleName(page, t.k);
      if (name === '') {
        fail(fails, `FAIL K1 screen=${id} target=${t.name} expected=accessible-name actual=empty`);
        continue;
      }
      if (t.field) {
        continue;
      }
      if (t.text === '') {
        fail(fails, `FAIL K1 screen=${id} target=${t.name} expected=visible-label actual=empty`);
      } else if (!t.labelled && t.text !== name) {
        fail(fails, `FAIL K1 screen=${id} target=${t.name} expected=visible-text=accessible-name actual=differs`);
      }
    }
  }
  summary('K1', visited, checked, fails);
  expect(fails).toEqual([]);
});

// The N1 card ports (exclusion 1 of hoan 26d8369d): aria-hidden decoration without text.
const N1_PORTS = '.ds-port[aria-hidden="true"]';

// compactValues derives, from the raw namespace fixtures only, the card row values that a
// compact-card value may show: the rows of one top-level resource and one bridge kind join
// the distinct settings of their bridges, the first without its head word, with " \u00b7 "
// (UI.md 4.5). It returns, per fixture, the groups (top-level uid and kind) with the first
// parts and the full settings strings of their bridges.
function compactValues() {
  const groups = [];
  for (const ns of ['batch', 'db', 'default', 'kube-system', 'local-path-storage', 'media', 'payments', 'shop']) {
    const d = JSON.parse(readRepo(`web/prototype/data/ns-${ns}.json`).toString('utf8'));
    const nodes = new Map(d.nodes.map((n) => [n.uid, n]));
    const top = (uid) => {
      let n = nodes.get(uid);
      while (n && n.parent && nodes.has(n.parent)) {
        n = nodes.get(n.parent);
      }
      return n ? n.uid : uid;
    };
    const byKey = new Map();
    for (const b of d.bridges) {
      const key = `${ns}|${top(b.from.uid)}|${b.kind}`;
      if (!byKey.has(key)) {
        byKey.set(key, { key, heads: [], settings: [] });
      }
      const g = byKey.get(key);
      for (const s of b.settings) {
        const i = s.indexOf(' ');
        g.heads.push(i < 0 ? '' : s.slice(i + 1).trim());
        g.settings.push(s);
      }
    }
    groups.push(...byKey.values());
  }
  return groups;
}

// fromFixture is true when a value shown in a compact card is a row value of one fixture
// group: its first part is a settings string without its head word and every further part is
// a whole settings string of the same group, with no part repeated.
function fromFixture(groups, value) {
  const parts = value.split(' \u00b7 ');
  if (new Set(parts).size !== parts.length) {
    return false;
  }
  return groups.some((g) => g.heads.includes(parts[0]) && parts.slice(1).every((p) => g.settings.includes(p)));
}

// K2 (hoan 26d8369d): every shown text node inside a K1 control lies inside the clip box of
// each ancestor that clips it. Horizontal: any ancestor whose computed overflow-x is not
// visible, and the viewport, clip. Vertical: overflow-y hidden or clip clips; an ancestor that
// scrolls vertically (and the page) is scrolled so the text is in view, and then the whole
// rectangle must lie inside it. For text in a map card the map canvas and the viewport are
// not boundaries (panning). Clip boxes are the padding boxes minus scrollbars; only ancestors
// of the containing-block chain clip. Range rectangles do not prove visible pixels: clip-path,
// mask and line-clamp are printed as unjudged.
test('K2 clipping', async ({ page }) => {
  const groups = compactValues();
  const fails = [];
  let visited = 0;
  let checked = 0;
  let excluded = 0;
  let unjudged = 0;
  for (const id of screenIds()) {
    if (!(await visit(page, id))) {
      fail(fails, `FAIL K2 screen=${id} target=screen expected=visited actual=not-visited`);
      continue;
    }
    visited++;
    const list = await targets(page);
    if (list.length === 0) {
      fail(fails, `FAIL K2 screen=${id} target=screen expected=elements>0 actual=0`);
    }
    const found = await page.evaluate(
      ({ tol, ports }) => {
        const describe = (el) => {
          if (!el) {
            return 'viewport';
          }
          const key = el.getAttribute('data-control');
          const cls = el.classList.length ? `.${Array.from(el.classList).join('.')}` : '';
          return `${el.tagName.toLowerCase()}${cls}${key ? `[data-control="${key}"]` : ''}`;
        };
        const rendered = (el) => {
          if (getComputedStyle(el).visibility !== 'visible') {
            return false;
          }
          for (let a = el; a; a = a.parentElement) {
            const cs = getComputedStyle(a);
            if (cs.display === 'none' || Number(cs.opacity) === 0) {
              return false;
            }
          }
          return true;
        };
        // A box that starts a containing block for absolute or fixed descendants.
        const contains = (cs, fixed) =>
          (!fixed && cs.position !== 'static') || cs.transform !== 'none' || cs.filter !== 'none' || cs.perspective !== 'none' || /paint|layout|strict|content/.test(cs.contain);
        // clippers lists el (the element that holds the text) and the ancestors of its
        // containing-block chain with their computed overflow, innermost first.
        const clippers = (el) => {
          const own = getComputedStyle(el);
          const out = own.overflowX !== 'visible' || own.overflowY !== 'visible' ? [{ el, x: own.overflowX, y: own.overflowY }] : [];
          let mode = own.position;
          for (let a = el.parentElement; a; a = a.parentElement) {
            const cs = getComputedStyle(a);
            const inChain = mode === 'absolute' ? contains(cs, false) : mode === 'fixed' ? contains(cs, true) : true;
            if (inChain) {
              if (cs.overflowX !== 'visible' || cs.overflowY !== 'visible') {
                out.push({ el: a, x: cs.overflowX, y: cs.overflowY });
              }
              mode = cs.position === 'absolute' || cs.position === 'fixed' ? cs.position : 'static';
            }
          }
          return out;
        };
        // clipBox is the padding box minus scrollbars in viewport pixels; client sizes are in
        // the element's own pixels, so they are scaled as the element is (the map zoom).
        const clipBox = (el) => {
          const b = el.getBoundingClientRect();
          const sx = el.offsetWidth > 0 ? b.width / el.offsetWidth : 1;
          const sy = el.offsetHeight > 0 ? b.height / el.offsetHeight : 1;
          const left = b.left + el.clientLeft * sx;
          const top = b.top + el.clientTop * sy;
          return { left, top, right: left + el.clientWidth * sx, bottom: top + el.clientHeight * sy };
        };
        const viewBox = () => ({ left: 0, top: 0, right: document.documentElement.clientWidth, bottom: document.documentElement.clientHeight });
        const rects = (node) => {
          const range = document.createRange();
          range.selectNodeContents(node);
          return Array.from(range.getClientRects());
        };
        const overX = (rs, b) => Math.max(0, ...rs.filter((r) => r.width > 0).map((r) => Math.max(b.left - r.left, r.right - b.right)));
        const overY = (rs, b) => Math.max(0, ...rs.filter((r) => r.height > 0).map((r) => Math.max(b.top - r.top, r.bottom - b.bottom)));
        // scrollInto scrolls a vertical scroller (null = the page) so the rectangles start
        // inside its clip box, and returns how to restore it.
        const scrollInto = (el, rs) => {
          const box = el ? clipBox(el) : viewBox();
          const top = Math.min(...rs.map((r) => r.top));
          const bottom = Math.max(...rs.map((r) => r.bottom));
          const delta = top < box.top ? top - box.top : bottom > box.bottom ? Math.min(bottom - box.bottom, top - box.top) : 0;
          const scroller = el ?? document.scrollingElement;
          const before = scroller.scrollTop;
          if (delta !== 0) {
            scroller.scrollTop = before + delta;
          }
          return () => {
            scroller.scrollTop = before;
          };
        };

        const out = [];
        const excl = [];
        const unj = new Map();
        let count = 0;
        const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
        for (let node = walker.nextNode(); node; node = walker.nextNode()) {
          const holder = node.parentElement;
          const control = holder ? holder.closest('[data-k-id]') : null;
          if (!control || node.data.trim() === '' || !rendered(holder)) {
            continue;
          }
          const k = control.getAttribute('data-k-id');
          const text = node.data.replace(/\s+/g, ' ').trim();
          if (holder.closest(ports)) {
            excl.push({ k, selector: describe(holder.closest(ports)), reason: 'n1-port' });
            continue;
          }
          if (holder.closest('select, option, textarea')) {
            excl.push({ k, selector: describe(holder.closest('select, option, textarea')), reason: 'form-value' });
            continue;
          }
          count++;
          for (let a = holder; a; a = a.parentElement) {
            const cs = getComputedStyle(a);
            for (const [prop, value] of [['clip-path', cs.clipPath], ['mask', cs.maskImage || cs.webkitMaskImage], ['-webkit-line-clamp', cs.webkitLineClamp]]) {
              if (value && value !== 'none') {
                unj.set(`${describe(a)} ${prop}`, { k, selector: describe(a), prop });
              }
            }
          }
          const card = holder.closest('.react-flow__node');
          const canvas = card ? card.closest('.proto-map__canvas') : null;
          const compactValue = holder.closest('.ds-card[data-card-mode="compact"] .ds-refrow__value');
          let rs = rects(node);
          if (rs.length === 0) {
            out.push({ k, text, boundary: 'none', axis: 'xy', px: 0, problem: 'no-rect' });
            continue;
          }
          const list = clippers(holder).filter((c) => c.el !== canvas);
          const restore = [];
          for (const c of list) {
            if (c.y === 'auto' || c.y === 'scroll') {
              restore.push(scrollInto(c.el, rs));
              rs = rects(node);
            }
          }
          if (!card) {
            restore.push(scrollInto(null, rs));
            rs = rects(node);
          }
          const worst = new Map();
          const note = (boundary, axis, px) => {
            const key = `${boundary}|${axis}`;
            if (px > tol && px > (worst.get(key)?.px ?? 0)) {
              worst.set(key, { boundary, axis, px });
            }
          };
          for (const c of list) {
            const b = clipBox(c.el);
            const x = overX(rs, b);
            if (c.x !== 'visible' && x > tol) {
              if (c.el === compactValue && getComputedStyle(c.el).textOverflow === 'ellipsis') {
                const title = c.el.getAttribute('title') ?? '';
                const full = c.el.textContent.replace(/\s+/g, ' ').trim();
                excl.push({ k, selector: describe(c.el), reason: 'n3-own-ellipsis', value: full, title, hidden: Boolean(c.el.closest('[aria-hidden="true"]')) });
              } else {
                note(describe(c.el), 'x', x);
              }
            }
            if (c.y !== 'visible') {
              note(describe(c.el), 'y', overY(rs, b));
            }
          }
          if (!card) {
            const v = viewBox();
            note('viewport', 'x', overX(rs, v));
            note('viewport', 'y', overY(rs, v));
          }
          for (const r of restore.reverse()) {
            r();
          }
          for (const w of worst.values()) {
            out.push({ k, text, boundary: w.boundary, axis: w.axis, px: Math.round(w.px), problem: 'clipped' });
          }
        }
        const shownValue = /^(text|search|email|url|tel|password|number)$/;
        for (const el of document.querySelectorAll('[data-k-id] input, input[data-k-id], [data-k-id] textarea, textarea[data-k-id]')) {
          if (el.tagName === 'INPUT' && !shownValue.test(el.type)) {
            continue;
          }
          if (rendered(el) && (el.value !== '' || (el.getAttribute('placeholder') ?? '') !== '')) {
            const control = el.closest('[data-k-id]');
            excl.push({ k: control.getAttribute('data-k-id'), selector: describe(el), reason: 'form-value' });
          }
        }
        const portText = Array.from(document.querySelectorAll(ports)).reduce((n, el) => n + el.textContent.length, 0);
        return { out, excl, unj: Array.from(unj.values()), count, ports: document.querySelectorAll(ports).length, portText };
      },
      { tol: TOLERANCE, ports: N1_PORTS },
    );
    checked += found.count;
    const names = new Map(list.map((t) => [String(t.k), t.name]));
    log(`K2 ports screen=${id} selector=${N1_PORTS} elements=${found.ports} textchars=${found.portText}`);
    const once = new Set();
    for (const f of found.out) {
      const line =
        f.problem === 'no-rect'
          ? `FAIL K2 screen=${id} target=${names.get(f.k)} text=${JSON.stringify(f.text)} expected=client-rects actual=no-rect`
          : `FAIL K2 screen=${id} target=${names.get(f.k)} text=${JSON.stringify(f.text)} boundary=${f.boundary} axis=${f.axis} expected=not-clipped actual=overflow`;
      if (!once.has(line)) {
        once.add(line);
        fail(fails, line);
        if (f.problem !== 'no-rect') {
          log(`K2 overflow screen=${id} target=${names.get(f.k)} boundary=${f.boundary} axis=${f.axis} px=${f.px}`);
        }
      }
    }
    for (const e of found.excl) {
      if (e.reason === 'n3-own-ellipsis' && (e.title !== e.value || e.hidden || !fromFixture(groups, e.value))) {
        const line = `FAIL K2 screen=${id} target=${names.get(e.k)} text=${JSON.stringify(e.value)} expected=fixture-value-exposed actual=title:${JSON.stringify(e.title)} hidden:${e.hidden} fixture:${fromFixture(groups, e.value)}`;
        if (!once.has(line)) {
          once.add(line);
          fail(fails, line);
        }
        continue;
      }
      const line = `K2 excluded ${id} ${e.selector} ${e.reason}`;
      excluded++;
      log(e.value === undefined ? `${line} target=${names.get(e.k)}` : `${line} target=${names.get(e.k)} value=${JSON.stringify(e.value)}`);
    }
    for (const u of found.unj) {
      unjudged++;
      log(`K2 unjudged ${id} ${u.selector} ${u.prop}`);
    }
  }
  log(`K2 screens=${visited} targets=${checked} failures=${fails.length} excluded=${excluded} unjudged=${unjudged} result=${fails.length === 0 ? 'PASS' : 'FAIL'}`);
  expect(fails).toEqual([]);
});

// K3: the open Inspector is not wider than its box and no visible content passes its right
// edge, except content inside a scroll container that lies inside the panel.
test('K3 Inspector overflow', async ({ page }) => {
  const fails = [];
  let visited = 0;
  let checked = 0;
  for (const id of screenIds()) {
    if (!(await visit(page, id))) {
      fail(fails, `FAIL K3 screen=${id} target=screen expected=visited actual=not-visited`);
      continue;
    }
    visited++;
    const found = await page.evaluate((tol) => {
      const panel = document.querySelector('.proto-inspector');
      if (!panel) {
        return null;
      }
      const p = panel.getBoundingClientRect();
      const ps = getComputedStyle(panel);
      if (p.width === 0 || p.height === 0 || ps.display === 'none' || ps.visibility === 'hidden') {
        return null;
      }
      const out = [];
      if (panel.scrollWidth > panel.clientWidth + tol) {
        out.push({ target: 'panel', px: panel.scrollWidth - panel.clientWidth });
      }
      const scrolls = (el) => {
        for (let a = el.parentElement; a && a !== panel; a = a.parentElement) {
          const ox = getComputedStyle(a).overflowX;
          if (ox === 'auto' || ox === 'scroll') {
            return a.getBoundingClientRect().right <= p.right + tol;
          }
        }
        return false;
      };
      const seen = new Map();
      let elements = 0;
      for (const el of panel.querySelectorAll('*')) {
        const r = el.getBoundingClientRect();
        const cs = getComputedStyle(el);
        if (r.width === 0 || r.height === 0 || cs.visibility === 'hidden') {
          continue;
        }
        elements++;
        if (r.right > p.right + tol && !scrolls(el)) {
          const base = el.getAttribute('data-control') ? `[data-control="${el.getAttribute('data-control')}"]` : `${el.tagName.toLowerCase()}${el.classList.length ? `.${Array.from(el.classList).join('.')}` : ''}`;
          const i = seen.get(base) ?? 0;
          seen.set(base, i + 1);
          out.push({ target: `${base}#${i}`, px: Math.round(r.right - p.right) });
        }
      }
      return { out, elements };
    }, TOLERANCE);
    if (found === null) {
      log(`K3 screen=${id} inspector=closed`);
      continue;
    }
    if (found.elements === 0) {
      fail(fails, `FAIL K3 screen=${id} target=panel expected=elements>0 actual=0`);
    }
    checked += found.elements;
    for (const f of found.out) {
      fail(fails, `FAIL K3 screen=${id} target=${f.target} expected=inside-panel actual=overflow`);
      log(`K3 overflow screen=${id} target=${f.target} px=${f.px}`);
    }
  }
  summary('K3', visited, checked, fails);
  expect(fails).toEqual([]);
});

// K4: the Inspector shows the state and the issue count that inspector-states.json derives
// from the raw fixtures and the propagation rules of UI.md 4.3.
test('K4 Inspector state', async ({ page }) => {
  const expected = JSON.parse(readRepo('web/prototype/e2e/inspector-states.json').toString('utf8'));
  const rows = new Map(expected.screens.map((r) => [r.screen, r]));
  const fails = [];
  let visited = 0;
  let checked = 0;
  for (const id of screenIds()) {
    if (!(await visit(page, id))) {
      fail(fails, `FAIL K4 screen=${id} target=screen expected=visited actual=not-visited`);
      continue;
    }
    visited++;
    const want = rows.get(id);
    if (!want) {
      continue;
    }
    const shown = await page.evaluate(() => {
      const panel = document.querySelector('.proto-inspector');
      const state = panel ? panel.querySelector('[data-inspector-state]') : null;
      const issues = panel ? panel.querySelector('[data-inspector-issues]') : null;
      return {
        state: state ? state.getAttribute('data-inspector-state') : 'absent',
        text: state ? state.innerText.replace(/\s+/g, ' ').trim() : 'absent',
        issues: issues ? issues.getAttribute('data-inspector-issues') : 'absent',
        issuesText: issues ? issues.innerText.replace(/\s+/g, ' ').trim() : 'absent',
      };
    });
    checked++;
    const target = `${want.uid}`;
    if (shown.state !== want.state) {
      fail(fails, `FAIL K4 screen=${id} target=${target} expected=state:${want.state} actual=state:${shown.state}`);
    }
    if (shown.text !== want.text) {
      fail(fails, `FAIL K4 screen=${id} target=${target} expected=text:${want.text} actual=text:${shown.text}`);
    }
    if (shown.issues !== String(want.issues) || shown.issuesText !== want.issuesText) {
      fail(fails, `FAIL K4 screen=${id} target=${target} expected=issues:${want.issues} actual=issues:${shown.issues}`);
    }
  }
  if (checked !== rows.size) {
    fail(fails, `FAIL K4 screen=all target=inspector-states.json expected=rows:${rows.size} actual=checked:${checked}`);
  }
  summary('K4', visited, checked, fails);
  expect(fails).toEqual([]);
});
