import { defineConfig } from '@playwright/test';

// E2E against a live OpenShift console. Configure via env (see .env.example):
//   CONSOLE_URL          console base URL (required)
//   KUBEADMIN_USER       login user (default: kubeadmin)
//   KUBEADMIN_PASSWORD   login password (required)
//   SCREENSHOT_DIR       where spec screenshots are written (default: ../docs/screenshots)
// Optional local overrides: copy .env.example to .env (gitignored) and
//   set -a && . ./.env && set +a && yarn test-e2e
if (!process.env.CONSOLE_URL) {
  throw new Error('CONSOLE_URL must be set (see console-plugin/.env.example)');
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
    baseURL: process.env.CONSOLE_URL,
    ignoreHTTPSErrors: true,
    storageState: 'e2e/.auth/state.json',
    viewport: { width: 1600, height: 900 },
    screenshot: 'only-on-failure',
  },
});
