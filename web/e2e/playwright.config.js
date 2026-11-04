// browser-handshake (packet M1-T2 S15): the real server and the real web/src/auth
// module in Playwright Chromium, Firefox and WebKit. No retries: a case passes or
// fails on its first run (packet section 8).
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: '*.e2e.js',
  retries: 0,
  workers: 1,
  fullyParallel: false,
  timeout: 180_000,
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    { name: 'firefox', use: { browserName: 'firefox' } },
    { name: 'webkit', use: { browserName: 'webkit' } },
  ],
});
