import {
  waiverExpired,
  isWaived,
  activeWaivedNames,
  expiringWaivers,
  futureWaiverDeadlineMs,
  soonestDeadlineDelayMs,
} from './waivers';
import { Waiver } from './models';

// waivers.ts decides which checks are excluded from the compliance score based
// on Waiver.expiresAt, a string carried in the CR that a user (or a hand-edit)
// controls. The security-relevant invariant its comments promise: an unparseable
// or corrupt expiresAt is treated as EXPIRED, so a garbage date can never grant a
// permanent score-suppressing waiver. Nothing pins that under hostile input. This
// is a fuzz sweep over the expiry corpus asserting throw-safety and, above all,
// that no malformed expiresAt is ever treated as active.

// Hostile expiresAt strings: empty, control/unicode bytes, calendar overflow,
// non-ISO junk, huge/negative/near-max dates, and numeric-looking noise. new
// Date(x).getTime() yields NaN for most of these; the code must fold NaN to
// "expired", never "active".
const HOSTILE_EXPIRY = [
  '',
  ' ',
  '\0',
  'not-a-date',
  '2026-02-31T00:00:00Z', // invalid calendar day
  '2026-13-01T00:00:00Z',
  '9999-99-99',
  '0000-01-01T00:00:00Z',
  '275760-09-14T00:00:00Z', // past max representable Date
  'Infinity',
  'NaN',
  'true',
  '日本語',
  '🙂',
  'x'.repeat(1000),
  '2026-01-01', // date-only, parseable
  '2999-01-01T00:00:00Z', // far future, parseable and active
  '1970-01-01T00:00:00Z', // epoch, long expired
];

const NOW = new Date('2026-07-13T00:00:00Z');
const asWaiver = (expiresAt?: string): Waiver => ({ name: 'check-1', expiresAt });

