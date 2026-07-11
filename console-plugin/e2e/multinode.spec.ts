import { test, expect } from '@playwright/test';
import { gotoTab } from './helpers';

// Assertions that only hold on the live multi-node, multi-benchmark cluster
// (CIS + PCI-DSS enabled, a cis-custom TailoredProfile bound, worker nodes
// joined so node checks go INCONSISTENT). Complements compliance.spec.ts, which
// covers the single-profile basics.

test.describe('Baseline Security multi-node / multi-benchmark', () => {
  test('donut surfaces the Inconsistent slice from node discrepancies', async ({ page }) => {
    await gotoTab(page, '');
    // The HTML legend renders "Inconsistent (N)"; N > 0 proves node-scan
    // discrepancies are counted, not silently dropped.
    const legend = page.getByText(/^Inconsistent \(\d+\)$/);
    await expect(legend).toBeVisible();
    const text = (await legend.textContent()) ?? '';
    const n = Number(text.match(/\((\d+)\)/)?.[1] ?? '0');
    expect(n).toBeGreaterThan(0);
  });

  test('both enabled benchmarks show per-profile score cards', async ({ page }) => {
    await gotoTab(page, '');
    await expect(page.getByText('CIS', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('PCI-DSS').first()).toBeVisible();
    // Each card carries Pass/Fail/Manual rows.
    await expect(page.getByText('Pass').first()).toBeVisible();
    await expect(page.getByText('Fail').first()).toBeVisible();
  });

  test('per-profile cards list Inconsistent, matching the donut', async ({ page }) => {
    await gotoTab(page, '');
    // The card row label is exactly "Inconsistent" (the donut legend is
    // "Inconsistent (N)"), so an exact match hits the per-profile card rows.
    await expect(page.getByText('Inconsistent', { exact: true }).first()).toBeVisible();
  });

  test('a bound TailoredProfile renders a Tailored card', async ({ page }) => {
    await gotoTab(page, '');
    // The demo TailoredProfile is cis-custom, labelled with a "Tailored" badge.
    await expect(page.getByText('Tailored').first()).toBeVisible();
  });

  test('compliance score deep-links on the cluster Overview details card', async ({ page }) => {
    await page.goto('/dashboards', { waitUntil: 'networkidle' });
    await expect(page.getByText('Compliance score')).toBeVisible();
    // Rendered as a link "<n> / 100" that navigates to the Compliance page.
    const scoreLink = page.getByRole('link', { name: /\d+ \/ 100/ });
    await expect(scoreLink).toBeVisible();
    await scoreLink.click();
    await expect(page).toHaveURL(/\/baseline-security/);
  });

  test('Results filter to INCONSISTENT returns rows', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=INCONSISTENT', {
      waitUntil: 'networkidle',
    });
    await expect(page.getByRole('button', { name: /Clear all filters/i })).toBeVisible();
    // At least one INCONSISTENT status label survives the filter.
    await expect(page.getByText('INCONSISTENT', { exact: true }).first()).toBeVisible();
    // No PASS rows leak through.
    await expect(page.getByText('PASS', { exact: true })).toHaveCount(0);
  });

  test('check detail modal opens and links to the raw resource', async ({ page }) => {
    await gotoTab(page, '/results');
    await page
      .getByRole('button', { name: /registries|Identity Provider|etcd|audit|kubelet/i })
      .first()
      .click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    // Deep-link to the ComplianceCheckResult resource.
    await expect(dialog.getByText(/ComplianceCheckResult resource/i)).toBeVisible();
  });

  test('INCONSISTENT check modal shows the per-node breakdown', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=INCONSISTENT', {
      waitUntil: 'networkidle',
    });
    // Only genuine PASS-vs-FAIL node splits stay INCONSISTENT after the operator
    // collapses benign PASS/NOT-APPLICABLE ones; open the first such check.
    await page
      .getByRole('button', { name: /audit|directory|log|access|file|etcd|kubelet/i })
      .first()
      .click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('Per-node results')).toBeVisible();
    // The per-node table names at least one node.
    await expect(dialog.getByText(/cluster0-/).first()).toBeVisible();
  });

  test('a failing check offers a Waive action (with patch permission)', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=FAIL', {
      waitUntil: 'networkidle',
    });
    await page.getByRole('button', { name: /file|etcd|kubelet|api|audit|owner/i }).first().click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('Waiver')).toBeVisible();
    // kubeadmin can patch clusterbaselines, so the action is enabled (not clicked
    // to avoid mutating shared cluster state).
    await expect(dialog.getByRole('button', { name: 'Waive check' })).toBeEnabled();
  });

  test('Export CSV downloads a file of the filtered results', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=FAIL', {
      waitUntil: 'networkidle',
    });
    const exportBtn = page.getByRole('button', { name: 'Export CSV' });
    await expect(exportBtn).toBeEnabled();
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      exportBtn.click(),
    ]);
    expect(download.suggestedFilename()).toMatch(/\.csv$/);
  });
});
