// T-5 resource references (static part), T-5a app isolation and T-11 app unchanged
// (packet DS-1 section 6). The builds go to temporary directories outside the repository.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { build } from 'vite';
import { afterAll, describe, expect, it } from 'vitest';
import { WEB_DIR, gitBlobId, sha256, walkFiles } from './helpers.js';

const FONT_SHA256 = {
  'Geist-wght.woff2': '9ce2d97863cab59a0c7522534112d6c42a4e25c0e6b2ab37766761b0f82cfbd6',
  'GeistMono-wght.woff2': '119f3e159fc15dd77311e929d3a657a193b57a3e625234666bc43c6e2b1127f7',
  'OFL.txt': 'c683bfbcc7e087f5d37a54ef628f10387c451a83ddc459b151403a164ac46c90',
};
const OFL_LINK_TEXT = 'Fonts: Geist and Geist Mono, SIL Open Font License 1.1';

const temps = [];
function tempDir(prefix) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  temps.push(dir);
  return dir;
}
afterAll(() => {
  for (const dir of temps) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

async function buildWith(configName, outDir) {
  const result = await build({
    configFile: path.join(WEB_DIR, configName),
    root: WEB_DIR,
    logLevel: 'warn',
    build: { outDir, emptyOutDir: true },
  });
  return (Array.isArray(result) ? result : [result]).flatMap((r) => r.output);
}

const isRelative = (ref) => !/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(ref) && !ref.startsWith('/') && !ref.startsWith('#');

// Every src and href of an HTML file and every url() and @import of a CSS file must be a
// relative path to a file in the output; no font may be a data: URL. Returns the scan
// lines and the problems.
function scanReferences(outDir, files) {
  const lines = [];
  const problems = [];
  const check = (from, ref, kind) => {
    if (/^data:/i.test(ref)) {
      lines.push(`${from} ${kind} data: URL`);
      if (/font|woff/i.test(ref) || kind === 'css url()') {
        problems.push(`${from}: data URL ${ref.slice(0, 40)}`);
      }
      return;
    }
    if (!isRelative(ref)) {
      lines.push(`${from} ${kind} ${ref} -> not relative`);
      problems.push(`${from}: not relative ${ref}`);
      return;
    }
    const target = path.resolve(path.dirname(path.join(outDir, from)), ref.split(/[?#]/)[0]);
    const exists = target.startsWith(outDir) && fs.existsSync(target) && fs.statSync(target).isFile();
    lines.push(`${from} ${kind} ${ref} -> ${exists ? 'file in output' : 'missing'}`);
    if (!exists) {
      problems.push(`${from}: missing ${ref}`);
    }
  };
  for (const [rel, text] of files) {
    if (rel.endsWith('.html')) {
      for (const m of text.matchAll(/\s(src|href)="([^"]*)"/g)) {
        check(rel, m[2], `html ${m[1]}`);
      }
    } else if (rel.endsWith('.css')) {
      for (const m of text.matchAll(/url\(\s*(['"]?)([^'")]*)\1\s*\)/g)) {
        check(rel, m[2], 'css url()');
      }
      for (const m of text.matchAll(/@import\s+(?:url\()?\s*['"]?([^'")\s;]+)/g)) {
        check(rel, m[1], 'css @import');
      }
    }
  }
  return { lines, problems };
}

// Chunks of a build output whose modules come from the design system or the gallery, and
// emitted font files.
function designSystemInOutput(output) {
  const found = [];
  for (const chunk of output) {
    if (chunk.type === 'chunk') {
      for (const id of chunk.moduleIds) {
        const p = id.split(path.sep).join('/');
        if (p.includes('/src/design-system/') || p.includes('gallery')) {
          found.push(`${chunk.fileName}: ${p.slice(p.indexOf('/src/') >= 0 ? p.indexOf('/src/') : 0)}`);
        }
      }
    } else if (/\.(woff2?|ttf|otf)$/i.test(chunk.fileName)) {
      found.push(`font asset ${chunk.fileName}`);
    }
  }
  return found;
}

describe('T-5 resource references', () => {
  it('T-5 the built gallery references only relative files inside its output; fonts and OFL.txt are separate files with the pinned sha256', { timeout: 120_000 }, async () => {
    const outDir = tempDir('ds-gallery-');
    const output = await buildWith('vite.gallery.config.js', outDir);
    const files = walkFiles(outDir).map((f) => [path.relative(outDir, f).split(path.sep).join('/'), f]);
    const texts = files.filter(([rel]) => /\.(html|css)$/.test(rel)).map(([rel, f]) => [rel, fs.readFileSync(f, 'utf8')]);
    expect(texts.some(([rel]) => rel === 'gallery.html')).toBe(true);
    const { lines, problems } = scanReferences(outDir, texts);
    for (const line of lines) {
      console.log(`T-5 scan: ${line}`);
    }
    expect(lines.length).toBeGreaterThanOrEqual(5);
    expect(problems).toEqual([]);

    // The two fonts and OFL.txt are emitted as their own files with the pinned bytes.
    for (const [name, expected] of Object.entries(FONT_SHA256)) {
      const stem = name.replace(/\.[^.]+$/, '');
      const ext = path.extname(name);
      const match = files.filter(([rel]) => path.basename(rel).startsWith(stem) && rel.endsWith(ext) && (name !== 'Geist-wght.woff2' || !rel.includes('GeistMono')));
      expect(match, name).toHaveLength(1);
      const digest = sha256(fs.readFileSync(match[0][1]));
      console.log(`T-5 emitted ${match[0][0]} sha256 ${digest}`);
      expect(digest).toBe(expected);
    }
    expect(output.filter((c) => c.type === 'asset' && /\.woff2$/.test(c.fileName))).toHaveLength(2);

    // The page links to OFL.txt with the licence text.
    const html = texts.find(([rel]) => rel === 'gallery.html')[1];
    const link = [...html.matchAll(/<a\s[^>]*href="([^"]*)"[^>]*>([^<]*)<\/a>/g)].find((m) => m[2] === OFL_LINK_TEXT);
    expect(link, 'OFL link').toBeTruthy();
    expect(link[1]).toBe('OFL.txt');

    // Negative fixtures: an absolute path, another origin and a font data: URL fail.
    const bad = scanReferences(outDir, [
      ['gallery.html', '<script src="/assets/a.js"></script><link href="https://cdn.example.invalid/a.css">'],
      ['assets/x.css', '@font-face{src:url(data:font/woff2;base64,AAAA)}'],
    ]);
    expect(bad.problems).toHaveLength(3);
  });
});

describe('T-5a app isolation', () => {
  it('T-5a the app build output has no design system or gallery module and no font file', { timeout: 120_000 }, async () => {
    const appOut = await buildWith('vite.config.js', tempDir('ds-app-'));
    const chunks = appOut.filter((c) => c.type === 'chunk');
    expect(chunks.length).toBeGreaterThan(0);
    const found = designSystemInOutput(appOut);
    console.log(`T-5a app build: ${chunks.length} chunk(s), ${chunks.reduce((n, c) => n + c.moduleIds.length, 0)} module(s), design system modules or fonts: ${found.length}`);
    expect(found).toEqual([]);
    // Negative fixture: the same check finds the design system in the gallery build.
    const galleryOut = await buildWith('vite.gallery.config.js', tempDir('ds-gallery-'));
    const inGallery = designSystemInOutput(galleryOut);
    expect(inGallery.some((f) => f.includes('/src/design-system/'))).toBe(true);
    expect(inGallery.filter((f) => f.startsWith('font asset'))).toHaveLength(2);
  });
});

// Blobs of the app files at the base 79a1618aa20b3a5c9762ff3586bd3b334009c3a5.
const APP_FILES = {
  'src/App.jsx': '92aa95744fa67593e5b871f7a13ae8e88d30309a',
  'src/App.test.jsx': '1bfe3cef79d2d30a7b670439faddd494ca8bf40d',
  'src/main.jsx': 'e1dc4fc51648bd284f02e7b49f97a68f2390cb58',
  'index.html': '8907ea331145557fecbebe415f07dfa525e703e1',
  'vite.config.js': '80cfbb288c88fb6a1abc4f9ba340eb4cac5d9781',
};

describe('T-11 app unchanged', () => {
  it('T-11 the app files equal their base blobs next to the separate gallery entry', () => {
    const changed = [];
    for (const [rel, blob] of Object.entries(APP_FILES)) {
      const actual = gitBlobId(fs.readFileSync(path.join(WEB_DIR, rel)));
      console.log(`T-11 ${rel} ${actual}`);
      if (actual !== blob) {
        changed.push(rel);
      }
    }
    expect(changed).toEqual([]);
    // The gallery has its own entry and configuration; the app's page does not reach it.
    const galleryHtml = fs.readFileSync(path.join(WEB_DIR, 'gallery.html'), 'utf8');
    expect(galleryHtml).toContain('src/design-system/gallery/main.jsx');
    const galleryConfig = fs.readFileSync(path.join(WEB_DIR, 'vite.gallery.config.js'), 'utf8');
    expect(galleryConfig).toContain("outDir: 'dist-gallery'");
    expect(fs.readFileSync(path.join(WEB_DIR, 'index.html'), 'utf8')).not.toContain('design-system');
    // Negative fixture: one changed byte of App.jsx gives another blob.
    const app = Buffer.from(fs.readFileSync(path.join(WEB_DIR, 'src/App.jsx')));
    app[0] ^= 1;
    expect(gitBlobId(app)).not.toBe(APP_FILES['src/App.jsx']);
  });
});
