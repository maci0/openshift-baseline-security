import { test, expect } from '@playwright/test';
import { shot } from './helpers';

// Extra screenshots of modals / states not covered by the assertion suite.

test.describe('Baseline Security screenshots', () => {
  test('check result detail modal', async ({ page }) => {
    await page.goto('/baseline-security/results', { waitUntil: 'domcontentloaded' });
    // Open the first check's detail modal (title is a link button).
    await page.getByRole('button', { name: /registries|Identity Provider|etcd|audit/i }).first().click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(page.getByText(/View full check details in OpenShift/i)).toBeVisible();
    await shot(page, 'result-detail');
  });

  test('results filtered by high severity', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-severity=high', {
      waitUntil: 'domcontentloaded',
    });
    // Applied-filter chip proves the severity filter took effect (a bare
    // "Clear all filters" check would also pass for an unrelated filter).
    const chipGroup = page.locator('.pf-v6-c-label-group, .pf-v6-c-chip-group').filter({
      hasText: /high/i,
    });
    await expect(chipGroup.first()).toBeVisible();
    await expect(page.getByRole('button', { name: /Clear all filters/i })).toBeVisible();
    await shot(page, 'results-severity-high');
  });

  test('remediation rendered-object view', async ({ page }) => {
    await page.goto('/baseline-security/remediations', { waitUntil: 'domcontentloaded' });
    const view = page.getByRole('button', { name: 'View' }).first();
    // Remediation rows arrive from a watch after domcontentloaded; wait for the
    // first row (bounded) so a slow load is not mistaken for an empty cluster.
    // A genuinely-empty cluster falls through the timeout and skips honestly.
    await view.waitFor({ timeout: 10_000 }).catch(() => {});
    // Skip (do not soft-pass) when the cluster has no remediations yet.
    test.skip((await view.count()) === 0, 'no remediations on cluster');
    await view.click();
    await expect(page.getByText('Rendered object')).toBeVisible();
    await shot(page, 'remediation-object');
  });

  test('remediation apply confirmation', async ({ page }) => {
    await page.goto('/baseline-security/remediations', { waitUntil: 'domcontentloaded' });
    // The per-row Apply button is name-scoped ("Apply <remediation-name>") so it
    // stays distinct from the "Batch apply ..." button and the modal's confirm
    // "Apply"; match the row action by that prefix.
    const apply = page.getByRole('button', { name: /^Apply \S/ }).first();
    // Wait (bounded) for the watch-delivered rows before the skip decision so a
    // slow load is not misread as an empty cluster; empty falls through and skips.
    await apply.waitFor({ timeout: 10_000 }).catch(() => {});
    // Skip (do not soft-pass) when Apply is absent; a bare pass is false confidence.
    test.skip((await apply.count()) === 0, 'no applyable remediations on cluster');
    await apply.click();
    await expect(page.getByText('Apply remediation?')).toBeVisible();
    await shot(page, 'remediation-apply');
    // Cancel: opening the confirm for a screenshot must not apply anything.
    await page.getByRole('button', { name: 'Cancel' }).click();
  });

  test('overview with a tailored profile card', async ({ page }) => {
    await page.goto('/baseline-security', { waitUntil: 'domcontentloaded' });
    // .first(): a second bound TailoredProfile would render multiple cards, and a
    // bare getByText would throw a strict-mode "resolved to N elements".
    await expect(page.getByText('cis-custom').first()).toBeVisible();
    await expect(page.getByText('Tailored').first()).toBeVisible();
    await shot(page, 'overview-tailored');
  });

  test('results filtered to a tailored profile', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-profile=tp-cis-custom', {
      waitUntil: 'domcontentloaded',
    });
    // Applied-filter chip proves the profile filter took effect (not just any
    // residual filter from a previous navigation).
    const chipGroup = page.locator('.pf-v6-c-label-group, .pf-v6-c-chip-group').filter({
      hasText: /cis-custom|tp-cis-custom/i,
    });
    await expect(chipGroup.first()).toBeVisible();
    await expect(page.getByRole('button', { name: /Clear all filters/i })).toBeVisible();
    await shot(page, 'results-tailored');
  });

  test('compliance score on the cluster Overview', async ({ page }) => {
    await page.goto('/dashboards', { waitUntil: 'domcontentloaded' });
    const score = page.getByRole('link', { name: /\d+ of 100/ });
    await expect(score).toBeVisible();
    // The injected "Compliance score" row sits low in the Details card, below the
    // default viewport fold; scroll it into view so the screenshot actually shows
    // the feature the docs caption points at (toBeVisible alone does not scroll).
    await score.scrollIntoViewIfNeeded();
    await expect(page.getByText('Compliance score')).toBeVisible();
    await shot(page, 'dashboard-score');
  });
});
