import { historyContentKey, toTrendData } from './overviewTrend';
import { ScoreSnapshot } from './models';
import { mulberry32 } from './testing/fuzz';

describe('toTrendData', () => {
  it('returns empty for missing or empty history', () => {
    expect(toTrendData(undefined)).toEqual([]);
    expect(toTrendData([])).toEqual([]);
  });

  it('drops unparseable times and non-finite scores', () => {
    expect(
      toTrendData([
        { time: 'not-a-date', score: 10 },
        { time: '2026-01-01T00:00:00Z', score: Number.NaN },
        { time: '2026-01-01T00:00:00Z', score: Number.POSITIVE_INFINITY },
      ]),
    ).toEqual([]);
  });

  it('keeps finite scores with parseable times', () => {
    const points = toTrendData([{ time: '2026-01-01T00:00:00.000Z', score: 80 }]);
    expect(points).toHaveLength(1);
    expect(points[0].y).toBe(80);
    expect(points[0].x.getTime()).toBe(Date.parse('2026-01-01T00:00:00.000Z'));
  });

  it('clamps out-of-range scores into the CRD bounds', () => {
    // A restored or hand-edited snapshot: the trend must not label, color, or
    // plot a score the operator's clampHistory would never have written.
    expect(
      toTrendData([
        { time: '2026-01-01T00:00:00Z', score: 5000 },
        { time: '2026-01-02T00:00:00Z', score: -20 },
      ]).map((p) => p.y),
    ).toEqual([100, 0]);
  });
});

// The separator the previous key builder interpolated, spelled out so the
// forgery below is readable and the source stays free of control characters.
const NUL = String.fromCharCode(0);
const SOH = String.fromCharCode(1);

describe('historyContentKey', () => {
  it('is empty for missing or empty history', () => {
    expect(historyContentKey(undefined)).toBe('');
    expect(historyContentKey([])).toBe('');
  });

  it('is stable across reallocations of the same points', () => {
    const a = [
      { time: '2026-01-01T00:00:00Z', score: 80 },
      { time: '2026-01-02T00:00:00Z', score: 82 },
    ];
    const b = a.map((h) => ({ time: h.time, score: h.score }));
    expect(historyContentKey(a)).toBe(historyContentKey(b));
    expect(historyContentKey(a)).not.toBe(historyContentKey(a.slice(0, 1)));
  });

  it('does not let a snapshot time forge another ring key', () => {
    // The old NUL/SOH interpolation gave both rings the same key, so MiniTrend
    // kept painting the first one after the status changed to the second.
    const two = [
      { time: '2026-01-01T00:00:00Z', score: 80 },
      { time: '2026-01-02T00:00:00Z', score: 82 },
    ];
    const merged = [
      { time: `2026-01-01T00:00:00Z${NUL}80${SOH}2026-01-02T00:00:00Z`, score: 82 },
    ];
    expect(historyContentKey(two)).not.toBe(historyContentKey(merged));
  });
});

// Structural fuzz for status.history, the one Overview input no other fuzz
// harness covers. Both helpers feed a React memo dependency, so the contract
// has two halves: a malformed ring must not throw (it would blank the page), and
// two rings that differ must not share a key (the chart would keep painting the
// stale series). The second half is the assertion a throw-only harness cannot
// express, so it is written as an explicit pairwise check over a fixed table of
// separator-bearing rings rather than left to the random sweep.
describe('history ring fuzz (untrusted status.history)', () => {
  // Leaves the CRD forbids but the API still delivers, plus the \0 / \x01 bytes
  // the key encoding has to survive.
  const LEAVES: unknown[] = [
    undefined,
    null,
    '',
    '2026-01-01T00:00:00Z',
    'a\x01b',
    'a\0b',
    'x'.repeat(64),
    0,
    -1,
    80,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    true,
  ];

  // A ring is a list of hostile entries, null entries included: the helpers
  // index and read elements, so a hand-edited ring must not assume objects.
  const junkRing = (next: () => number): unknown[] =>
    Array.from({ length: Math.floor(next() * 4) }, () => {
      if (next() < 0.25) {
        return null;
      }
      return {
        time: LEAVES[Math.floor(next() * LEAVES.length)],
        score: LEAVES[Math.floor(next() * LEAVES.length)],
      };
    });

  const ITER = 3000;

  it('toTrendData never throws and only ever returns plottable points', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = mulberry32(seed);
      const ring = junkRing(next);
      let points: { x: Date; y: number }[];
      try {
        // SAFETY: the ring holds hostile entries on purpose; the helpers own the guards.
        points = toTrendData(ring as ScoreSnapshot[]);
      } catch (e) {
        throw new Error(`seed ${seed} threw: ${String(e)}`);
      }
      // Every surviving point must be one Victory can plot: a real instant and a
      // score inside the CRD [0,100] bounds. A NaN y compares false against
      // every threshold and would paint the badge green.
      for (const p of points) {
        expect(Number.isNaN(p.x.getTime())).toBeFalsy();
        expect(Number.isFinite(p.y)).toBeTruthy();
        expect(p.y).toBeGreaterThanOrEqual(0);
        expect(p.y).toBeLessThanOrEqual(100);
      }
      expect(points.length).toBeLessThanOrEqual(ring.length);
    }
  });

  it('historyContentKey never throws and is stable for equal content', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = mulberry32(seed);
      const ring = junkRing(next);
      let key: string;
      let again: string;
      try {
        // SAFETY: the ring holds hostile entries on purpose; the helpers own the guards.
        key = historyContentKey(ring as ScoreSnapshot[]);
        // SAFETY: a re-read of the same ring, so the key must not depend on array identity.
        again = historyContentKey(ring as ScoreSnapshot[]);
      } catch (e) {
        throw new Error(`seed ${seed} threw: ${String(e)}`);
      }
      expect(typeof key).toBe('string');
      expect(again).toBe(key);
    }
  });

  // Hand-picked rings a bare-separator encoding collapses. Every entry has
  // content distinct from every other, so all keys must be pairwise distinct.
  // Typed as unknown because several entries carry the string scores a
  // hand-edited status.history can hold.
  const COLLISION_CASES: unknown[][] = [
    [
      { time: 'a', score: 'b' },
      { time: 'c', score: 'd' },
    ],
    [{ time: 'a\0b\x01c', score: 'd' }],
    [{ time: 'a\x01b', score: 80 }],
    [
      { time: 'a', score: 80 },
      { time: 'b', score: 80 },
    ],
    [{ time: 'a\0b', score: 80 }],
    [
      { time: 'a', score: 80 },
      { time: '', score: 80 },
    ],
    [{ time: '1:2026-01-01T00:00:00Z', score: 80 }],
    [{ time: '2026-01-01T00:00:00Z', score: 80 }],
    [{ time: '2026-01-01T00:00:00Z', score: 81 }],
  ];

  // SAFETY: every ring here is hostile by design; historyContentKey owns the guards.
  const keyOf = (ring: unknown[]): string => historyContentKey(ring as ScoreSnapshot[]);

  it('gives distinct content distinct keys', () => {
    expect(new Set(COLLISION_CASES.map(keyOf)).size).toBe(COLLISION_CASES.length);
  });

  // The regression this table exists for: a two-point ring and a one-point ring
  // whose time swallows the separator bytes encoded to the same key, so React
  // kept the stale series after the status changed.
  it('separates a two-point ring from a one-point ring carrying the separators', () => {
    expect(keyOf(COLLISION_CASES[0])).not.toBe(keyOf(COLLISION_CASES[1]));
  });
});
