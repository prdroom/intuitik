// Builds the design system gallery with Vite's build() API and web/vite.gallery.config.js
// (only outDir overridden, to a temporary directory outside the repository) and serves it
// on 127.0.0.1 with the panel's normal-response CSP (packet DS-1 section 6.2). The only
// other route is /__negative/<name>, the negative fixture pages of gallery_fixtures/.
// The font files can be held until the test releases them (B-2).
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'vite';

const WEB_DIR = fileURLToPath(new URL('..', import.meta.url));
const FIXTURE_DIR = fileURLToPath(new URL('./gallery_fixtures/', import.meta.url));

// T2's normal-response value without the ws:// source (the gallery opens no socket).
export const GALLERY_CSP = "default-src 'self'; connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'";

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8',
};

function inside(root, file) {
  const rel = path.relative(root, file);
  return rel !== '' && !rel.startsWith('..') && !path.isAbsolute(rel);
}

function get(url) {
  return new Promise((resolve) => {
    http
      .get(url, (res) => {
        res.resume();
        resolve(res.statusCode);
      })
      .on('error', () => resolve(0));
  });
}

// Closes the server (if listening) and deletes the build directory.
async function cleanUp(server, outDir) {
  if (server && server.listening) {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
  fs.rmSync(outDir, { recursive: true, force: true });
}

// Every failure after the build directory exists (build, listen, readiness) closes the
// server and deletes the directory before it is thrown; the error carries both, so that
// a caller can verify the clean-up. `readyPath` and `readyTimeoutMs` exist for that check.
export async function startGallery({ readyPath = '/gallery.html', readyTimeoutMs = 10_000 } = {}) {
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'intuitik-gallery-'));
  const fonts = { hold: false, held: [], requested: 0 };
  let server;
  let origin;
  try {
    await build({
      configFile: path.join(WEB_DIR, 'vite.gallery.config.js'),
      logLevel: 'warn',
      build: { outDir, emptyOutDir: true },
    });
    server = http.createServer((req, res) => {
      const { pathname } = new URL(req.url, 'http://127.0.0.1');
      const negative = pathname.startsWith('/__negative/');
      const root = negative ? FIXTURE_DIR : outDir;
      const rel = decodeURIComponent(negative ? pathname.slice('/__negative/'.length) : pathname === '/' ? 'gallery.html' : pathname.slice(1));
      const file = path.resolve(root, rel);
      if (!inside(root, file) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
        res.writeHead(404, { 'Content-Security-Policy': GALLERY_CSP });
        res.end();
        return;
      }
      const send = () => {
        res.writeHead(200, {
          'Content-Type': TYPES[path.extname(file)] ?? 'application/octet-stream',
          'Content-Security-Policy': GALLERY_CSP,
          'Cache-Control': 'no-store',
        });
        res.end(fs.readFileSync(file));
      };
      if (file.endsWith('.woff2')) {
        fonts.requested += 1;
        if (fonts.hold) {
          fonts.held.push(send);
          return;
        }
      }
      send();
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    origin = `http://127.0.0.1:${server.address().port}`;
    // The tests start only after the server answers.
    const deadline = Date.now() + readyTimeoutMs;
    while ((await get(`${origin}${readyPath}`)) !== 200) {
      if (Date.now() > deadline) {
        throw new Error(`the gallery server did not answer ${readyPath} within ${readyTimeoutMs} ms`);
      }
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  } catch (err) {
    await cleanUp(server, outDir);
    err.galleryOutDir = outDir;
    err.galleryOrigin = origin;
    throw err;
  }
  return {
    origin,
    url: `${origin}/gallery.html`,
    negative: (name) => `${origin}/__negative/${name}`,
    holdFonts() {
      fonts.hold = true;
      fonts.requested = 0;
    },
    heldFonts: () => fonts.held.length,
    releaseFonts() {
      fonts.hold = false;
      for (const send of fonts.held.splice(0)) {
        send();
      }
    },
    async close() {
      fonts.hold = false;
      for (const send of fonts.held.splice(0)) {
        send();
      }
      await cleanUp(server, outDir);
    },
  };
}

// Starts a gallery whose readiness check cannot succeed and returns what was left behind:
// the error, whether the build directory still exists and whether the port still answers.
export async function readinessFailureLeftovers() {
  try {
    const gallery = await startGallery({ readyPath: '/__negative/no-such-page.html', readyTimeoutMs: 500 });
    await gallery.close();
    return { error: null };
  } catch (err) {
    const dirExists = Boolean(err.galleryOutDir) && fs.existsSync(err.galleryOutDir);
    const status = err.galleryOrigin ? await get(`${err.galleryOrigin}/gallery.html`) : 0;
    return { error: err.message, outDir: err.galleryOutDir, origin: err.galleryOrigin, dirExists, portAnswers: status !== 0 };
  }
}

// The identity lines every gallery test prints (packet section 6.2). On a pull_request run
// the checkout is the merge commit, not the PR head; the run record binds the head.
export function identityLines() {
  let head;
  try {
    head = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: WEB_DIR, encoding: 'utf8' }).trim();
  } catch (err) {
    head = `unavailable (${err.message.split('\n')[0]})`;
  }
  return [`checkout ${head}`, `github_sha ${process.env.GITHUB_SHA ?? 'unset'}`];
}
