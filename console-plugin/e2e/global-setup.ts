import { chromium, FullConfig } from '@playwright/test';
import { chmod, mkdir } from 'fs/promises';
import { existsSync } from 'node:fs';
import { writeFile } from 'node:fs/promises';

// Logs into the OpenShift console once and saves the authenticated storage
// state so each spec starts already logged in.
export default async function globalSetup(_config: FullConfig) {
  const consoleURL = (process.env.CONSOLE_URL ?? '').trim();
  const user = (process.env.KUBEADMIN_USER ?? 'kubeadmin').trim() || 'kubeadmin';
  const password = (process.env.KUBEADMIN_PASSWORD ?? '').trim();
  if (!consoleURL || !password) {
    throw new Error(
      'CONSOLE_URL and KUBEADMIN_PASSWORD must be set (see console-plugin/.env.example)',
    );
  }

  // .yarnrc.yml sets enableScripts: false, so no install script fetches the
  // browser binaries and CI sets PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD for the same
  // reason. A clean clone therefore has no chromium until it is asked for.
  // Name the install command instead of letting chromium.launch() fail with a
  // cache-path dump.
  if (!existsSync(chromium.executablePath())) {
    throw new Error(
      'Playwright chromium is not installed; run: yarn playwright install chromium',
    );
  }

  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  await page.goto(consoleURL, { waitUntil: 'domcontentloaded' });

  // Multi-IDP clusters show a provider chooser first.
  const kubeadminLink = page.locator('a', { hasText: 'kube:admin' });
  if (await kubeadminLink.count()) {
    await kubeadminLink.click();
  }
  await page.fill('#inputUsername', user);
  await page.fill('#inputPassword', password);
  await page.click('button[type=submit]');
  await page.waitForURL('**/console-openshift-console**', { timeout: 30_000 });

  // Dismiss the guided-tour modal if it appears.
  try {
    await page.getByRole('button', { name: /skip tour/i }).click({ timeout: 5_000 });
  } catch {
    // no tour
  }

  await mkdir('e2e/.auth', { recursive: true, mode: 0o700 });
  const statePath = 'e2e/.auth/state.json';
  // The state file holds live kubeadmin session cookies. Playwright creates it
  // with the process umask (0644 on a default host) and only the chmod below
  // narrows it, so on a shared host another local user can read the session
  // for the length of that write. Create the file owner-only first: storageState
  // truncates an existing path, so it never loosens the mode, and the chmod
  // still tightens a file left behind by an older run.
  await writeFile(statePath, '', { mode: 0o600 });
  await page.context().storageState({ path: statePath });
  // Session cookies: owner-only (gitignored path; still tighten on shared hosts).
  await chmod(statePath, 0o600);
  await browser.close();
}
