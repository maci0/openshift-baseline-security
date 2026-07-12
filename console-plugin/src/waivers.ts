// Waiver lookup, expiry, and active-set helpers (matching operator score exclusion).
import { expiresAtMs } from './dates';
import { Waiver } from './models';

// A waiver is expired once its expiresAt is in the past; an expired waiver no
// longer excludes its check (matching the operator). Unparseable expiresAt is
// treated as expired so a corrupt hand-edit cannot grant a permanent waiver.
export const waiverExpired = (w: Waiver, now: Date = new Date()): boolean => {
  if (!w.expiresAt) {
    return false;
  }
  const t = expiresAtMs(w.expiresAt);
  return Number.isNaN(t) || t <= now.getTime();
};

// The waiver entry for a check name (regardless of expiry), or undefined.
export const findWaiver = (name: string, waivers?: Waiver[]): Waiver | undefined =>
  name ? waivers?.find((w) => w.name === name) : undefined;

// True when a check is actively waived (has a non-expired waiver), i.e. excluded
// from the score. Empty names never match. Expired waivers do not count.
// `now` is optional so callers (and the default) only construct a Date when a
// matching waiver exists; Results filters call this for every check.
export const isWaived = (name: string, waivers?: Waiver[], now?: Date): boolean => {
  const w = findWaiver(name, waivers);
  return !!w && !waiverExpired(w, now ?? new Date());
};

// Names of currently-active (non-expired) waivers as a Set, so score math and
// row filters are O(1) per check instead of scanning the waiver list each time.
// Shared by scoring, CSV export, and the Results table.
export const activeWaivedNames = (
  waivers: Waiver[] | undefined,
  now: Date = new Date(),
): Set<string> => {
  const set = new Set<string>();
  for (const w of waivers ?? []) {
    if (w.name && !waiverExpired(w, now)) {
      set.add(w.name);
    }
  }
  return set;
};

// Active waivers expiring within `withinMs` (not yet expired), for surfacing.
// Unparseable expiresAt is excluded (NaN is not finite); matches waiverExpired.
export const expiringWaivers = (
  waivers: Waiver[] | undefined,
  withinMs: number,
  now: Date = new Date(),
): Waiver[] =>
  (waivers ?? []).filter((w) => {
    if (!w.expiresAt) {
      return false;
    }
    const t = expiresAtMs(w.expiresAt);
    if (Number.isNaN(t)) {
      return false;
    }
    return t > now.getTime() && t <= now.getTime() + withinMs;
  });

// Signed 32-bit setTimeout max; larger delays wrap and fire immediately.
const MAX_TIMEOUT_MS = 2_147_483_647;
// Pad past the deadline so callers observe t <= now after the tick fires.
const TICK_PAD_MS = 25;

// setTimeout delay until the soonest future epoch-ms deadline, or 0 when none.
// Shared by Overview / Results waiver re-render clocks so pad and max cannot drift.
export const soonestDeadlineDelayMs = (
  nowMs: number,
  deadlines: readonly number[],
): number => {
  let soonest = 0;
  for (const t of deadlines) {
    if (!Number.isFinite(t) || t <= nowMs) {
      continue;
    }
    if (soonest === 0 || t < soonest) {
      soonest = t;
    }
  }
  if (soonest === 0) {
    return 0;
  }
  return Math.min(Math.max(soonest - nowMs + TICK_PAD_MS, TICK_PAD_MS), MAX_TIMEOUT_MS);
};

// Future expiresAt instants (epoch ms) for still-active waivers, plus optional
// per-expiry offsets (e.g. -14d so Overview can tick when a waiver enters the
// expiring-soon alert window). Unparseable / already-past times are omitted.
export const futureWaiverDeadlineMs = (
  waivers: Waiver[] | undefined,
  nowMs: number,
  offsetsMs: readonly number[] = [],
): number[] => {
  const out: number[] = [];
  for (const w of waivers ?? []) {
    if (!w?.expiresAt) {
      continue;
    }
    const t = expiresAtMs(w.expiresAt);
    if (Number.isNaN(t) || t <= nowMs) {
      continue;
    }
    out.push(t);
    for (const off of offsetsMs) {
      const d = t + off;
      if (Number.isFinite(d) && d > nowMs) {
        out.push(d);
      }
    }
  }
  return out;
};
