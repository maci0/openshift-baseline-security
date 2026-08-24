// Shared waiver-expiry re-render clock. Active waivers are time-sensitive:
// membership alone is not enough. A waiver can expire (or enter the
// expiring-soon window) with no CR edit, and operator status-only updates
// reallocate the spec.waivers array without changing its membership. One hook
// so the content key and reschedule cadence cannot drift between Overview and
// Results.
import * as React from 'react';
import { Waiver } from '../models';
import { futureWaiverDeadlineMs, soonestDeadlineDelayMs } from '../waivers';

// Content key for spec.waivers: identity deps would rebuild waiver sets (and
// reschedule the expiry timer) on every reconcile even when nothing changed.
export const waiversContentKey = (waivers: Waiver[] | undefined): string =>
  (waivers ?? [])
    .map((w) => `${w.name ?? ''}\0${w.expiresAt ?? ''}`)
    .join('\x01');

// Content key (for memo deps) plus tick count (bumped once per fired deadline,
// so effects/memos can react to time passing).
export interface WaiverClock {
  key: string;
  tick: number;
}

// Schedule a tick at the soonest future waiver deadline plus any per-deadline
// offsetsMs (e.g. -14d so a tab clocks when a waiver enters the expiring-soon
// alert window). Returns the content key (for memo deps) and the tick count
// (bumped once per fired deadline, so effects/memos can react to time passing).
export const useWaiverExpiryClock = (
  waivers: Waiver[] | undefined,
  offsetsMs: readonly number[] = [],
): WaiverClock => {
  const key = waiversContentKey(waivers);
  const [tick, setTick] = React.useState(0);
  React.useEffect(() => {
    const now = Date.now();
    const delay = soonestDeadlineDelayMs(now, futureWaiverDeadlineMs(waivers, now, offsetsMs));
    if (delay === 0) {
      return;
    }
    const id = window.setTimeout(() => setTick((c) => c + 1), delay);
    return () => window.clearTimeout(id);
    // waivers/offsets read when the content key or tick changes
    // (content-stable + expiry).
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key + tick
  }, [key, tick]);
  return { key, tick };
};
