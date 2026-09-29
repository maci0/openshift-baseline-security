import { expect, Page } from '@playwright/test';

// Navigate to a Compliance tab and wait for the shared page header, so every
// test starts from a known-loaded state.
export const gotoTab = async (page: Page, subpath: string): Promise<void> => {
  await page.goto(`/baseline-security${subpath}`, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: 'Compliance', exact: true })).toBeVisible();
};
