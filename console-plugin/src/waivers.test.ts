import {
  waiverExpired,
  findWaiver,
  isWaived,
  activeWaivedNames,
  expiringWaivers,
  futureWaiverDeadlineMs,
  soonestDeadlineDelayMs,
} from './waivers';
import { addWaiverPatch, removeWaiverPatch } from './patches';
import { effectiveStatus, resultFilterStatus } from './status';
import { resultsHref } from './links';
import { Waiver } from './models';

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

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

  it('expiringWaivers returns waivers in (now, now+window], excluding past/beyond/permanent', () => {
    const windowMs = 24 * 3600 * 1000;
    const at = (offsetMs: number) => new Date(NOW.getTime() + offsetMs).toISOString();
    const waivers: Waiver[] = [
      { name: 'in', expiresAt: at(12 * 3600 * 1000) }, // inside window
      { name: 'edge', expiresAt: at(windowMs) }, // exactly at window end (inclusive)
      { name: 'beyond', expiresAt: at(48 * 3600 * 1000) }, // past window end
      { name: 'past', expiresAt: at(-3600 * 1000) }, // already expired
      { name: 'permanent' }, // no expiry
      ...HOSTILE_EXPIRY.map((e, i) => ({ name: `c-${i}`, expiresAt: e })),
    ];
    const got = expiringWaivers(waivers, windowMs, NOW).map((w) => w.name);
    expect(got).toContain('in');
    expect(got).toContain('edge');
    expect(got).not.toContain('beyond');
    expect(got).not.toContain('past');
    expect(got).not.toContain('permanent');
    // Every returned waiver is genuinely inside (now, now+window].
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
    // Among multiple future deadlines the soonest wins regardless of order.
    expect(soonestDeadlineDelayMs(now, [now + 5000, now + 200])).toBe(225);
    expect(soonestDeadlineDelayMs(now, [now + 200, now + 5000])).toBe(225);
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
    // Missing waiver list is not an error, just no deadlines.
    expect(futureWaiverDeadlineMs(undefined, now)).toEqual([]);
    // -14d on far is still future; on near is past and dropped.
    const withOffset = futureWaiverDeadlineMs(waivers, now, [-2 * week]);
    expect(withOffset).toContain(new Date(far).getTime() - 2 * week);
    expect(withOffset).not.toContain(new Date(near).getTime() - 2 * week);
  });
});
describe('waivers', () => {
  it('isWaived matches by name', () => {
    const w = [{ name: 'a', reason: 'x' }, { name: 'b' }];
    expect(isWaived('a', w)).toBe(true);
    expect(isWaived('b', w)).toBe(true);
    expect(isWaived('c', w)).toBe(false);
    expect(isWaived('a', undefined)).toBe(false);
    expect(isWaived('a', [])).toBe(false);
    // Empty names never match (corrupt waiver entry).
    expect(isWaived('', [{ name: '' }])).toBe(false);
    expect(isWaived('', w)).toBe(false);
  });
  // Hot path for score math, CSV, and Results filters: one Set of active names.
  it('activeWaivedNames builds a Set of non-expired names only', () => {
    const now = new Date('2026-07-11T00:00:00Z');
    const set = activeWaivedNames(
      [
        { name: 'active', reason: 'r' },
        { name: 'future', expiresAt: '2026-07-12T00:00:00Z' },
        { name: 'expired', expiresAt: '2026-07-10T00:00:00Z' },
        { name: 'exact', expiresAt: now.toISOString() }, // t <= now => expired
        { name: 'bad', expiresAt: 'not-a-date' },
        { name: '' },
        { name: 'active' }, // dedupe
      ],
      now,
    );
    expect(set).toBeInstanceOf(Set);
    expect([...set].sort()).toEqual(['active', 'future']);
    expect(set.has('expired')).toBe(false);
    expect(set.has('exact')).toBe(false);
    expect(set.has('bad')).toBe(false);
    expect(set.has('')).toBe(false);
    expect(activeWaivedNames(undefined, now).size).toBe(0);
    expect(activeWaivedNames([], now).size).toBe(0);
  });
  it('resultFilterStatus maps FAIL+waiver to WAIVED for Overview drill-down fidelity', () => {
    const w = [{ name: 'f1' }];
    expect(resultFilterStatus({ metadata: { name: 'f1' }, status: 'FAIL' }, w)).toBe('WAIVED');
    expect(resultFilterStatus({ metadata: { name: 'f2' }, status: 'FAIL' }, w)).toBe('FAIL');
    // Waived PASS still scores as PASS (self-healing); filter stays PASS.
    expect(resultFilterStatus({ metadata: { name: 'f1' }, status: 'PASS' }, w)).toBe('PASS');
    expect(resultFilterStatus({ metadata: { name: 'x' }, status: 'MANUAL' }, w)).toBe('MANUAL');
    expect(resultFilterStatus({ metadata: { name: 'f1' }, status: 'FAIL' }, undefined)).toBe(
      'FAIL',
    );
    // Expired waiver must not map to WAIVED (score re-includes the FAIL).
    // resultFilterStatus does not take `now`; isWaived defaults to Date.now().
    // Use a clearly-past year so wall-clock CI drift cannot flip the chip.
    expect(
      resultFilterStatus(
        { metadata: { name: 'f1' }, status: 'FAIL' },
        [{ name: 'f1', expiresAt: '2000-01-01T00:00:00Z' }],
      ),
    ).toBe('FAIL');
    // Permanent waiver (no expiresAt) stays WAIVED without depending on clock.
    expect(
      resultFilterStatus(
        { metadata: { name: 'f1' }, status: 'FAIL' },
        [{ name: 'f1' }],
      ),
    ).toBe('WAIVED');
    // Prebuilt Set path (Results/CSV hot path): same FAIL→WAIVED mapping.
    const set = new Set(['f1']);
    expect(resultFilterStatus({ metadata: { name: 'f1' }, status: 'FAIL' }, set)).toBe('WAIVED');
    expect(resultFilterStatus({ metadata: { name: 'f2' }, status: 'FAIL' }, set)).toBe('FAIL');
    expect(resultFilterStatus({ metadata: { name: 'f1' }, status: 'PASS' }, set)).toBe('PASS');
  });
  // Filter chips use effective status: a benign INCONSISTENT is not "INCONSISTENT".
  it('resultFilterStatus collapses benign INCONSISTENT before filtering', () => {
    expect(
      resultFilterStatus({
        metadata: {
          name: 'inc',
          annotations: {
            'compliance.openshift.io/inconsistent-source': 'node0:PASS',
            'compliance.openshift.io/most-common-status': 'NOT-APPLICABLE',
          },
        },
        status: 'INCONSISTENT',
      }),
    ).toBe('PASS');
  });
  // Overview N/A deep-links must include SKIP rows (operator ResultCounts fold).
  it('resultFilterStatus folds SKIP into NOT-APPLICABLE', () => {
    expect(
      resultFilterStatus({ metadata: { name: 's1' }, status: 'SKIP' }),
    ).toBe('NOT-APPLICABLE');
  });
  // Untrusted CCR status/annotations + waiver names: filter chips must never throw;
  // FAIL+active-waiver maps to WAIVED; SKIP folds to NOT-APPLICABLE.
  it('fuzz: resultFilterStatus never throws; WAIVED only for FAIL with active waiver', () => {
    const statuses = [
      'PASS',
      'FAIL',
      'ERROR',
      'MANUAL',
      'INFO',
      'SKIP',
      'NOT-APPLICABLE',
      'INCONSISTENT',
      '',
      // Raw WAIVED from a CCR is a forged/unknown token (WAIVED is synthetic,
      // assigned only for FAIL+active waiver): folds to ERROR on both sides.
      'WAIVED',
    ];
    for (let i = 0; i < 1500; i++) {
      const status = statuses[i % statuses.length];
      const name = i % 4 === 0 ? randomString(i % 24) : `chk-${i % 17}`;
      const waivers =
        i % 5 === 0
          ? undefined
          : i % 5 === 1
            ? new Set<string>([name, `other-${i}`])
            : [{ name }, { name: `other-${i}`, expiresAt: randomString(i % 12) }];
      const r = {
        status,
        metadata: {
          name,
          annotations: {
            'compliance.openshift.io/inconsistent-source':
              i % 3 === 0 ? randomString(i % 36) : `n0:${statuses[i % statuses.length]}`,
            'compliance.openshift.io/most-common-status':
              i % 2 === 0 ? randomString(i % 10) : 'PASS',
          },
        },
      };
      let got: string;
      expect(() => {
        got = resultFilterStatus(r, waivers as never);
      }).not.toThrow();
      expect(typeof got!).toBe('string');
      if (status === 'SKIP') {
        expect(got!).toBe('NOT-APPLICABLE');
      } else if (status === '' || status === 'WAIVED') {
        // Empty and raw-WAIVED statuses map to ERROR (operator tally parity:
        // the operator fails a forged raw WAIVED closed the same way).
        expect(got!).toBe('ERROR');
      } else if (status !== 'INCONSISTENT' && status !== 'FAIL') {
        expect(got!).toBe(status);
      }
      if (got! === 'WAIVED') {
        // Only FAIL+active waiver may produce WAIVED.
        expect(effectiveStatus(r)).toBe('FAIL');
      }
    }
  });
  it('resultsHref FAIL deep-link is distinct from WAIVED', () => {
    expect(resultsHref('FAIL')).toContain('rowFilter-result-status=FAIL');
    expect(resultsHref('WAIVED')).toContain('rowFilter-result-status=WAIVED');
    expect(resultsHref('FAIL')).not.toContain('WAIVED');
  });
  it('addWaiverPatch creates the array when absent, appends when present', () => {
    expect(addWaiverPatch(undefined, { name: 'chk', reason: 'risk' })).toEqual([
      { op: 'add', path: '/spec/waivers', value: [{ name: 'chk', reason: 'risk' }] },
    ]);
    expect(addWaiverPatch(null, { name: 'chk', reason: 'risk' })).toEqual([
      { op: 'add', path: '/spec/waivers', value: [{ name: 'chk', reason: 'risk' }] },
    ]);
    // Empty array still exists after the last remove: must append with "/-".
    expect(addWaiverPatch([], { name: 'chk' })).toEqual([
      { op: 'add', path: '/spec/waivers/-', value: { name: 'chk' } },
    ]);
    expect(addWaiverPatch([{ name: 'other' }], { name: 'chk' })).toEqual([
      { op: 'add', path: '/spec/waivers/-', value: { name: 'chk' } },
    ]);
  });
  it('addWaiverPatch carries governance fields, dropping empty ones', () => {
    expect(
      addWaiverPatch(undefined, {
        name: 'chk',
        reason: 'risk',
        requestedBy: 'alice',
        approvedBy: '',
        expiresAt: '2027-01-01T00:00:00Z',
        reviewBy: '2026-12-01T00:00:00Z',
      }),
    ).toEqual([
      {
        op: 'add',
        path: '/spec/waivers',
        value: [
          {
            name: 'chk',
            reason: 'risk',
            requestedBy: 'alice',
            expiresAt: '2027-01-01T00:00:00Z',
            reviewBy: '2026-12-01T00:00:00Z',
          },
        ],
      },
    ]);
    // Non-empty approvedBy is retained (not dropped with the empty-string path).
    expect(
      addWaiverPatch(undefined, {
        name: 'chk2',
        reason: 'risk',
        approvedBy: 'bob',
      }),
    ).toEqual([
      {
        op: 'add',
        path: '/spec/waivers',
        value: [{ name: 'chk2', reason: 'risk', approvedBy: 'bob' }],
      },
    ]);
    // Whitespace-only optional text is empty; surrounding whitespace is trimmed.
    expect(
      addWaiverPatch(undefined, {
        name: 'chk3',
        reason: '  padded  ',
        requestedBy: '   ',
        approvedBy: '\t',
      }),
    ).toEqual([
      {
        op: 'add',
        path: '/spec/waivers',
        value: [{ name: 'chk3', reason: 'padded' }],
      },
    ]);
  });
  it('addWaiverPatch replaces an existing entry instead of duplicating', () => {
    expect(addWaiverPatch([{ name: 'chk', reason: 'old' }], { name: 'chk', reason: 'new' })).toEqual(
      [
        { op: 'test', path: '/spec/waivers/0/name', value: 'chk' },
        { op: 'replace', path: '/spec/waivers/0', value: { name: 'chk', reason: 'new' } },
      ],
    );
  });
  it('addWaiverPatch is a no-op for empty or non-DNS-1123 names', () => {
    expect(addWaiverPatch(undefined, { name: '', reason: 'x' })).toEqual([]);
    expect(addWaiverPatch([], { name: '' })).toEqual([]);
    // CRD Pattern on waiver name (DNS-1123 subdomain).
    expect(addWaiverPatch(undefined, { name: 'Bad_Name' })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'UPPER' })).toEqual([]);
  });
  // CRD MaxLength bounds: reject over-long fields client-side so admission is not
  // the first (and opaque) failure mode.
  it('addWaiverPatch is a no-op when fields exceed CRD MaxLength', () => {
    expect(addWaiverPatch(undefined, { name: 'a'.repeat(254) })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', reason: 'r'.repeat(1025) })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', requestedBy: 'u'.repeat(254) })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', approvedBy: 'u'.repeat(254) })).toEqual([]);
    // Boundary values still produce a patch.
    expect(addWaiverPatch(undefined, { name: 'a'.repeat(253) })).toEqual([
      { op: 'add', path: '/spec/waivers', value: [{ name: 'a'.repeat(253) }] },
    ]);
  });
  // expiresAt/reviewBy must be RFC3339 (metav1.Time); free-form Date.parse
  // successes and invalid calendar days fail closed before admission.
  it('addWaiverPatch is a no-op for unparseable expiresAt or reviewBy', () => {
    expect(addWaiverPatch(undefined, { name: 'chk', expiresAt: 'not-a-date' })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', reviewBy: 'tomorrow' })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', expiresAt: 'March 1, 2026' })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', expiresAt: '01/02/2026' })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', expiresAt: '2026-01-01' })).toEqual([]);
    expect(addWaiverPatch(undefined, { name: 'chk', expiresAt: '2026-02-31T00:00:00Z' })).toEqual(
      [],
    );
    expect(
      addWaiverPatch(undefined, { name: 'chk', expiresAt: '2027-01-01T00:00:00Z' }),
    ).toEqual([
      {
        op: 'add',
        path: '/spec/waivers',
        value: [{ name: 'chk', expiresAt: '2027-01-01T00:00:00Z' }],
      },
    ]);
    expect(
      addWaiverPatch(undefined, { name: 'chk', reviewBy: '2027-06-15T23:59:59.999Z' }),
    ).toEqual([
      {
        op: 'add',
        path: '/spec/waivers',
        value: [{ name: 'chk', reviewBy: '2027-06-15T23:59:59.999Z' }],
      },
    ]);
  });
  it('addWaiverPatch refuses a new entry past CRD MaxItems=256 (replace still works)', () => {
    const full = Array.from({ length: 256 }, (_, i) => ({ name: `w-${i}` }));
    expect(addWaiverPatch(full, { name: 'w-new' })).toEqual([]);
    expect(addWaiverPatch(full, { name: 'w-0', reason: 'updated' })).toEqual([
      { op: 'test', path: '/spec/waivers/0/name', value: 'w-0' },
      { op: 'replace', path: '/spec/waivers/0', value: { name: 'w-0', reason: 'updated' } },
    ]);
  });
  it('waiverExpired / isWaived respect expiry', () => {
    const now = new Date('2026-07-11T00:00:00Z');
    const past = { name: 'a', expiresAt: '2026-07-10T00:00:00Z' };
    const future = { name: 'b', expiresAt: '2026-07-12T00:00:00Z' };
    const none = { name: 'c' };
    const bad = { name: 'd', expiresAt: 'not-a-date' };
    // Exact equality is expired (t <= now), lockstep with operator !After(now).
    const exact = { name: 'e', expiresAt: now.toISOString() };
    expect(waiverExpired(past, now)).toBe(true);
    expect(waiverExpired(future, now)).toBe(false);
    expect(waiverExpired(none, now)).toBe(false);
    // Corrupt expiresAt must not grant a permanent waiver.
    expect(waiverExpired(bad, now)).toBe(true);
    expect(waiverExpired(exact, now)).toBe(true);
    // isWaived (excluded from score) is false for an expired waiver.
    expect(isWaived('a', [past], now)).toBe(false);
    expect(isWaived('b', [future], now)).toBe(true);
    expect(isWaived('c', [none], now)).toBe(true);
    expect(isWaived('d', [bad], now)).toBe(false);
    expect(isWaived('e', [exact], now)).toBe(false);
  });

  // expiresAt is CR/user text; corrupt values must never throw and must not
  // count as permanently active (NaN → expired).
  it('fuzz: waiverExpired never throws; unparseable expiresAt is expired', () => {
    const now = new Date('2026-07-11T12:00:00Z');
    for (let i = 0; i < 2000; i++) {
      const expiresAt =
        i % 5 === 0
          ? undefined
          : i % 5 === 1
            ? randomString(i % 48)
            : i % 5 === 2
              ? 'not-a-date'
              : i % 5 === 3
                ? new Date(now.getTime() + (i - 1000) * 60_000).toISOString()
                : '';
      const w = { name: 'chk', expiresAt };
      expect(() => waiverExpired(w, now)).not.toThrow();
      const expired = waiverExpired(w, now);
      expect(typeof expired).toBe('boolean');
      if (!expiresAt) {
        expect(expired).toBe(false);
        continue;
      }
      const t = new Date(expiresAt).getTime();
      if (Number.isNaN(t)) {
        expect(expired).toBe(true);
      } else {
        expect(expired).toBe(t <= now.getTime());
      }
    }
  });
  it('findWaiver returns the entry regardless of expiry', () => {
    const now = new Date('2026-07-11T00:00:00Z');
    const past = { name: 'a', expiresAt: '2026-07-10T00:00:00Z', reason: 'r' };
    expect(findWaiver('a', [past])).toEqual(past);
    expect(findWaiver('x', [past])).toBeUndefined();
    expect(isWaived('a', [past], now)).toBe(false); // expired: not excluded
  });
  it('expiringWaivers lists active waivers within the window only', () => {
    const now = new Date('2026-07-11T00:00:00Z');
    const day = 86400000;
    const soon = { name: 'soon', expiresAt: '2026-07-13T00:00:00Z' }; // in 2 days
    const later = { name: 'later', expiresAt: '2026-08-01T00:00:00Z' };
    const gone = { name: 'gone', expiresAt: '2026-07-01T00:00:00Z' }; // expired
    const perm = { name: 'perm' };
    // Corrupt expiresAt is NaN and must not appear as "expiring soon".
    const bad = { name: 'bad', expiresAt: 'not-a-date' };
    // Window edge: exactly now+withinMs is included (t <= now+withinMs).
    const edge = { name: 'edge', expiresAt: new Date(now.getTime() + 7 * day).toISOString() };
    const out = expiringWaivers([soon, later, gone, perm, bad, edge], 7 * day, now);
    expect(out.map((w) => w.name)).toEqual(['soon', 'edge']);
  });
  it('removeWaiverPatch test-guards the name before removing', () => {
    expect(removeWaiverPatch(2, 'chk')).toEqual([
      { op: 'test', path: '/spec/waivers/2/name', value: 'chk' },
      { op: 'remove', path: '/spec/waivers/2' },
    ]);
  });
  // Fail closed: a bad call site must not emit a patch that always 404s.
  it('removeWaiverPatch is a no-op for invalid index or empty name', () => {
    expect(removeWaiverPatch(-1, 'chk')).toEqual([]);
    expect(removeWaiverPatch(1.5, 'chk')).toEqual([]);
    expect(removeWaiverPatch(NaN, 'chk')).toEqual([]);
    expect(removeWaiverPatch(0, '')).toEqual([]);
  });
  it('fuzz: addWaiverPatch carries the name when DNS-1123 valid', () => {
    for (let i = 0; i < 500; i++) {
      // Force a valid DNS-1123 subdomain so we exercise the happy path; invalid
      // shapes are covered by the no-op cases above.
      const name = `chk-${i}`;
      const patch = addWaiverPatch(i % 2 === 0 ? [] : undefined, {
        name,
        reason: randomString(i % 10),
      });
      expect(patch.length).toBeGreaterThan(0);
      expect(patch[0].op === 'add' || patch[0].op === 'test').toBe(true);
      const last = patch[patch.length - 1];
      const v = last.value as { name: string } | { name: string }[];
      const entry = Array.isArray(v) ? v[0] : v;
      expect(entry.name).toBe(name);
    }
    // Invalid shapes must stay no-ops.
    for (const bad of ['', 'Bad_Name', 'A'.repeat(10), 'has space']) {
      expect(addWaiverPatch(undefined, { name: bad })).toEqual([]);
    }
  });
  // expiresAt/reviewBy free-text must fail closed: unparseable times never ship
  // a patch that would 422 at admission; emitted times stay RFC3339-shaped.
  it('fuzz: addWaiverPatch rejects unparseable expiresAt/reviewBy', () => {
    const rfc3339 =
      /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
    for (let i = 0; i < 1000; i++) {
      const name = `chk-${i}`;
      const expiresAt =
        i % 7 === 0
          ? undefined
          : i % 7 === 1
            ? '2027-01-01T00:00:00Z'
            : i % 7 === 2
              ? '2026-02-31T00:00:00Z'
              : i % 7 === 3
                ? 'March 1, 2026'
                : i % 7 === 4
                  ? '2026-01-01'
                  : i % 7 === 5
                    ? ''
                    : randomString(i % 48);
      const reviewBy =
        i % 5 === 0
          ? undefined
          : i % 5 === 1
            ? '2027-06-15T23:59:59.999Z'
            : i % 5 === 2
              ? 'tomorrow'
              : i % 5 === 3
                ? '01/02/2026'
                : randomString(i % 32);
      let patch: ReturnType<typeof addWaiverPatch>;
      expect(() => {
        patch = addWaiverPatch(undefined, { name, expiresAt, reviewBy });
      }).not.toThrow();
      if (patch!.length === 0) {
        continue;
      }
      const last = patch![patch!.length - 1];
      const v = last.value as { expiresAt?: string; reviewBy?: string };
      const entry = Array.isArray(v) ? (v as { expiresAt?: string; reviewBy?: string }[])[0] : v;
      if (entry.expiresAt) {
        expect(entry.expiresAt).toMatch(rfc3339);
      }
      if (entry.reviewBy) {
        expect(entry.reviewBy).toMatch(rfc3339);
      }
    }
  });
});
