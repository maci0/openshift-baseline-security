import * as React from 'react';

// Restore focus after a modal closes (WCAG 2.4.3), deferred a frame so the modal
// unmounts first and the backdrop does not steal it. If the trigger was removed
// while the modal was open (a batch-apply button replaced by an in-progress
// label, or an unbound row deleted), el is detached and el.focus() is a no-op
// that drops focus to <body>; fall back to a stable container so keyboard
// context is not lost. Pure DOM (no PatternFly import) so it is unit-testable.
//
// The frame is not a formality: within it the admin can activate another row
// control and open a second dialog, and an unconditional restore would pull
// focus out of that dialog behind its backdrop. The callback therefore yields
// to a dialog that was not already open, and the returned cancel lets the
// caller's effect drop a restore it no longer wants (unmount, or the open/close
// transition running again) before the frame fires.
export const restoreFocus = (
  el: HTMLElement | null,
  fallback?: React.RefObject<HTMLElement | null>,
): (() => void) => {
  // Dialogs open at schedule time. The one just closed is usually still mounted
  // for its exit transition, so it must not veto the restore; a dialog that
  // appears later did not exist when this was scheduled, and that one owns the
  // focus now.
  const openThen = openDialogs();
  const frame = window.requestAnimationFrame(() => {
    const holder = focusHolder();
    if (holder && !openThen.has(holder)) {
      return;
    }
    if (el?.isConnected) {
      el.focus();
    } else {
      fallback?.current?.focus();
    }
  });
  return () => {
    window.cancelAnimationFrame(frame);
  };
};

// openDialogs snapshots the dialogs currently mounted, as elements rather than
// a count so a re-render of the same dialog still matches.
// SAFETY: reads the document through globalThis and yields an empty set when
// the host has none, so the node test environment exercises the plain path.
const openDialogs = (): Set<Element> => {
  const doc = globalThis.document;
  if (!doc) {
    return new Set<Element>();
  }
  return new Set<Element>(doc.querySelectorAll('[role="dialog"]'));
};

// focusHolder returns the dialog element the live focus sits in, or null when
// focus is outside any dialog (or the host has no document).
const focusHolder = (): Element | null => {
  const active = globalThis.document?.activeElement;
  if (!active) {
    return null;
  }
  return active.closest('[role="dialog"]');
};
