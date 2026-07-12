import { chromium, FullConfig } from '@playwright/test';
import { chmod, mkdir } from 'fs/promises';

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
  await page.context().storageState({ path: statePath });
  // Session cookies: owner-only (gitignored path; still tighten on shared hosts).
  await chmod(statePath, 0o600);
  await browser.close();
}
