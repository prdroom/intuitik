// T-4 CSP compatibility (static part) and T-6 read only (packet DS-1 section 6).
import fs from 'node:fs';
import path from 'node:path';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { GALLERY_ITEMS } from '../gallery/fixtures.js';
import { GalleryItem } from '../gallery/registry.jsx';
import { DESIGN_SYSTEM_DIR, attr, cssRules, relativeToDesignSystem, textOf, walkFiles, report, reportResults } from './helpers.js';

reportResults();

const itemMarkup = () => GALLERY_ITEMS.map((item) => [item.id, renderToStaticMarkup(<GalleryItem item={item} />)]);

// Inline style in static markup: a style attribute or a style element.
function inlineStyleProblems(markup) {
  const problems = [];
  if (/<[a-zA-Z][^>]*\sstyle=/.test(markup)) {
    problems.push('style attribute');
  }
  if (/<style[\s>]/i.test(markup)) {
    problems.push('style element');
  }
  return problems;
}

// Run-time style insertion in module source (style elements, CSSOM inserts, style props).
const RUNTIME_STYLE = /createElement\(\s*['"]style['"]|insertRule\s*\(|adoptedStyleSheets|\.style\.|\.style\s*=|setAttribute\(\s*['"]style['"]|\bstyle=\{/;

// Buttons and links whose text, aria-label or title names a cluster-changing action.
const CHANGING = /\b(apply|delete|patch|scale|restart|edit)\b/i;
function changingActions(markup) {
  const found = [];
  const re = /<(button|a)(\s[^>]*)?>([\s\S]*?)<\/\1>/g;
  let m;
  while ((m = re.exec(markup)) !== null) {
    const tag = `<${m[1]}${m[2] ?? ''}>`;
    for (const value of [textOf(m[3]), attr(tag, 'aria-label'), attr(tag, 'title')]) {
      if (value && CHANGING.test(value)) {
        found.push(`${m[1]}: ${value}`);
      }
    }
  }
  return found;
}

describe('T-4 CSP compatibility, static part', () => {
  it('T-4 no gallery item has a style attribute or a style element; fonts load by @font-face with relative url() only', () => {
    const items = itemMarkup();
    expect(items.length).toBe(GALLERY_ITEMS.length);
    const problems = items.flatMap(([id, markup]) => inlineStyleProblems(markup).map((p) => `${id}: ${p}`));
    report(`T-4 static markup of ${items.length} items: ${problems.length} inline style problem(s)`);
    expect(problems).toEqual([]);

    const modules = walkFiles(DESIGN_SYSTEM_DIR).filter((f) => /\.(js|jsx)$/.test(f) && !relativeToDesignSystem(f).startsWith('tests/'));
    const inserting = modules.filter((f) => RUNTIME_STYLE.test(fs.readFileSync(f, 'utf8'))).map(relativeToDesignSystem);
    expect(inserting).toEqual([]);

    const fontsCss = fs.readFileSync(path.join(DESIGN_SYSTEM_DIR, 'fonts.css'), 'utf8');
    const faces = cssRules(fontsCss).filter((r) => r.selectors[0] === '@font-face');
    expect(faces).toHaveLength(2);
    const urls = faces.flatMap((r) => r.decls.filter(([p]) => p === 'src').flatMap(([, v]) => [...v.matchAll(/url\(\s*'([^']*)'\s*\)/g)].map((u) => u[1])));
    expect(urls.sort()).toEqual(['./fonts/Geist-wght.woff2', './fonts/GeistMono-wght.woff2']);
    const allCss = walkFiles(DESIGN_SYSTEM_DIR)
      .filter((f) => f.endsWith('.css'))
      .map((f) => fs.readFileSync(f, 'utf8'))
      .join('\n');
    expect(allCss.match(/@font-face/g)).toHaveLength(2);

    // Negative fixtures: a style attribute, a style element and a CSSOM insert are found.
    expect(inlineStyleProblems('<div style="color:red">x</div>')).toEqual(['style attribute']);
    expect(inlineStyleProblems('<style>.a{}</style>')).toEqual(['style element']);
    expect(RUNTIME_STYLE.test('sheet.insertRule(".a{}")')).toBe(true);
  });
});

describe('T-6 read only', () => {
  it('T-6 no button or link of any gallery item names apply, delete, patch, scale, restart or edit', () => {
    const items = itemMarkup();
    let controls = 0;
    const found = [];
    for (const [id, markup] of items) {
      controls += (markup.match(/<(button|a)[\s>]/g) ?? []).length;
      for (const f of changingActions(markup)) {
        found.push(`${id}: ${f}`);
      }
    }
    report(`T-6 ${controls} buttons and links checked, ${found.length} cluster-changing`);
    expect(controls).toBeGreaterThan(20);
    expect(found).toEqual([]);
    // Negative fixtures.
    expect(changingActions('<button type="button">Delete pod</button>')).toHaveLength(1);
    expect(changingActions('<a href="#x" title="Edit YAML">Open</a>')).toHaveLength(1);
    expect(changingActions('<button aria-label="Scale up">+</button>')).toHaveLength(1);
  });
});
