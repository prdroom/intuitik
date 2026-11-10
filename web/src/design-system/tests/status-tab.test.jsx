// T-3a attention is not a tab (packet DS-1 section 6; specification C-10, DS-D).
import fs from 'node:fs';
import path from 'node:path';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { NodeCard, StatusTab } from '../index.js';
import { DESIGN_SYSTEM_DIR, attr, cssRules, rulesForClass, textOf, report, reportResults } from './helpers.js';

reportResults();

const css = fs.readFileSync(path.join(DESIGN_SYSTEM_DIR, 'components.css'), 'utf8');

// The start tag of the element that carries data-status.
function statusElement(markup) {
  const m = markup.match(/<div[^>]*data-status="[^"]*"[^>]*>/);
  return m ? m[0] : null;
}

const classesOf = (tag) => (attr(tag, 'class') ?? '').split(/\s+/).filter(Boolean);

// Border and fill classes of the three node-state tabs, read from their rendered markup.
function nodeTabStyleClasses() {
  const classes = new Set();
  for (const state of ['failing', 'progressing', 'unknown']) {
    const tag = statusElement(renderToStaticMarkup(<StatusTab state={state} text="x" />));
    for (const c of classesOf(tag)) {
      if (/^ds-tab-(border|fill)-/.test(c)) {
        classes.add(c);
      }
    }
  }
  return classes;
}

// Problems of an attention element: a node-state border or fill class, or a CSS rule of
// one of its classes that sets a border other than none or a background other than
// transparent.
function attentionProblems(classes, tabClasses, rules) {
  const problems = [];
  for (const c of classes) {
    if (tabClasses.has(c)) {
      problems.push(`class ${c}`);
    }
    for (const rule of rulesForClass(rules, c)) {
      for (const [prop, value] of rule.decls) {
        if (prop.startsWith('border') && !prop.startsWith('border-radius') && value !== 'none') {
          problems.push(`${rule.selectors.join(',')} ${prop}: ${value}`);
        }
        if (prop.startsWith('background') && value !== 'transparent') {
          problems.push(`${rule.selectors.join(',')} ${prop}: ${value}`);
        }
      }
    }
  }
  return problems;
}

describe('T-3a attention is not a tab', () => {
  it('T-3a the StatusTab of an attention-only card has the \u2715 mark and the caller text, no tab border and no fill', () => {
    const markup = renderToStaticMarkup(<NodeCard kind="Service" name="web" rows={[]} status={{ state: 'attention', text: '1 broken link' }} />);
    const tag = statusElement(markup);
    expect(tag).not.toBeNull();
    const classes = classesOf(tag);
    const start = markup.indexOf(tag);
    const element = markup.slice(start, markup.indexOf('</div>', start) + 6);
    expect(element).toContain('data-icon="x"');
    expect(textOf(element)).toContain('1 broken link');
    const tabClasses = nodeTabStyleClasses();
    expect([...tabClasses].sort()).toEqual(['ds-tab-border-dashed', 'ds-tab-border-solid', 'ds-tab-fill-failing', 'ds-tab-fill-progressing', 'ds-tab-fill-unknown']);
    const rules = cssRules(css);
    const attentionRules = rulesForClass(rules, 'ds-status-mark--attention');
    expect(attentionRules.length).toBeGreaterThan(0);
    const problems = attentionProblems(classes, tabClasses, rules);
    report(`T-3a attention classes ${classes.join(' ')}; problems ${JSON.stringify(problems)}`);
    expect(problems).toEqual([]);
    // Negative fixture: the attention element given the failing tab's border class fails.
    expect(attentionProblems([...classes, 'ds-tab-border-solid'], tabClasses, rules)).not.toEqual([]);
  });
});
