// The prototype job's Playwright cases (packet FA2-T8 6.5): one project, chromium, no
// retries, against the built prototype served by vite preview on port 4317. Chromium's
// software WebGL is allowed so that the R2 candidate (sigma, WebGL2) can draw on a runner
// without a GPU.
import { fileURLToPath } from 'node:url';
import { defineConfig } from '@playwright/test';

const web = fileURLToPath(new URL('../..', import.meta.url));

export default defineConfig({
  testDir: '.',
  testMatch: '*.e2e.js',
  // Playwright's own failure records stay inside web/prototype (git-ignored there).
  outputDir: '../test-results',
  retries: 0,
  workers: 1,
  fullyParallel: false,
  timeout: 900_000,
  use: {
    baseURL: 'http://localhost:4317',
    actionTimeout: 20_000,
    viewport: { width: 1440, height: 900 },
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium', launchOptions: { args: ['--enable-unsafe-swiftshader', '--use-angle=swiftshader'] } },
    },
  ],
  webServer: {
    command: 'npx vite preview --config prototype/vite.config.js --port 4317 --strictPort',
    cwd: web,
    url: 'http://localhost:4317/index.html',
    // GitHub Actions sets CI, so the job always starts its own server.
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
