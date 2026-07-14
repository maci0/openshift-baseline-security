import { test, expect, Page } from '@playwright/test';
import { gotoTab, shot } from './helpers';

// Dark-theme screenshots + a smoke assertion that the plugin renders on the
// console's dark theme. The console applies PatternFly's dark theme by toggling
// `pf-v6-theme-dark` on <html>; force it so the run does not depend on the test
// user's saved theme preference.
const forceDark = (page: Page) =>
  page.evaluate(() => document.documentElement.classList.add('pf-v6-theme-dark'));

test.describe('Baseline Security dark theme', () => {
  test('Overview renders on the dark theme', async ({ page }) => {
    await gotoTab(page, '');
    await forceDark(page);
    // The page background is dark (near-black), proving the theme applied.
    const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    const [r, g, b, a = 1] = bg.match(/[\d.]+/g)!.map(Number);
    // A transparent body (rgba(...,0)) would pass r+g+b<150 vacuously; require the
    // background to actually be opaque before trusting its darkness.
    expect(a).toBeGreaterThan(0.5);
    expect(r + g + b).toBeLessThan(150);
    // Core content still visible.
    await expect(page.getByText('of 100', { exact: true })).toBeVisible();
    await expect(page.getByText(/^Inconsistent \(\d+\)$/)).toBeVisible();
    await shot(page, 'overview-dark');
  });

  test('Results and a check modal render on the dark theme', async ({ page }) => {
    await page.goto('/baseline-security/results?rowFilter-result-status=INCONSISTENT', {
      waitUntil: 'domcontentloaded',
    });
    await forceDark(page);
    // Only genuine PASS-vs-FAIL node splits stay INCONSISTENT after the operator
    // collapses benign PASS/NOT-APPLICABLE ones. Avoid "file": it also matches
    // the "Profile" column-header button.
    await page
      .getByRole('button', { name: /audit|directory|access|log/i })
      .first()
      .click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('Per-node results')).toBeVisible();
    await shot(page, 'inconsistent-drilldown-dark');
  });

  test('Profiles and Remediations render on the dark theme', async ({ page }) => {
    await gotoTab(page, '/profiles');
    await forceDark(page);
    await expect(page.getByText('CIS', { exact: true })).toBeVisible();
    await shot(page, 'profiles-dark');
    await gotoTab(page, '/remediations');
    await forceDark(page);
    await expect(
      page.getByText(/Node remediations render into MachineConfigs/i),
    ).toBeVisible();
    await shot(page, 'remediations-dark');
  });
});
