// T-7 specification section 5 rows (packet DS-1 section 6), each as a named test.
import fs from 'node:fs';
import path from 'node:path';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Link, NamespaceTile, NodeCard, RefRow } from '../index.js';
import { DESIGN_SYSTEM_DIR, attr, cssRules, rulesForClass, textOf } from './helpers.js';

const css = cssRules(fs.readFileSync(path.join(DESIGN_SYSTEM_DIR, 'components.css'), 'utf8'));

const rowTags = (markup) => [...markup.matchAll(/<div[^>]*data-row-id="[^"]*"[^>]*>/g)].map((m) => m[0]);

// Fold check: exactly `normal` Connected rows other than the fold row, one fold row with
// the text "+N connected", and every anomalous row id present.
function foldProblems(markup, { normal, folded, anomalous }) {
  const tags = rowTags(markup);
  const problems = [];
  const plain = tags.filter((t) => attr(t, 'data-row-state') === 'connected' && attr(t, 'data-row-id') !== 'fold');
  const fold = tags.filter((t) => attr(t, 'data-row-id') === 'fold');
  if (plain.length !== normal) problems.push(`normal rows ${plain.length}`);
  if (fold.length !== 1) problems.push(`fold rows ${fold.length}`);
  if (!textOf(markup).includes(`+${folded} connected`)) problems.push('fold text');
  for (const id of anomalous) {
    if (!tags.some((t) => attr(t, 'data-row-id') === id)) problems.push(`anomalous row ${id} missing`);
  }
  return problems;
}

const row = (id, state, extra = {}) => ({ id, state, name: 'env', value: `value-${id}`, ...extra });

describe('T-7 specification section 5 rows', () => {
  it('T-7 fold rule: a compact NodeCard with 5 normal and 2 anomalous rows renders 2 normal rows, "+3 connected" and both anomalous rows', () => {
    const rows = [row('n1', 'connected'), row('a1', 'broken', { reason: 'Broken — ConfigMap payments-config not found' }), row('n2', 'connected'), row('n3', 'connected'), row('n4', 'connected'), row('a2', 'waiting', { reason: 'Waiting — data-db-0 waits for consumer Pod db-0' }), row('n5', 'connected')];
    const markup = renderToStaticMarkup(<NodeCard kind="Deployment" name="payments" rows={rows} mode="compact" />);
    const problems = foldProblems(markup, { normal: 2, folded: 3, anomalous: ['a1', 'a2'] });
    console.log(`T-7 fold rule rows ${JSON.stringify(rowTags(markup).map((t) => attr(t, 'data-row-id')))}`);
    expect(problems).toEqual([]);
    // Negative fixture: the expanded card (nothing folded) fails the fold check.
    const expanded = renderToStaticMarkup(<NodeCard kind="Deployment" name="payments" rows={rows} mode="expanded" />);
    expect(foldProblems(expanded, { normal: 2, folded: 3, anomalous: ['a1', 'a2'] })).not.toEqual([]);
  });

  it('T-7 reason line: a Broken RefRow with a 120-character reason has the whole reason in the DOM text, no ellipsis, no fixed row height', () => {
    const reason = 'selector app=web,tier=frontend,track=stable matches 0 Pods in shop: every Pod of Deployment web carries track=canary now';
    expect(reason).toHaveLength(120);
    const markup = renderToStaticMarkup(<RefRow id="r" state="broken" name="selects" value="app=web" reason={reason} />);
    const reasonProblems = (m) => {
      const p = [];
      if (!textOf(m).includes(reason)) p.push('reason not whole');
      if (textOf(m).includes('…')) p.push('ellipsis character');
      if (/\sstyle=/.test(m)) p.push('style attribute');
      return p;
    };
    expect(reasonProblems(markup)).toEqual([]);
    const heights = ['ds-refrow', 'ds-refrow--two-line', 'ds-refrow--broken', 'ds-refrow__reason'].flatMap((c) =>
      rulesForClass(css, c).flatMap((r) => r.decls.filter(([p]) => p === 'height' || p === 'max-height').map(([p, v]) => `${r.selectors.join(',')} ${p}: ${v}`)),
    );
    expect(heights).toEqual([]);
    // Negative fixture: a cut reason fails.
    expect(reasonProblems(markup.replace('track=canary now', 'track=ca…'))).not.toEqual([]);
  });

  it('T-7 empty state: a NamespaceTile with 0 Pods renders "No resources in <ns>" with the name', () => {
    const empty = (m, ns) => textOf(m).includes(`No resources in ${ns}`);
    const markup = renderToStaticMarkup(<NamespaceTile name="default" workloads={0} pods={{ ok: 0, progressing: 0, failing: 0 }} anomalies={{}} labels={{}} />);
    expect(empty(markup, 'default')).toBe(true);
    // Negative fixture: a tile with Pods does not show the empty text.
    const full = renderToStaticMarkup(<NamespaceTile name="shop" workloads={2} pods={{ ok: 3, progressing: 0, failing: 0 }} anomalies={{}} labels={{}} />);
    expect(empty(full, 'shop')).toBe(false);
  });

  it('T-7 accessible name: Link Broken from Service web to Pods app=web has kind, both ends, state and the caller text', () => {
    const text = 'selector app=web matches 0 Pods';
    const markup = renderToStaticMarkup(<Link from={{ x: 0, y: 0 }} to={{ x: 100, y: 40 }} width={120} height={60} state="broken" kind="Traffic" fromLabel="Service web" toLabel="Pods app=web" text={text} />);
    const tag = markup.match(/<svg[^>]*>/)[0];
    const name = attr(tag, 'aria-label');
    console.log(`T-7 accessible name: ${name}`);
    const nameProblems = (n) => ['Traffic', 'Service web', 'Pods app=web', 'Broken', text].filter((part) => !n || !n.includes(part));
    expect(attr(tag, 'role')).toBe('img');
    expect(nameProblems(name)).toEqual([]);
    // Negative fixture: a name without the state fails.
    expect(nameProblems(name.replace('Broken', ''))).toEqual(['Broken']);
  });
});
