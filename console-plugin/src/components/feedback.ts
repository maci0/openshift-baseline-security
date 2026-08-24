// Shared UI timing for transient success banners (rescan, waive, apply, profile).
import * as React from 'react';

// One value so tabs cannot drift on dismiss cadence.
export const SUCCESS_DISMISS_MS = 8000;

// Auto-clear transient feedback after SUCCESS_DISMISS_MS. `active` gates on the
// banner being present; `blocked` holds dismissal while a related error must
// stay (the timer is cleared when blocked flips true). Shared by every tab so
// the effect and its cleanup cannot drift; `dismiss` is read through a ref so
// callers can pass an inline setState closure without re-arming the timer.
export const useAutoDismiss = <T>(active: T, blocked: boolean, dismiss: () => void): void => {
  const dismissRef = React.useRef(dismiss);
  React.useEffect(() => {
    dismissRef.current = dismiss;
  }, [dismiss]);
  React.useEffect(() => {
    if (!active || blocked) return;
    const id = window.setTimeout(() => dismissRef.current(), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [active, blocked]);
};
