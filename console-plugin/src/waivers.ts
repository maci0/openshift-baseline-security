import { Waiver } from './models';

// A waiver is expired once its expiresAt is in the past; an expired waiver no
// longer excludes its check (matching the operator). Unparseable expiresAt is
// treated as expired so a corrupt hand-edit cannot grant a permanent waiver.
export const waiverExpired = (w: Waiver, now: Date = new Date()): boolean => {
  if (!w.expiresAt) {
    return false;
  }
  const t = new Date(w.expiresAt).getTime();
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
    const t = new Date(w.expiresAt).getTime();
    if (Number.isNaN(t)) {
      return false;
    }
    return t > now.getTime() && t <= now.getTime() + withinMs;
  });
