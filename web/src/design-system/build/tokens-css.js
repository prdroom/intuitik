// Build-time generation of the design system CSS custom properties from tokens.json
// (packet DS-1 T-1, T-4). Nothing here runs in the browser: the gallery build turns the
// generated text into a CSS file, so no style is inserted at run time.
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const TOKENS_PATH = fileURLToPath(new URL('../tokens.json', import.meta.url));
const OFL_PATH = fileURLToPath(new URL('../fonts/OFL.txt', import.meta.url));

export const TOKENS_CSS_ID = 'virtual:design-system-tokens.css';
const RESOLVED_TOKENS_CSS_ID = '\0virtual:design-system-tokens.css';

const HEX_COLOUR = /^#[0-9a-fA-F]{6}$/;

export function readTokens() {
  return JSON.parse(fs.readFileSync(TOKENS_PATH, 'utf8'));
}

// Every leaf of tokens.json except the metadata keys (leading "_") and null values, as
// [custom property name, CSS value]. Numbers are pixel sizes; the type arrays become a
// size and a line height.
export function tokenCustomProperties(tokens) {
  const out = [];
  const walk = (node, path) => {
    if (node === null) {
      return;
    }
    if (Array.isArray(node)) {
      if (path[0] === 'type' && node.length === 2) {
        out.push([`--${path.join('-')}-size`, `${node[0]}px`]);
        out.push([`--${path.join('-')}-line`, `${node[1]}px`]);
      }
      return;
    }
    if (typeof node === 'object') {
      for (const [key, value] of Object.entries(node)) {
        if (!key.startsWith('_')) {
          walk(value, [...path, key]);
        }
      }
      return;
    }
    out.push([`--${path.join('-')}`, typeof node === 'number' ? `${node}px` : String(node)]);
  };
  walk(tokens, []);
  return out;
}

// Every colour value in tokens.json, metadata included (the canvas values of
// _deviation_from_canvas), for the gallery's swatch rules.
export function tokenColours(tokens) {
  const colours = new Set();
  const walk = (node) => {
    if (typeof node === 'string' && HEX_COLOUR.test(node)) {
      colours.add(node.toUpperCase());
    } else if (node && typeof node === 'object') {
      Object.values(node).forEach(walk);
    }
  };
  walk(tokens);
  return [...colours].sort();
}

export function tokensCss(tokens) {
  const lines = [':root {'];
  for (const [name, value] of tokenCustomProperties(tokens)) {
    lines.push(`  ${name}: ${value};`);
  }
  lines.push('}');
  for (const colour of tokenColours(tokens)) {
    lines.push(`.ds-swatch[data-colour="${colour.slice(1)}"] { background: ${colour}; }`);
  }
  return `${lines.join('\n')}\n`;
}

// Vite plugin: `import 'virtual:design-system-tokens.css'` yields the generated CSS.
export function designSystemTokens() {
  return {
    name: 'intuitik-design-system-tokens',
    resolveId(id) {
      return id === TOKENS_CSS_ID ? RESOLVED_TOKENS_CSS_ID : null;
    },
    load(id) {
      if (id !== RESOLVED_TOKENS_CSS_ID) {
        return null;
      }
      this.addWatchFile(TOKENS_PATH);
      return tokensCss(readTokens());
    },
  };
}

// Vite plugin: emits the font licence next to the gallery page as OFL.txt, byte for byte,
// so that the page's relative link resolves.
export function fontLicence() {
  return {
    name: 'intuitik-design-system-font-licence',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: 'OFL.txt', source: fs.readFileSync(OFL_PATH) });
    },
  };
}
