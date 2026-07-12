import { expect, Page } from '@playwright/test';
import * as path from 'path';

// Screenshots double as the docs assets; SCREENSHOT_DIR points at docs/screenshots.
// Empty/whitespace env is treated as unset (same as missing) so a blank export
// does not write screenshots into the process cwd.
const SHOT_DIR =
  (process.env.SCREENSHOT_DIR ?? '').trim() ||
  path.resolve(__dirname, '../../docs/screenshots');

// Save a screenshot under the docs/screenshots dir.
export const shot = (page: Page, name: string): Promise<Buffer> =>
  page.screenshot({ path: path.join(SHOT_DIR, `${name}.png`) });

// Navigate to a Compliance tab and wait for the shared page header, so every
// test starts from a known-loaded state.
export const gotoTab = async (page: Page, subpath: string): Promise<void> => {
  await page.goto(`/baseline-security${subpath}`, { waitUntil: 'networkidle' });
  await expect(page.getByRole('heading', { name: 'Compliance', exact: true })).toBeVisible();
};
