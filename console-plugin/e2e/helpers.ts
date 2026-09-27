import { expect, Page } from '@playwright/test';
import * as path from 'path';

// Screenshots double as the docs assets; SCREENSHOT_DIR points at docs/screenshots.
// Empty/whitespace env is treated as unset (same as missing) so a blank export
// does not write screenshots into the process cwd.
const SHOT_DIR =
  (process.env.SCREENSHOT_DIR ?? '').trim() ||
  path.resolve(__dirname, '../../docs/screenshots');

// Viewport rectangle, the shape getBoundingClientRect reports.
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
  right: number;
}

// The account control is the right-most interactive element of the masthead:
// every console build puts it last, with the notification and help controls to
// its left. Selecting by position rather than by class or test id keeps this
// working across console versions, whose masthead markup is not ours to pin.
// Returns an index into the queried list, or -1 when there is nothing to hide
// (caller then covers the whole masthead rather than capture a bare username).
export const pickAccountControl = (controls: readonly Rect[]): number => {
  let best = -1;
  let bestRight = 0;
  for (let i = 0; i < controls.length; i++) {
    const c = controls[i];
    // Collapsed or hidden controls cannot be the account chip, and a
    // zero-width one never wins on position anyway.
    if (c.width <= 0 || c.height <= 0) {
      continue;
    }
    if (best < 0 || c.right > bestRight) {
      best = i;
      bestRight = c.right;
    }
  }
  return best;
};

// PatternFly masthead first, then the ARIA banner, then any header: the console
// renders its own masthead markup, so the fallbacks matter.
const MASTHEAD_SELECTOR = '.pf-v6-c-masthead, [role="banner"], header';
const CONTROL_SELECTOR = 'button, a[href], [role="button"]';

// Rectangles of every masthead control, measured in the page. Empty when there
// is no masthead: pickAccountControl then yields -1, and the caller finds
// nothing to hide, which is correct because the identity chip lives in it.
const mastheadControlRects = (page: Page): Promise<Rect[]> =>
  page.evaluate(
    ([mastheadSel, controlSel]) => {
      const masthead = document.querySelector(mastheadSel);
      if (!masthead) {
        return [];
      }
      return Array.from(masthead.querySelectorAll(controlSel)).map((el) => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right };
      });
    },
    [MASTHEAD_SELECTOR, CONTROL_SELECTOR],
  );

// Hide or restore the account control. visibility keeps the layout intact, so
// the masthead does not reflow around a gap while the shot is taken, and the
// masthead's own background shows through where the chip was. Restoring to the
// empty inline value is exact: nothing else writes inline visibility here.
const setAccountVisibility = (
  page: Page,
  index: number,
  visibility: 'hidden' | '',
): Promise<void> =>
  page.evaluate(
    // One object argument, not an array: the array form infers
    // `(string | number)[]` for the parameter, which no `evaluate` overload
    // accepts, and nothing the callback needs is a type assertion.
    ({
      mastheadSel,
      controlSel,
      target,
      vis,
    }: {
      mastheadSel: string;
      controlSel: string;
      target: number;
      vis: 'hidden' | '';
    }) => {
      const masthead = document.querySelector(mastheadSel);
      if (!masthead) {
        return;
      }
      // -1 covers the whole masthead: an account chip the query could not
      // resolve must not reach a committed PNG.
      const el =
        target < 0 ? masthead : masthead.querySelectorAll(controlSel).item(target);
      if (el instanceof HTMLElement) {
        el.style.visibility = vis;
      }
    },
    {
      mastheadSel: MASTHEAD_SELECTOR,
      controlSel: CONTROL_SELECTOR,
      target: index,
      vis: visibility,
    },
  );

// Save a screenshot under the docs/screenshots dir, with the signed-in console
// identity hidden. The masthead chip renders the account name and its avatar
// initials, and docs/screenshots is committed: a capture taken against a
// cluster where the suite runs under a real SSO account would publish that
// person's name into the repo. Nothing in the suite needs the chip, so it is
// hidden for the capture and restored afterwards.
export const shot = async (page: Page, name: string): Promise<Buffer> => {
  const index = pickAccountControl(await mastheadControlRects(page));
  await setAccountVisibility(page, index, 'hidden');
  try {
    return await page.screenshot({
      path: path.join(SHOT_DIR, `${name}.png`),
      // Screenshot-level, not `use`: the `use` block cannot take these, and a
      // committed PNG must not depend on where a CSS transition or the caret
      // happened to be when the frame was grabbed.
      animations: 'disabled',
      caret: 'hide',
    });
  } finally {
    await setAccountVisibility(page, index, '');
  }
};

// Navigate to a Compliance tab and wait for the shared page header, so every
// test starts from a known-loaded state.
export const gotoTab = async (page: Page, subpath: string): Promise<void> => {
  await page.goto(`/baseline-security${subpath}`, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: 'Compliance', exact: true })).toBeVisible();
};
