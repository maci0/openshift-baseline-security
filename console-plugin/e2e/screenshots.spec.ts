import { test, expect } from '@playwright/test';
import { shot } from './helpers';

// Extra screenshots of modals / states not covered by the assertion suite.

test.describe('Baseline Security screenshots', () => {
  test('check result detail modal', async ({ page }) => {
    await page.goto('/baseline-security/results', { waitUntil: 'networkidle' });
    // Open the first check's detail modal (title is a link button).
    await page.getByRole('button', { name: /registries|Identity Provider|etcd|audit/i }).first().click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(page.getByText(/ComplianceCheckResult resource/i)).toBeVisible();
    await shot(page, 'result-detail');
  });

  test('results filtered by high severity', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-severity=high', {
      waitUntil: 'networkidle',
    });
    await expect(page.getByRole('button', { name: /Clear all filters/i })).toBeVisible();
    await shot(page, 'results-severity-high');
  });

  test('remediation rendered-object view', async ({ page }) => {
    await page.goto('/baseline-security/remediations', { waitUntil: 'networkidle' });
    const view = page.getByRole('button', { name: 'View' }).first();
    if (await view.count()) {
      await view.click();
      await expect(page.getByText('Rendered object')).toBeVisible();
      await shot(page, 'remediation-object');
    }
  });

  test('remediation apply confirmation', async ({ page }) => {
    await page.goto('/baseline-security/remediations', { waitUntil: 'networkidle' });
    const apply = page.getByRole('button', { name: 'Apply', exact: true }).first();
    if (await apply.count()) {
      await apply.click();
      await expect(page.getByText('Apply remediation?')).toBeVisible();
      await shot(page, 'remediation-apply');
    }
  });

  test('overview with a tailored profile card', async ({ page }) => {
    await page.goto('/baseline-security', { waitUntil: 'networkidle' });
    await expect(page.getByText('cis-custom')).toBeVisible();
    await expect(page.getByText('Tailored')).toBeVisible();
    await shot(page, 'overview-tailored');
  });

  test('results filtered to a tailored profile', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-profile=tp-cis-custom', {
      waitUntil: 'networkidle',
    });
    await expect(page.getByRole('button', { name: /Clear all filters/i })).toBeVisible();
    await shot(page, 'results-tailored');
  });

  test('compliance score on the cluster Overview', async ({ page }) => {
    await page.goto('/dashboards', { waitUntil: 'networkidle' });
    const score = page.getByRole('link', { name: /\d+ \/ 100/ });
    await expect(score).toBeVisible();
    await shot(page, 'dashboard-score');
  });
});
