// Shared waiver-expiry re-render clock. Active waivers are time-sensitive:
// membership alone is not enough. A waiver can expire (or enter the
// expiring-soon window) with no CR edit, and operator status-only updates
// reallocate the spec.waivers array without changing its membership. One hook
// so the content key and reschedule cadence cannot drift between Overview and
// Results.
import * as React from 'react';
import { nowMs } from '../clock';
import { encodeKeyPart } from '../contentKey';
import { Waiver } from '../models';
import { futureWaiverDeadlineMs, soonestDeadlineDelayMs } from '../waivers';

// Content key for spec.waivers: identity deps would rebuild waiver sets (and
// reschedule the expiry timer) on every reconcile even when nothing changed.
// Encoded, not interpolated: a name or expiresAt containing the separator
// would otherwise forge another waiver set's key, leaving an expiring waiver's
// label stuck at its old value.
export const waiversContentKey = (waivers: Waiver[] | undefined): string =>
  (waivers ?? [])
    .map((w) => encodeKeyPart(w.name) + encodeKeyPart(w.expiresAt))
    .join('');

// Schedule a tick at the soonest future waiver deadline plus any per-deadline
// offsetsMs (e.g. -14d so a tab clocks when a waiver enters the expiring-soon
// alert window). Returns the content key (for memo deps) and the tick count
// (bumped once per fired deadline, so effects/memos can react to time passing).
export const useWaiverExpiryClock = (
  waivers: Waiver[] | undefined,
  offsetsMs: readonly number[] = [],
) => {
  // Memoized on the array identity: status-only CR updates reallocate
  // spec.waivers, and the key must still be rebuilt for them, but a re-render
  // driven by anything else must not re-join up to 256 encoded pairs. The
  // offsets ride along in the key so the effect below, which reads them, is
  // rescheduled when they change rather than pinning the values from whichever
  // render last moved the key.
  const key = React.useMemo(
    () => waiversContentKey(waivers) + offsetsMs.map(encodeKeyPart).join(''),
    [waivers, offsetsMs],
  );
  const [tick, setTick] = React.useState(0);
  React.useEffect(() => {
    const now = nowMs();
    const delay = soonestDeadlineDelayMs(now, futureWaiverDeadlineMs(waivers, now, offsetsMs));
    if (delay === 0) {
      return;
    }
    const id = window.setTimeout(() => setTick((c) => c + 1), delay);
    return () => window.clearTimeout(id);
    // waivers/offsets read when the content key or tick changes; the key
    // encodes both.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key + tick
  }, [key, tick]);
  return { key, tick };
};
