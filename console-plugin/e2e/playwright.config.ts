import path from 'path';
import { defineConfig } from '@playwright/test';
import { loadDotEnv } from './dotenv';

// console-plugin/.env if present (never committed). Non-empty process env wins;
// an unknown or malformed line fails here rather than as a missing value later.
loadDotEnv(path.resolve(__dirname, '../.env'));

// E2E against a live OpenShift console. Configure via env (see .env.example):
//   CONSOLE_URL          console base URL (required, absolute http(s) URL; no userinfo)
//   KUBEADMIN_USER       login user (default: kubeadmin)
//   KUBEADMIN_PASSWORD   login password (required)
//   SCREENSHOT_DIR       where spec screenshots are written (default: ../docs/screenshots)
// Optional: copy .env.example to .env (gitignored); yarn test-e2e loads it.
const consoleURL = (process.env.CONSOLE_URL ?? '').trim();
if (!consoleURL) {
  throw new Error('CONSOLE_URL must be set (see console-plugin/.env.example)');
}
try {
  const u = new URL(consoleURL);
  if (u.protocol !== 'http:' && u.protocol !== 'https:') {
    throw new Error('protocol');
  }
  // Credentials belong in KUBEADMIN_*; userinfo would leak into logs/baseURL.
  if (u.username || u.password) {
    throw new Error('userinfo');
  }
} catch (e) {
  if (e instanceof Error && e.message === 'userinfo') {
    throw new Error(
      'CONSOLE_URL must not embed credentials; set KUBEADMIN_USER / KUBEADMIN_PASSWORD (see console-plugin/.env.example)',
    );
  }
  throw new Error(
    'CONSOLE_URL must be an absolute http(s) URL (see console-plugin/.env.example)',
  );
}
process.env.CONSOLE_URL = consoleURL;

// Fail fast here (not only in global-setup) so a missing password does not
// launch browsers or leave a half-written auth state.
const kubePassword = (process.env.KUBEADMIN_PASSWORD ?? '').trim();
if (!kubePassword) {
  throw new Error(
    'KUBEADMIN_PASSWORD must be set (see console-plugin/.env.example)',
  );
}
process.env.KUBEADMIN_PASSWORD = kubePassword;
const kubeUser = (process.env.KUBEADMIN_USER ?? '').trim();
if (kubeUser) {
  process.env.KUBEADMIN_USER = kubeUser;
} else {
  delete process.env.KUBEADMIN_USER;
}

// Empty/whitespace SCREENSHOT_DIR means default (helpers.ts); drop so empty
// string is never treated as a relative write path.
const screenshotDir = (process.env.SCREENSHOT_DIR ?? '').trim();
if (screenshotDir) {
  process.env.SCREENSHOT_DIR = screenshotDir;
} else {
  delete process.env.SCREENSHOT_DIR;
}

export default defineConfig({
  testDir: '.',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  globalSetup: './global-setup.ts',
  use: {
    baseURL: consoleURL,
    ignoreHTTPSErrors: true,
    storageState: 'e2e/.auth/state.json',
    viewport: { width: 1600, height: 900 },
    screenshot: 'only-on-failure',
    // docs/screenshots/ holds committed build outputs, so a capture must not
    // depend on the runner: force reduced motion so a spinner or fade caught
    // mid-frame cannot make two runs of the same page differ, and pin the pixel
    // density instead of inheriting the host display's. reducedMotion moved
    // under contextOptions in Playwright 1.62 (`use.reducedMotion` is rejected
    // now), and `animations`/`caret` were never `use` options: they are
    // page.screenshot options, so `shot()` passes them at the capture site.
    contextOptions: { reducedMotion: 'reduce' },
    deviceScaleFactor: 1,
  },
});
