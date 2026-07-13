import * as React from 'react';

// Restore focus after a modal closes (WCAG 2.4.3), deferred a frame so the modal
// unmounts first and the backdrop does not steal it. If the trigger was removed
// while the modal was open (a batch-apply button replaced by an in-progress
// label, or an unbound row deleted), el is detached and el.focus() is a no-op
// that drops focus to <body>; fall back to a stable container so keyboard
// context is not lost. Pure DOM (no PatternFly import) so it is unit-testable.
export const restoreFocus = (
  el: HTMLElement | null,
  fallback?: React.RefObject<HTMLElement | null>,
): void => {
  window.requestAnimationFrame(() => {
    if (el?.isConnected) {
      el.focus();
    } else {
      fallback?.current?.focus();
    }
  });
};
