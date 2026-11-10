// T-10 port coordinates (packet DS-1 section 6; C-12, PMC f9f302ed). This proves the
// arithmetic; the laid-out positions and the callback values are B-2 in the browser.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { rowCenters } from '../geometry.js';
import { DESIGN_SYSTEM_DIR, cssRules, rulesForClass } from './helpers.js';

const tokens = JSON.parse(fs.readFileSync(path.join(DESIGN_SYSTEM_DIR, 'tokens.json'), 'utf8'));

const sameCentres = (a, b) => a.length === b.length && a.every((v, i) => Math.abs(v - b[i]) < 1e-9);

// Cutting declarations of the reason line's rules.
function cutDeclarations(rules) {
  return rulesForClass(rules, 'ds-refrow__reason').flatMap((r) =>
    r.decls.filter(([p, v]) => (p === 'text-overflow' && /ellipsis/.test(v)) || (p === 'white-space' && /nowrap/.test(v)) || p === '-webkit-line-clamp' || p === 'line-clamp').map(([p, v]) => `${p}: ${v}`),
  );
}

describe('T-10 port coordinates', () => {
  it('T-10 rowCenters gives each row centre from cardHeader, rowPad, rowGap and the laid-out heights', () => {
    const { cardHeader, rowPad, rowGap, rowTwoLine } = tokens.size;
    expect(rowTwoLine).toBe(48);
    const heights = [30, 48, 76, 30];
    // Expected values, computed here from the tokens.
    const expected = [];
    let top = cardHeader + rowPad;
    for (const h of heights) {
      expected.push(top + h / 2);
      top += h + rowGap;
    }
    const actual = rowCenters(heights);
    console.log(`T-10 rowCenters([30, 48, 76, 30]) = ${JSON.stringify(actual)}; expected ${JSON.stringify(expected)}`);
    expect(sameCentres(actual, expected)).toBe(true);
    // A taller reason row moves every later centre: heights are inputs, not a fixed row.
    expect(rowCenters([30, 30])[1]).not.toBe(rowCenters([48, 30])[1]);
    // Negative fixture: centres shifted by one row gap fail the comparison.
    expect(sameCentres(actual.map((c) => c + rowGap), expected)).toBe(false);
  });

  it('T-10 the CSS of the reason line has neither text-overflow: ellipsis nor white-space: nowrap', () => {
    const rules = cssRules(fs.readFileSync(path.join(DESIGN_SYSTEM_DIR, 'components.css'), 'utf8'));
    expect(rulesForClass(rules, 'ds-refrow__reason').length).toBeGreaterThan(0);
    expect(cutDeclarations(rules)).toEqual([]);
    // Negative fixture: a reason rule with nowrap and ellipsis fails.
    expect(cutDeclarations(cssRules('.ds-refrow__reason { white-space: nowrap; text-overflow: ellipsis; }'))).toHaveLength(2);
  });
});
