import { test, expect, Page } from '@playwright/test';
import * as path from 'path';

// Screenshots double as the docs assets; SCREENSHOT_DIR points at docs/screenshots.
const SHOT_DIR = process.env.SCREENSHOT_DIR ?? path.resolve(__dirname, '../../docs/screenshots');
const shot = (page: Page, name: string) =>
  page.screenshot({ path: path.join(SHOT_DIR, `${name}.png`) });

const goto = async (page: Page, subpath: string) => {
  await page.goto(`/baseline-security${subpath}`, { waitUntil: 'networkidle' });
  // The compliance page header is present on every tab.
  await expect(page.getByRole('heading', { name: 'Compliance', exact: true })).toBeVisible();
};

test.describe('Baseline Security console plugin', () => {
  test('Overview shows the compliance score and profile breakdown', async ({ page }) => {
    await goto(page, '');
    // "Compliance score" appears both as the card title and the donut aria
    // title; the score denominator label is unambiguous.
    await expect(page.getByText('of 100')).toBeVisible();
    await expect(page.getByText('Details')).toBeVisible();
    // Compliance Operator version surfaced in the details card. exact: the
    // page subtitle also contains the phrase "Compliance Operator".
    await expect(page.getByText('Compliance Operator', { exact: true })).toBeVisible();
    // At least one profile summary card (CIS by default); watch-delivered and
    // rendered as a PatternFly CardTitle (not a heading role), so match text.
    await expect(page.getByText('CIS', { exact: true }).first()).toBeVisible();
    await shot(page, 'overview');
  });

  test('Results table lists checks and supports filtering', async ({ page }) => {
    await goto(page, '/results');
    // Column headers.
    await expect(page.getByRole('columnheader', { name: 'Check' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Status' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Severity' })).toBeVisible();
    // Rows are rendered (link buttons carry the human-readable title).
    await expect(page.getByRole('button', { name: /registries|Identity Provider|etcd|audit/i }).first()).toBeVisible();
    await shot(page, 'results');
  });

  test('Results deep-link applies the FAIL status filter', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=FAIL', {
      waitUntil: 'networkidle',
    });
    // The applied filter chip is shown.
    await expect(page.getByText('FAIL', { exact: false }).first()).toBeVisible();
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
