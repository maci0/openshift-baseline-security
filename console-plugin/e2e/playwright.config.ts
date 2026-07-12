import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { defineConfig } from '@playwright/test';

// Only e2e knobs from .env; never inject PATH/NODE_OPTIONS/etc. into the runner.
const DOTENV_KEYS = new Set([
  'CONSOLE_URL',
  'KUBEADMIN_USER',
  'KUBEADMIN_PASSWORD',
  'SCREENSHOT_DIR',
]);

// Load console-plugin/.env if present. Non-empty process env wins (CI injects
// secrets; local .env is for convenience only). Empty/whitespace process env
// does not block .env, so a blank export cannot hide a filled .env value.
// Never commit .env. Accepts optional `export KEY=value` (shell-sourced style).
function loadDotEnv(file: string): void {
  if (!existsSync(file)) return;
  for (const line of readFileSync(file, 'utf8').split(/\r?\n/)) {
    const t = line.trim();
    if (!t || t.startsWith('#')) continue;
    const eq = t.indexOf('=');
    if (eq <= 0) continue;
    let key = t.slice(0, eq).trim();
    if (key.startsWith('export ')) {
      key = key.slice(7).trim();
    }
    if (!DOTENV_KEYS.has(key)) continue;
    const existing = process.env[key];
    if (existing !== undefined && existing.trim() !== '') continue;
    let val = t.slice(eq + 1).trim();
    if (
      (val.startsWith('"') && val.endsWith('"')) ||
      (val.startsWith("'") && val.endsWith("'"))
    ) {
      // Quoted: keep interior # and spaces (passwords may contain them).
      val = val.slice(1, -1);
    } else {
      // Unquoted: strip shell-style trailing comments (`KEY=value # note`).
      // Require a space before # so values like `pass#1` stay intact.
      const hash = val.indexOf(' #');
      if (hash >= 0) {
        val = val.slice(0, hash).trimEnd();
      }
    }
    process.env[key] = val;
  }
}

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
  },
});
