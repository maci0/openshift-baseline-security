import { test, expect } from '@playwright/test';
import { gotoTab } from './helpers';

// Coverage for Overview/Remediations affordances that the other specs render
// but never exercise: inline schedule editing, the score-trend card, the
// scoring-mode readout, the HTML report export, and the batch-apply confirm.
// All assertions are non-mutating: edit/confirm dialogs are opened then
// cancelled so shared cluster state is never changed.

test.describe('Baseline Security governance affordances', () => {
  test('schedule is editable inline and cancels without saving', async ({ page }) => {
    await gotoTab(page, '');
    // kubeadmin can patch clusterbaselines, so the Edit affordance renders.
    const edit = page.getByRole('button', { name: 'Edit schedule' });
    await expect(edit).toBeVisible();
    await edit.click();
    // The inline editor exposes the cron field seeded with the current value.
    const field = page.locator('#schedule-cron');
    await expect(field).toBeVisible();
    await expect(field).not.toHaveValue('');
    // Save is offered; Cancel leaves the schedule untouched.
    await expect(page.getByRole('button', { name: 'Save schedule' })).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    // Back to the read-only view: the Edit button returns, the field is gone.
    await expect(page.getByRole('button', { name: 'Edit schedule' })).toBeVisible();
    await expect(field).toHaveCount(0);
  });

  test('an invalid cron disables Save and flags the field', async ({ page }) => {
    await gotoTab(page, '');
    await page.getByRole('button', { name: 'Edit schedule' }).click();
    const field = page.locator('#schedule-cron');
    await field.fill('not a cron');
    // Client-side validation marks the field invalid and disables Save, so a
    // malformed cron can never reach the CR (no need to click a disabled button).
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(page.getByRole('button', { name: 'Save schedule' })).toBeDisabled();
    await expect(page.getByText(/5-field cron/i)).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    await expect(field).toHaveCount(0);
  });

  test('the Details card reports the scoring mode', async ({ page }) => {
    await gotoTab(page, '');
    await expect(page.getByText('Scoring mode')).toBeVisible();
    // One of the two modes must render as the value.
    await expect(
      page.getByText(/Flat \(equal weight\)|Severity-weighted/),
    ).toBeVisible();
  });

  test('the Score trend card renders on the Overview', async ({ page }) => {
    await gotoTab(page, '');
    // "Score trend" also appears as the chart's SVG <title>; scope to the card
    // title text so the assertion is unambiguous.
    await expect(
      page.locator('.pf-v6-c-card__title-text', { hasText: 'Score trend' }),
    ).toBeVisible();
  });

  test('Export HTML report produces a self-contained report document', async ({ page, context }) => {
    await gotoTab(page, '');
    const exportBtn = page.getByRole('button', { name: 'Export HTML report' });
    await expect(exportBtn).toBeEnabled();
    // The report opens in a new tab (window.open); capture it and prove it is the
    // compliance report, not a blank or error tab.
    const [popup] = await Promise.all([
      context.waitForEvent('page'),
      exportBtn.click(),
    ]);
    await popup.waitForLoadState('domcontentloaded');
    await expect(popup.getByText(/Compliance/i).first()).toBeVisible();
    await expect(popup.getByText(/of 100|Score/i).first()).toBeVisible();
    await popup.close();
  });

  test('Batch apply opens a confirmation and cancels without applying', async ({ page }) => {
    await gotoTab(page, '/remediations');
    const batch = page.getByRole('button', { name: /Batch apply \d+ node remediation/ });
    // Skip (not soft-pass) if the cluster currently has no batchable node
    // remediations; a bare pass would be false confidence.
    test.skip((await batch.count()) === 0, 'no batchable node remediations on cluster');
    await batch.click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('Batch apply node remediations?')).toBeVisible();
    // Cancel: do not mutate the cluster (no pools paused, nothing applied).
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });
});
