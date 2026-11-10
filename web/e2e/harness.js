// Runs intuitik for the browser cases. The panel's stdin is a pseudo terminal made by
// `script` (util-linux), so that Enter can be pressed; its stderr goes to a file; the
// opener is a recording stand-in given through BROWSER (packet item P1 (a)).
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

export const TEXTS = {
  noSession: [
    'No session in this tab.',
    'To open the panel, press Enter in the terminal where intuitik is running. A new login address opens in your browser. You can also restart intuitik.',
    'If your terminal shows "someone else may have connected first", restart intuitik instead of pressing Enter.',
  ],
  refused: [
    'This login address is no longer valid. It was already used, or it expired after 60 seconds.',
    'Press Enter in the terminal where intuitik is running to open a new login address, or restart intuitik.',
    'If your terminal shows "someone else may have connected first", restart intuitik instead of pressing Enter.',
  ],
  stopped: [
    'intuitik has stopped, and this tab is disconnected.',
    'Close this tab. To continue, run intuitik again; it opens a new tab.',
  ],
};

const LOGIN_URL = /^http:\/\/127\.0\.0\.1:(\d+)\/#t=([A-Za-z0-9_-]{43})$/;

function binary(test) {
  const name = test ? 'INTUITIK_E2E_TEST_BINARY' : 'INTUITIK_E2E_BINARY';
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is not set`);
  }
  return value;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function until(what, timeoutMs, fn) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await fn();
    if (value) {
      return value;
    }
    if (Date.now() > deadline) {
      throw new Error(`no ${what} within ${timeoutMs} ms`);
    }
    await sleep(25);
  }
}

export async function startPanel({ test = false, fixture, ports, clock = false, hold = false } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'intuitik-pw-'));
  const openLog = path.join(dir, 'opened.txt');
  const errFile = path.join(dir, 'stderr.txt');
  const recorder = path.join(dir, 'record-open');
  fs.writeFileSync(recorder, `#!/bin/sh\nprintf '%s\\n' "$1" >> '${openLog}'\n`, { mode: 0o700 });
  const env = { ...process.env, BROWSER: recorder };
  for (const key of Object.keys(env)) {
    if (key.startsWith('INTUITIK_TEST_')) {
      delete env[key];
    }
  }
  if (fixture) env.INTUITIK_TEST_FIXTURE = fixture;
  if (ports) env.INTUITIK_TEST_PORTS = ports.join(',');
  const clockFile = path.join(dir, 'clock.txt');
  if (clock) {
    fs.writeFileSync(clockFile, '0');
    env.INTUITIK_TEST_CLOCK = clockFile;
  }
  const releaseFile = path.join(dir, 'release');
  if (hold) env.INTUITIK_TEST_HOLD_RELEASE = releaseFile;

  const child = spawn('script', ['-q', '-f', '-e', '-c', `exec '${binary(test || Boolean(fixture) || clock || hold || Boolean(ports))}' 2>'${errFile}'`, '/dev/null'], {
    env,
    stdio: ['pipe', 'pipe', 'inherit'],
  });
  let stdout = '';
  child.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  const exited = new Promise((resolve) => child.on('exit', resolve));
  const port = Number(
    await until('listen line', 15_000, () => stdout.match(/listening on http:\/\/127\.0\.0\.1:(\d+)/)?.[1]),
  );
  const pid = Number(
    await until('panel pid', 5_000, () => {
      try {
        return execFileSync('pgrep', ['-P', String(child.pid)]).toString().trim().split('\n')[0];
      } catch {
        return null;
      }
    }),
  );
  const opened = () => {
    try {
      return fs.readFileSync(openLog, 'utf8').split('\n').filter(Boolean);
    } catch {
      return [];
    }
  };
  const panel = {
    port,
    pid,
    origin: `http://127.0.0.1:${port}`,
    stdout: () => stdout,
    stderr: () => {
      try {
        return fs.readFileSync(errFile, 'utf8');
      } catch {
        return '';
      }
    },
    async token(n = 1) {
      const url = await until(`login address ${n}`, 10_000, () => opened()[n - 1]);
      const m = url.match(LOGIN_URL);
      if (!m || Number(m[1]) !== port) {
        throw new Error(`opener argument ${url} is not this panel's login address`);
      }
      return m[2];
    },
    async enter() {
      const n = opened().length;
      child.stdin.write('\n');
      return panel.token(n + 1);
    },
    setClock(seconds) {
      fs.writeFileSync(clockFile, String(seconds));
    },
    release() {
      fs.writeFileSync(releaseFile, 'go');
    },
    async interrupt() {
      process.kill(pid, 'SIGINT');
      await exited;
    },
    async kill() {
      process.kill(pid, 'SIGKILL');
      await exited;
    },
    async close() {
      try {
        process.kill(pid, 'SIGKILL');
      } catch {
        // already gone
      }
      await exited;
      fs.rmSync(dir, { recursive: true, force: true });
    },
  };
  return panel;
}

// recordingServer counts every HTTP request and WebSocket attempt it receives.
export async function recordingServer(port = 0, host = '127.0.0.1') {
  const arrivals = [];
  const server = http.createServer((req, res) => {
    arrivals.push(`${req.method} ${req.url}`);
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('recording server');
  });
  server.on('upgrade', (req, socket) => {
    arrivals.push(`UPGRADE ${req.url}`);
    socket.destroy();
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, host, resolve);
  });
  return {
    port: server.address().port,
    arrivals,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

// recordingProxy answers absolute-form requests and CONNECT, recording each target.
export async function recordingProxy() {
  const arrivals = [];
  const server = http.createServer((req, res) => {
    arrivals.push(`${req.method} ${req.url}`);
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('recording proxy');
  });
  server.on('connect', (req, socket) => {
    arrivals.push(`CONNECT ${req.url}`);
    socket.destroy();
  });
  server.on('upgrade', (req, socket) => {
    arrivals.push(`UPGRADE ${req.url}`);
    socket.destroy();
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return {
    port: server.address().port,
    arrivals,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

// squatter listens on [::1]:0 and counts connections (S1).
export async function squatter() {
  let accepted = 0;
  const server = net.createServer((socket) => {
    accepted += 1;
    socket.destroy();
  });
  await new Promise((resolve) => server.listen(0, '::1', resolve));
  return {
    port: server.address().port,
    accepted: () => accepted,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

// secretForms are the searched encodings of a secret (raw, base64url, base64).
export function secretForms(secret) {
  const raw = Buffer.from(secret);
  return [secret, raw.toString('base64url'), raw.toString('base64').replace(/=+$/, '')];
}

export function countSecret(text, secret) {
  let n = 0;
  for (const form of secretForms(secret)) {
    n += text.split(form).length - 1;
  }
  return n;
}

export { sleep, until };
