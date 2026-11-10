// T-9 gallery coverage (packet DS-1 section 6; specification sections 3 and 4).
import fs from 'node:fs';
import path from 'node:path';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import Gallery from '../gallery/Gallery.jsx';
import { GALLERY_ITEMS } from '../gallery/fixtures.js';
import { RENDERED_COMPONENTS } from '../gallery/registry.jsx';
import { DESIGN_SYSTEM_DIR } from './helpers.js';

const COMPONENTS = ['C-01', 'C-02', 'C-03', 'C-04', 'C-05', 'C-10', 'C-11', 'C-12', 'C-13', 'C-14', 'C-15', 'C-16', 'C-17', 'C-20', 'C-21', 'C-22', 'C-23', 'C-24', 'C-25', 'C-26', 'C-30', 'C-31', 'C-32', 'C-33', 'C-34', 'C-40', 'C-41', 'C-42', 'C-43'];

const LINK = ['connected', 'broken', 'waiting', 'warning', 'unknown', 'optional'];
const KINDS = ['ingress', 'service', 'deployment', 'statefulset', 'daemonset', 'cronjob', 'job', 'pod', 'configmap', 'secret', 'pvc', 'pv', 'storageclass', 'hpa', 'httproute'];

// Every state or variant of each specification section 3 row, as gallery item ids.
const REQUIRED = {
  'C-01': [...['x', 'clock', 'tri', 'lock', 'info', 'ok', 'ring'].map((s) => `status.${s}`), ...KINDS.map((k) => `kind.${k}`), 'kind.unknown'],
  'C-02': ['neutral', 'broken', 'attention', 'optional', 'unknown'].flatMap((t) => [`${t}.mono`, `${t}.sans`]),
  'C-03': [...LINK.map((s) => `link.${s}`), ...['healthy', 'progressing', 'failing', 'unknown'].map((s) => `node.${s}`)],
  'C-04': ['primary', 'secondary', 'text-link'].flatMap((v) => [v, `${v}.disabled`]),
  'C-05': ['default'],
  'C-10': ['failing', 'progressing', 'unknown', 'attention', 'failing.broken-link', 'healthy'],
  'C-11': [...LINK.map((s) => `${s}.one-line`), ...LINK.filter((s) => s !== 'connected').map((s) => `${s}.two-line`), 'broken.long-reason', 'connected.long-value'],
  'C-12': ['in', 'out'].flatMap((d) => LINK.map((s) => `${d}.${s}`)),
  'C-13': LINK,
  'C-14': ['compact.default', 'compact.folded', 'compact.long-reason', 'expanded.default', 'selected', 'unknown'],
  'C-15': ['broken', 'optional'],
  'C-16': ['anomalies', 'healthy', 'empty', 'no-permission'],
  'C-17': ['node', 'storageclass', 'pv'],
  'C-20': ['normal', 'paused'],
  'C-21': ['cluster', 'namespace', 'workload'],
  'C-22': ['groups', 'selected', 'empty'],
  'C-23': ['details.pod', 'details.service', 'diagnose'],
  'C-24': ['target', 'ghost'],
  'C-25': ['default'],
  'C-26': ['default'],
  'C-30': ['external', 'local'],
  'C-31': ['default'],
  'C-32': ['default'],
  'C-33': ['default'],
  'C-34': ['error', 'not-set-up'],
  'C-40': ['unknown', 'broken'],
  'C-41': ['permission', 'paused'],
  'C-42': ['partial', 'start'],
  'C-43': ['default'],
};

const ID_FORM = /^C-\d\d\.[a-z0-9-]+(\.[a-z0-9-]+)?$/;

function coverageProblems(items) {
  const problems = [];
  const ids = items.map((i) => i.id);
  if (new Set(ids).size !== ids.length) problems.push('duplicate ids');
  for (const i of items) {
    if (!ID_FORM.test(i.id)) problems.push(`id form ${i.id}`);
    if (i.component !== i.id.split('.')[0]) problems.push(`component of ${i.id}`);
  }
  for (const c of COMPONENTS) {
    if (!items.some((i) => i.component === c)) problems.push(`no item for ${c}`);
    for (const v of REQUIRED[c]) {
      if (!ids.includes(`${c}.${v}`)) problems.push(`missing ${c}.${v}`);
    }
  }
  return problems;
}

describe('T-9 gallery coverage', () => {
  it('T-9 every component C-01 to C-43 has its items with ids C-xx.<variant>[.<sub>], unique, naming every state or variant', () => {
    const problems = coverageProblems(GALLERY_ITEMS);
    const counts = {};
    for (const i of GALLERY_ITEMS) counts[i.component] = (counts[i.component] ?? 0) + 1;
    console.log(`T-9 ${GALLERY_ITEMS.length} items: ${JSON.stringify(counts)}`);
    expect(problems).toEqual([]);
    expect(Object.keys(REQUIRED).sort()).toEqual([...COMPONENTS].sort());
    expect([...RENDERED_COMPONENTS].sort()).toEqual([...COMPONENTS].sort());
    // The fixtures are not imported by any component.
    const componentDir = path.join(DESIGN_SYSTEM_DIR, 'components');
    for (const f of fs.readdirSync(componentDir)) {
      expect(fs.readFileSync(path.join(componentDir, f), 'utf8'), f).not.toContain('fixtures');
    }
    // Negative fixtures: a missing variant and a duplicate id fail.
    expect(coverageProblems(GALLERY_ITEMS.filter((i) => i.id !== 'C-11.waiting.two-line'))).toEqual(['missing C-11.waiting.two-line']);
    expect(coverageProblems([...GALLERY_ITEMS, GALLERY_ITEMS[0]])).toEqual(['duplicate ids']);
  });

  it('T-9 the gallery top shows the token table with the two changed values next to their canvas values', () => {
    const tokens = JSON.parse(fs.readFileSync(path.join(DESIGN_SYSTEM_DIR, 'tokens.json'), 'utf8'));
    const markup = renderToStaticMarkup(<Gallery />);
    const table = markup.indexOf('data-token-deviations');
    const firstItem = markup.indexOf('data-gallery-item=');
    expect(table).toBeGreaterThan(-1);
    expect(table).toBeLessThan(firstItem);
    const rowOf = (token) => {
      const start = markup.indexOf(`data-token="${token}"`);
      return start < 0 ? '' : markup.slice(start, markup.indexOf('</tr>', start));
    };
    const values = { 'color.faint': tokens.color.faint, 'color.unknown.fg': tokens.color.unknown.fg };
    for (const d of tokens._deviation_from_canvas.filter((x) => x.token in values)) {
      const row = rowOf(d.token);
      expect(row, d.token).toContain(d.canvas);
      expect(row, d.token).toContain(values[d.token]);
      expect(row.indexOf(d.canvas)).toBeLessThan(row.indexOf(values[d.token]));
    }
    // Every item is on the page.
    for (const i of GALLERY_ITEMS) {
      expect(markup).toContain(`data-gallery-item="${i.id}"`);
    }
  });
});