describe('waivers throw-safety and no-permanent-grant (fuzz sweep)', () => {
  for (const expiry of HOSTILE_EXPIRY) {
    const label = JSON.stringify(expiry).slice(0, 40);
    const w = asWaiver(expiry);

    it(`never throws and never treats corrupt expiry as active for ${label}`, () => {
      let expired: unknown;
      expect(() => {
        expired = waiverExpired(w, NOW);
      }).not.toThrow();
      expect(typeof expired).toBe('boolean');

      const parseable = !Number.isNaN(new Date(expiry).getTime());
      // A truthy-but-unparseable expiresAt must fold to expired => not waived, not
      // in the active set, not surfaced as expiring. A corrupt date grants nothing.
      // (A falsy expiresAt like '' means "no expiry set" = permanent, same as
      // undefined; that intentional branch is asserted separately below.)
      if (expiry && !parseable) {
        expect(expired).toBe(true);
        expect(isWaived('check-1', [w], NOW)).toBe(false);
        expect(activeWaivedNames([w], NOW).has('check-1')).toBe(false);
        expect(expiringWaivers([w], 365 * 24 * 3600 * 1000, NOW)).toHaveLength(0);
      }
    });
  }

  it('activeWaivedNames and isWaived agree, and only future-dated waivers are active', () => {
    const waivers = HOSTILE_EXPIRY.map((e, i) => ({ name: `c-${i}`, expiresAt: e }));
    const active = activeWaivedNames(waivers, NOW);
    for (const w of waivers) {
      expect(isWaived(w.name, waivers, NOW)).toBe(active.has(w.name));
      if (active.has(w.name)) {
        // Active => either no expiry set (falsy = permanent) OR a parseable date
        // strictly in the future. A truthy-unparseable date is never active.
        if (w.expiresAt) {
          const t = new Date(w.expiresAt).getTime();
          expect(Number.isNaN(t)).toBe(false);
          expect(t).toBeGreaterThan(NOW.getTime());
        }
      }
    }
  });

  it('isWaived agrees with activeWaivedNames on duplicate names (any active entry wins)', () => {
    // spec.waivers is listType=map keyed on name so the apiserver forbids dupes,
    // but the helpers must still agree defensively: a name is waived if ANY entry
    // is active, regardless of array order.
    const expiredFirst: Waiver[] = [
      { name: 'x', expiresAt: '2000-01-01T00:00:00Z' }, // expired, listed first
      { name: 'x', expiresAt: '3000-01-01T00:00:00Z' }, // active
    ];
    expect(isWaived('x', expiredFirst, NOW)).toBe(true);
    expect(activeWaivedNames(expiredFirst, NOW).has('x')).toBe(true);
    // Reverse order: same answer.
    const activeFirst = [...expiredFirst].reverse();
    expect(isWaived('x', activeFirst, NOW)).toBe(true);
    // All duplicates expired -> not waived.
    const bothExpired: Waiver[] = [
      { name: 'y', expiresAt: '2000-01-01T00:00:00Z' },
      { name: 'y', expiresAt: '2001-01-01T00:00:00Z' },
    ];
    expect(isWaived('y', bothExpired, NOW)).toBe(false);
    expect(activeWaivedNames(bothExpired, NOW).has('y')).toBe(false);
  });

  it('expiringWaivers only returns waivers strictly inside the window', () => {
    const waivers = HOSTILE_EXPIRY.map((e, i) => ({ name: `c-${i}`, expiresAt: e }));
    const windowMs = 24 * 3600 * 1000;
    for (const w of expiringWaivers(waivers, windowMs, NOW)) {
      const t = new Date(w.expiresAt!).getTime();
      expect(t).toBeGreaterThan(NOW.getTime());
      expect(t).toBeLessThanOrEqual(NOW.getTime() + windowMs);
    }
  });

  it('expiresAt exactly equal to now is expired, not active (== now boundary)', () => {
    // Lockstep with the operator aggregate predicate !ExpiresAt.After(now):
    // equality counts as expired on both sides. A waiver whose deadline is the
    // current instant no longer excludes its check.
    const w = asWaiver(NOW.toISOString());
    expect(waiverExpired(w, NOW)).toBe(true);
    expect(isWaived('check-1', [w], NOW)).toBe(false);
    // One millisecond into the future is still active.
    const future = asWaiver(new Date(NOW.getTime() + 1).toISOString());
    expect(waiverExpired(future, NOW)).toBe(false);
    expect(isWaived('check-1', [future], NOW)).toBe(true);
  });

  it('a missing (undefined) expiresAt is never expired but also never expiring', () => {
    const w = asWaiver(undefined);
    expect(waiverExpired(w, NOW)).toBe(false);
    expect(isWaived('check-1', [w], NOW)).toBe(true);
    expect(expiringWaivers([w], 365 * 24 * 3600 * 1000, NOW)).toHaveLength(0);
  });

  it('soonestDeadlineDelayMs pads, floors, and caps setTimeout delays', () => {
    const now = NOW.getTime();
    expect(soonestDeadlineDelayMs(now, [])).toBe(0);
    expect(soonestDeadlineDelayMs(now, [now - 1, Number.NaN])).toBe(0);
    // Pad +25ms past the deadline so callers observe t <= now after the tick.
    expect(soonestDeadlineDelayMs(now, [now + 100])).toBe(125);
    // Floor at 25ms when the deadline is already in the past-relative gap.
    expect(soonestDeadlineDelayMs(now, [now + 1])).toBe(26);
    // Cap at signed-32-bit setTimeout max.
    expect(soonestDeadlineDelayMs(now, [now + 3_000_000_000])).toBe(2_147_483_647);
  });

  it('futureWaiverDeadlineMs includes expiry and positive future offsets only', () => {
    const now = NOW.getTime();
    const week = 7 * 24 * 3600 * 1000;
    const far = new Date(now + 30 * 24 * 3600 * 1000).toISOString();
    const near = new Date(now + week).toISOString();
    const waivers: Waiver[] = [
      { name: 'a', expiresAt: far },
      { name: 'b', expiresAt: near },
      { name: 'c', expiresAt: 'not-a-date' },
      { name: 'd' },
    ];
    const plain = futureWaiverDeadlineMs(waivers, now);
    expect(plain).toHaveLength(2);
    expect(plain).toContain(new Date(far).getTime());
    expect(plain).toContain(new Date(near).getTime());
    // -14d on far is still future; on near is past and dropped.
    const withOffset = futureWaiverDeadlineMs(waivers, now, [-2 * week]);
    expect(withOffset).toContain(new Date(far).getTime() - 2 * week);
    expect(withOffset).not.toContain(new Date(near).getTime() - 2 * week);
  });
});
