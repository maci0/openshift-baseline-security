import { test, expect } from '@playwright/test';
import { gotoTab as goto, shot } from './helpers';

test.describe('Baseline Security console plugin', () => {
  test('Overview shows the compliance score and profile breakdown', async ({ page }) => {
    await goto(page, '');
    // Composition donut center label + legend; the legend proves the
    // non-compliant slices render (not an all-green gauge).
    await expect(page.getByText('of 100')).toBeVisible();
    await expect(page.getByText(/^Fail \(\d+\)$/)).toBeVisible();
    await expect(page.getByText('Details')).toBeVisible();
    // Compliance Operator version surfaced in the details card. exact: the
    // page subtitle also contains the phrase "Compliance Operator".
    await expect(page.getByText('Compliance Operator', { exact: true })).toBeVisible();
    // At least one profile summary card (CIS by default); watch-delivered and
    // rendered as a PatternFly CardTitle (not a heading role), so match text.
    await expect(page.getByText('CIS', { exact: true }).first()).toBeVisible();
    // Recent changes (regressions) card: newly-failing / fixed since the last
    // scan. On a steady cluster it shows its empty state rather than a list.
    await expect(page.getByText('Recent changes')).toBeVisible();
    await expect(
      page.getByText(/No changes since the last scan|No previous scan to compare yet|Newly failing/),
    ).toBeVisible();
    await shot(page, 'overview');
  });

  test('Results table lists checks and supports filtering', async ({ page }) => {
    await goto(page, '/results');
    // Column headers, including the Profile column that disambiguates the same
    // rule appearing in several benchmarks.
    await expect(page.getByRole('columnheader', { name: 'Check' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Profile' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Status' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Severity' })).toBeVisible();
    // A benchmark label renders in the Profile column (PCI-DSS is distinctive).
    await expect(page.getByText('PCI-DSS', { exact: true }).first()).toBeVisible();
    // Rows are rendered (link buttons carry the human-readable title).
    await expect(page.getByRole('button', { name: /registries|Identity Provider|etcd|audit/i }).first()).toBeVisible();
    // CSV export button present.
    await expect(page.getByRole('button', { name: 'Export CSV' })).toBeVisible();
    await shot(page, 'results');
  });

  test('Results deep-link applies the FAIL status filter', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=FAIL', {
      waitUntil: 'networkidle',
    });
    // The applied-filter chip proves the filter took effect (a bare "FAIL" text
    // check would pass on an unfiltered table via the status column labels).
    const chipGroup = page.locator('.pf-v6-c-label-group, .pf-v6-c-chip-group').filter({
      hasText: 'FAIL',
    });
    await expect(chipGroup.first()).toBeVisible();
    await expect(page.getByRole('button', { name: /Clear all filters/i })).toBeVisible();
    // And no PASS rows survive the filter.
    await expect(page.getByText('PASS', { exact: true })).toHaveCount(0);
    await shot(page, 'results-fail-filter');
  });

  test('Remediations tab renders the warning and table', async ({ page }) => {
    await goto(page, '/remediations');
    await expect(
      page.getByText(/Node remediations render into MachineConfigs/i),
    ).toBeVisible();
    await expect(page.getByText('Auto-apply remediations after each scan')).toBeVisible();
    await shot(page, 'remediations');
  });

  test('Profiles tab shows the selectable benchmark catalog', async ({ page }) => {
    await goto(page, '/profiles');
    await expect(page.getByText('CIS', { exact: true })).toBeVisible();
    await expect(page.getByText('PCI-DSS')).toBeVisible();
    await expect(page.getByText('DISA STIG')).toBeVisible();
    await shot(page, 'profiles');
  });

  test('New tailored profile rejects an invalid name', async ({ page }) => {
    await goto(page, '/profiles');
    const newBtn = page.getByRole('button', { name: 'New tailored profile' });
    // The authoring control is gated on the tailoredprofiles create permission;
    // kubeadmin has it, so the button is present.
    await expect(newBtn).toBeVisible();
    await newBtn.click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    const create = dialog.getByRole('button', { name: 'Create and bind' });
    // Empty name: Create disabled.
    await expect(create).toBeDisabled();
    // Invalid name (spaces/uppercase): inline error + Create stays disabled.
    await dialog.getByRole('textbox').first().fill('Not Valid');
    await expect(
      dialog.getByText(/Use lowercase letters, digits/i),
    ).toBeVisible();
    await expect(create).toBeDisabled();
    // Valid name: Create enabled (not clicked, to avoid mutating the cluster).
    await dialog.getByRole('textbox').first().fill('e2e-valid-name');
    await expect(create).toBeEnabled();
  });

  test('Compliance is reachable under the Administration nav section', async ({ page }) => {
    await page.goto('/dashboards', { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: 'Administration' }).click();
    const complianceNav = page.getByRole('link', { name: 'Compliance' });
    await expect(complianceNav).toBeVisible();
    await shot(page, 'nav-administration');
    await complianceNav.click();
    await expect(page).toHaveURL(/\/baseline-security/);
  });
});
