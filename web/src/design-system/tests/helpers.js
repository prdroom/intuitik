// Shared helpers of the design system tests: git blob ids, a small CSS rule reader, static
// markup text and file walking. No test framework code here.
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const DESIGN_SYSTEM_DIR = fileURLToPath(new URL('..', import.meta.url));
export const WEB_DIR = fileURLToPath(new URL('../../..', import.meta.url));

// The git blob id of the bytes: SHA-1 of "blob <len>\0" followed by the bytes.
export function gitBlobId(bytes) {
  const header = Buffer.from(`blob ${bytes.length}\0`, 'utf8');
  return crypto.createHash('sha1').update(Buffer.concat([header, bytes])).digest('hex');
}

export function sha256(bytes) {
  return crypto.createHash('sha256').update(bytes).digest('hex');
}

// Rules of a plain CSS file (no nesting): [{ selectors: [...], decls: [[prop, value]] }].
// @font-face blocks come back with the selector "@font-face".
export function cssRules(text) {
  const rules = [];
  const body = text.replace(/\/\*[\s\S]*?\*\//g, '');
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m;
  while ((m = re.exec(body)) !== null) {
    const selectors = m[1]
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    const decls = m[2]
      .split(';')
      .map((d) => d.trim())
      .filter(Boolean)
      .map((d) => {
        const i = d.indexOf(':');
        return [d.slice(0, i).trim().toLowerCase(), d.slice(i + 1).trim()];
      });
    rules.push({ selectors, decls });
  }
  return rules;
}

// Rules with a selector that names the class (as a whole class name).
export function rulesForClass(rules, className) {
  const re = new RegExp(`\\.${className.replace(/[-]/g, '\\-')}(?![\\w-])`);
  return rules.filter((r) => r.selectors.some((s) => re.test(s)));
}

const ENTITIES = { '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#x27;': "'", '&#39;': "'" };

export function decode(text) {
  return text.replace(/&(amp|lt|gt|quot|#x27|#39);/g, (e) => ENTITIES[e]);
}

// The text content of a markup string.
export function textOf(markup) {
  return decode(markup.replace(/<[^>]*>/g, ''));
}

// Attribute value of the first match of `name="..."` in an element's start tag.
export function attr(startTag, name) {
  const m = startTag.match(new RegExp(`\\s${name}="([^"]*)"`));
  return m ? decode(m[1]) : null;
}

export function walkFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walkFiles(p, out);
    } else {
      out.push(p);
    }
  }
  return out;
}

export function relativeToDesignSystem(file) {
  return path.relative(DESIGN_SYSTEM_DIR, file).split(path.sep).join('/');
}
