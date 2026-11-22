import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { designSystemTokens, fontLicence } from '../src/design-system/build/tokens-css.js';

// The relation map prototype (packet FA2-T8 6.3). It is not part of the product build: its
// own pages, its own output directory web/prototype/dist (git-ignored) and relative paths.
// Object files are copied next to the pages and fetched on demand, never bundled; an object
// is copied only when its namespace's graph answer lists it as read, so the object of a
// Secret the answer cannot read (payments/payment-api) is not served. The T12 L graph file is
// copied byte for byte for the measurement page.
const root = fileURLToPath(new URL('.', import.meta.url));
const objectsDir = path.join(root, 'data', 'objects');
const lGraph = path.join(root, '..', '..', 'test', 'e2e', 'prototype', 'testdata', 'l-graph.tar.gz');

function readNodes(ns) {
  const file = path.join(root, 'data', `ns-${ns}.json`);
  if (!fs.existsSync(file)) {
    return new Set();
  }
  return new Set(JSON.parse(fs.readFileSync(file, 'utf8')).nodes.map((n) => `${n.kind}/${n.name}`));
}

function staticFiles() {
  return {
    name: 'intuitik-prototype-static',
    generateBundle() {
      for (const ns of fs.readdirSync(objectsDir).sort()) {
        const read = readNodes(ns);
        for (const kind of fs.readdirSync(path.join(objectsDir, ns)).sort()) {
          for (const file of fs.readdirSync(path.join(objectsDir, ns, kind)).sort()) {
            if (read.has(`${kind}/${file.replace(/\.json$/, '')}`)) {
              this.emitFile({ type: 'asset', fileName: `data/objects/${ns}/${kind}/${file}`, source: fs.readFileSync(path.join(objectsDir, ns, kind, file)) });
            }
          }
        }
      }
      // Not named .gz: vite preview would send it with Content-Encoding gzip and the browser
      // would hand the page the unpacked tar instead of the file's own bytes.
      this.emitFile({ type: 'asset', fileName: 'measure/l-graph.bin', source: fs.readFileSync(lGraph) });
    },
  };
}

export default defineConfig({
  root,
  base: './',
  publicDir: false,
  plugins: [react(), designSystemTokens(), fontLicence(), staticFiles()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    rollupOptions: {
      input: {
        main: path.join(root, 'index.html'),
        measure: path.join(root, 'measure', 'index.html'),
        compare: path.join(root, 'measure', 'compare.html'),
      },
    },
  },
  preview: {
    port: 4317,
    strictPort: true,
  },
});
