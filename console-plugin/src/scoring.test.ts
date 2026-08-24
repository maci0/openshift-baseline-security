import { ClusterBaseline, ComplianceCheckResult, ResultCounts } from './models';
import { HISTORY_SCORING_MODE_ANN, aggregateCounts, checkSeverity, clusterScore, effectiveScoringMode, flatProfileScore, historyScoringModeMismatch, profileScore, scoreColor, scoreLabelColor, severityWeight } from './scoring';

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

describe('scoreColor', () => {
  it.each([
    [undefined, 'danger'],
    [0, 'danger'],
    [59, 'danger'],
    [60, 'warning'],
    [89, 'warning'],
    [90, 'success'],
    [100, 'success'],
    // NaN must not fall through Math comparisons as a false success color.
    [Number.NaN, 'danger'],
  ])('score %p -> %s', (score, status) => {
    expect(scoreColor(score as number | undefined)).toContain(`status--${status}`);
  });
  it('fuzz: always a CSS var token', () => {
    for (let i = 0; i < 500; i++) {
      const s =
        i === 0 ? undefined : i === 1 ? Number.NaN : Math.floor(fuzzRand() * 200) - 50;
      const color = scoreColor(s);
      expect(color.startsWith('var(--pf-t--')).toBe(true);
      if (s === undefined || Number.isNaN(s) || (typeof s === 'number' && s < 60)) {
        expect(color).toContain('status--danger');
      }
    }
  });
});

describe('scoreLabelColor', () => {
  it.each([
    [0, 'red'],
    [59, 'red'],
    [60, 'orange'],
    [89, 'orange'],
    [90, 'green'],
    [100, 'green'],
    // NaN comparisons are false: must not paint green/orange (same band as scoreColor danger).
    [Number.NaN, 'red'],
    // Non-finite extremes: only >=90 is green; -Infinity is red, +Infinity is green.
    [Number.NEGATIVE_INFINITY, 'red'],
    [Number.POSITIVE_INFINITY, 'green'],
  ])('score %p -> %s', (score, color) => {
    expect(scoreLabelColor(score)).toBe(color);
  });
});

describe('effectiveScoringMode / historyScoringModeMismatch', () => {
  it('defaults to Flat when scoring mode is unset', () => {
    expect(effectiveScoringMode(undefined)).toBe('Flat');
    expect(effectiveScoringMode({ spec: { profiles: ['cis'] } })).toBe('Flat');
    expect(
      effectiveScoringMode({ spec: { profiles: ['cis'], scoring: { mode: 'Flat' } } }),
    ).toBe('Flat');
    expect(
      effectiveScoringMode({
        spec: { profiles: ['cis'], scoring: { mode: 'SeverityWeighted' } },
      }),
    ).toBe('SeverityWeighted');
  });

  // Annotation is hand-editable CR metadata; unknown stamps and modes must not throw.
  it('fuzz: historyScoringModeMismatch never throws; empty stamp is not a mismatch', () => {
    for (let i = 0; i < 500; i++) {
      const stamp =
        i % 4 === 0 ? undefined : i % 4 === 1 ? '' : i % 4 === 2 ? 'Flat' : randomString(i % 16);
      const mode: 'Flat' | 'SeverityWeighted' | undefined =
        i % 3 === 0 ? undefined : i % 3 === 1 ? 'Flat' : 'SeverityWeighted';
      const baseline = {
        metadata: {
          name: 'cluster',
          annotations: stamp === undefined ? undefined : { [HISTORY_SCORING_MODE_ANN]: stamp },
        },
        spec: {
          profiles: ['cis' as const],
          scoring: mode ? { mode } : undefined,
        },
      };
      const mismatch = historyScoringModeMismatch(baseline);
      expect(typeof mismatch).toBe('boolean');
      if (!stamp) {
        expect(mismatch).toBe(false);
      }
      // effectiveScoringMode collapses anything except SeverityWeighted to Flat.
      const effective = effectiveScoringMode(baseline);
      expect(effective === 'Flat' || effective === 'SeverityWeighted').toBe(true);
    }
  });

  it('detects history points stamped under a different scoring mode', () => {
    expect(historyScoringModeMismatch(undefined)).toBe(false);
    expect(
      historyScoringModeMismatch({
        metadata: { name: 'cluster' },
        spec: { profiles: ['cis'] },
      }),
    ).toBe(false);
    expect(
      historyScoringModeMismatch({
        metadata: {
          name: 'cluster',
          annotations: { [HISTORY_SCORING_MODE_ANN]: 'Flat' },
        },
        spec: { profiles: ['cis'], scoring: { mode: 'Flat' } },
      }),
    ).toBe(false);
    expect(
      historyScoringModeMismatch({
        metadata: {
          name: 'cluster',
          annotations: { [HISTORY_SCORING_MODE_ANN]: 'Flat' },
        },
        spec: { profiles: ['cis'], scoring: { mode: 'SeverityWeighted' } },
      }),
    ).toBe(true);
    expect(
      historyScoringModeMismatch({
        metadata: {
          name: 'cluster',
          annotations: { [HISTORY_SCORING_MODE_ANN]: 'SeverityWeighted' },
        },
        spec: { profiles: ['cis'] },
      }),
    ).toBe(true);
  });
});

describe('severityWeight / profileScore', () => {
  const check = (
    name: string,
    suite: string,
    status: string,
    severity: string,
  ): ComplianceCheckResult =>
    ({
      metadata: {
        name,
        namespace: 'openshift-compliance',
        labels: { 'compliance.openshift.io/suite': suite },
      },
      status,
      severity,
    }) as ComplianceCheckResult;

  it('matches the operator weight table', () => {
    expect(severityWeight('high')).toBe(10);
    expect(severityWeight('medium')).toBe(5);
    expect(severityWeight('low')).toBe(2);
    expect(severityWeight('unknown')).toBe(1);
    expect(severityWeight(undefined)).toBe(1);
    // Case-sensitive lockstep with operator: unexpected casing is weight 1.
    expect(severityWeight('HIGH')).toBe(1);
    expect(severityWeight('info')).toBe(1);
  });
  // Typed field wins; check-severity label is fallback; missing both is "unknown"
  // so Results severity filters and CSV match the weight table / TEST-PLAN.
  it('checkSeverity prefers .severity and falls back to the label', () => {
    expect(checkSeverity({ severity: 'high' })).toBe('high');
    expect(
      checkSeverity({
        severity: '',
        metadata: { labels: { 'compliance.openshift.io/check-severity': 'medium' } },
      }),
    ).toBe('medium');
    expect(
      checkSeverity({
        severity: 'high',
        metadata: { labels: { 'compliance.openshift.io/check-severity': 'low' } },
      }),
    ).toBe('high');
    expect(checkSeverity({})).toBe('unknown');
    expect(checkSeverity({ metadata: { labels: {} } })).toBe('unknown');
    expect(checkSeverity({ severity: '', metadata: { labels: {} } })).toBe('unknown');
  });
  it('profileScore SeverityWeighted uses label severity when field is absent', () => {
    const results = [
      {
        metadata: {
          name: 'p1',
          namespace: 'openshift-compliance',
          labels: {
            'compliance.openshift.io/suite': 'baseline-cis',
            'compliance.openshift.io/check-severity': 'high',
          },
        },
        status: 'PASS',
      },
      {
        metadata: {
          name: 'f1',
          namespace: 'openshift-compliance',
          labels: {
            'compliance.openshift.io/suite': 'baseline-cis',
            'compliance.openshift.io/check-severity': 'low',
          },
        },
        status: 'FAIL',
      },
    ] as ComplianceCheckResult[];
    // high PASS (10) + low FAIL (2) => 83; weight-1 defaults would yield 50.
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
        },
      ),
    ).toBe(83);
  });
  it('flatProfileScore floors pass/(pass+fail)', () => {
    expect(flatProfileScore(1, 1)).toBe(50);
    expect(flatProfileScore(3, 1)).toBe(75);
    expect(flatProfileScore(0, 0)).toBeNull();
    // Lockstep with operator score(): integer division floors so a single FAIL
    // among many PASS never rounds up to a false 100.
    expect(flatProfileScore(999, 1)).toBe(99);
    expect(flatProfileScore(1, 2)).toBe(33);
    expect(flatProfileScore(1, 0)).toBe(100);
    expect(flatProfileScore(0, 5)).toBe(0);
    // Lockstep with operator score(): negative or non-finite mass is nil, not
    // a negative/NaN badge (denom>0 alone was false confidence).
    expect(flatProfileScore(-1, 5)).toBeNull();
    expect(flatProfileScore(5, -1)).toBeNull();
    expect(flatProfileScore(-1, -1)).toBeNull();
    expect(flatProfileScore(Number.NaN, 1)).toBeNull();
    expect(flatProfileScore(1, Number.POSITIVE_INFINITY)).toBeNull();
    // Finite operands whose sum or p*100 product overflows must fail closed
    // like the operator's int64 score() (nil), never NaN / Infinity: those
    // leak past threshold comparisons as a false badge color.
    expect(flatProfileScore(1e308, 1e308)).toBeNull();
    expect(flatProfileScore(Number.MAX_VALUE, Number.MAX_VALUE)).toBeNull();
    expect(flatProfileScore(1e307, 1)).toBeNull();
    expect(flatProfileScore(-1e307, -1e307)).toBeNull();
  });
  // Untrusted / hand-edited ResultCounts: score is null or in [0,100], never NaN.
  // Oracle matches operator score(): floor(pass*100/(pass+fail)) for non-neg finite.
  it('fuzz: flatProfileScore is null or [0,100] for arbitrary mass', () => {
    const samples: Array<[number | undefined, number | undefined]> = [
      [0, 0],
      [1, 0],
      [0, 1],
      [2, 1],
      [-1, 5],
      [5, -1],
      [Number.NaN, 1],
      [1, Number.POSITIVE_INFINITY],
      [Number.MAX_SAFE_INTEGER, 1],
      [Number.MAX_VALUE, Number.MAX_VALUE],
      [1e307, 1],
      [1e308, 1e308],
      [undefined, undefined],
    ];
    for (let i = 0; i < 2000; i++) {
      const pass =
        i < samples.length ? samples[i][0] : Math.floor(fuzzRand() * 1e6) - 1e3;
      const fail =
        i < samples.length ? samples[i][1] : Math.floor(fuzzRand() * 1e6) - 1e3;
      let got: number | null = null;
      expect(() => {
        got = flatProfileScore(pass, fail);
      }).not.toThrow();
      if (got === null) {
        continue;
      }
      expect(Number.isFinite(got)).toBe(true);
      expect(got).toBeGreaterThanOrEqual(0);
      expect(got).toBeLessThanOrEqual(100);
      const p = pass ?? 0;
      const f = fail ?? 0;
      if (Number.isFinite(p) && Number.isFinite(f) && p >= 0 && f >= 0 && p + f > 0) {
        expect(got).toBe(Math.floor((p * 100) / (p + f)));
      }
    }
  });
  it('profileScore uses flat counts by default', () => {
    expect(profileScore({ pass: 1, fail: 1 })).toBe(50);
  });
  // SeverityWeighted with an empty CCR list (watch still loading) must not blank
  // the Overview badge when status already has pass/fail tallies.
  it('profileScore SeverityWeighted falls back to flat when results are empty', () => {
    expect(
      profileScore(
        { pass: 3, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results: [],
          profiles: ['cis'],
        },
      ),
    ).toBe(75);
    // Prefer last history point (operator weighted) over flat when present.
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results: [],
          profiles: ['cis'],
          history: [{ score: 83 }, { score: 90 }],
        },
      ),
    ).toBe(90);
    // Loaded path with no countable mass still null (not a false flat score).
    expect(
      profileScore(
        { pass: 0, fail: 0 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results: [check('m1', 'baseline-cis', 'MANUAL', 'medium')],
          profiles: ['cis'],
        },
      ),
    ).toBeNull();
  });
  it('profileScore weights by severity in SeverityWeighted mode', () => {
    const results = [
      check('p1', 'baseline-cis', 'PASS', 'high'),
      check('f1', 'baseline-cis', 'FAIL', 'low'),
    ];
    // high PASS (10) + low FAIL (2) => 83; flat would be 50
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
        },
      ),
    ).toBe(83);
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        { mode: 'Flat', filterKey: 'cis', results, profiles: ['cis'] },
      ),
    ).toBe(50);
  });
  // Overview cards recompute severity-weighted scores client-side; a waived FAIL
  // must leave the denominator so the badge matches status.score.
  it('profileScore SeverityWeighted excludes waived FAILs', () => {
    const results = [
      check('p1', 'baseline-cis', 'PASS', 'high'),
      check('f1', 'baseline-cis', 'FAIL', 'high'),
    ];
    const now = new Date('2026-07-11T00:00:00Z');
    // Without waiver: high PASS (10) + high FAIL (10) => 50
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
          waivers: [{ name: 'f1', reason: 'accepted' }],
          now,
        },
      ),
    ).toBe(100);
    // Expired waiver must re-include the FAIL.
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
          waivers: [{ name: 'f1', expiresAt: '2026-07-10T00:00:00Z' }],
          now,
        },
      ),
    ).toBe(50);
    // Only FAILs, all waived: no countable mass => null (not a false 100/0).
    expect(
      profileScore(
        { pass: 0, fail: 2 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results: [
            check('f1', 'baseline-cis', 'FAIL', 'high'),
            check('f2', 'baseline-cis', 'FAIL', 'low'),
          ],
          profiles: ['cis'],
          waivers: [{ name: 'f1' }, { name: 'f2' }],
          now,
        },
      ),
    ).toBeNull();
    // Foreign / stale waiver names must not invent matches (by-name contract).
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
          waivers: [{ name: 'not-a-real-check' }, { name: '' }],
          now,
        },
      ),
    ).toBe(50);
    // Shared activeWaived Set path (Overview prebuilds one Set for all cards).
    const activeWaived = new Set(['f1']);
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
          activeWaived,
          now,
        },
      ),
    ).toBe(100);
  });
  // Overview pre-buckets by suite and ownership, then omits profiles so
  // profileScore weighs the bucket without a second membership scan.
  it('profileScore SeverityWeighted prefiltered bucket skips ownership re-scan', () => {
    const bucket = [
      check('p1', 'baseline-cis', 'PASS', 'high'),
      check('f1', 'baseline-cis', 'FAIL', 'low'),
    ];
    // high PASS (10) + low FAIL (2) => 83; no profiles/tailored => trust bucket.
    expect(
      profileScore(
        { pass: 1, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results: bucket,
        },
      ),
    ).toBe(83);
  });
  // Multi-profile watches return every suite; score for one card must ignore
  // foreign suites and unselected tailored results.
  it('profileScore SeverityWeighted filters by suite and ownership', () => {
    const results = [
      check('cis-pass', 'baseline-cis', 'PASS', 'high'),
      check('stig-fail', 'baseline-stig', 'FAIL', 'high'),
      check('tp-fail', 'baseline-tp-custom', 'FAIL', 'high'),
    ];
    expect(
      profileScore(
        { pass: 1, fail: 0 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'cis',
          results,
          profiles: ['cis'],
          tailoredProfiles: ['custom'],
        },
      ),
    ).toBe(100);
    expect(
      profileScore(
        { pass: 0, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'tp-custom',
          results,
          profiles: ['cis'],
          tailoredProfiles: ['custom'],
        },
      ),
    ).toBe(0);
    // Tailored-only baseline: profiles may be empty; still recompute weights.
    expect(
      profileScore(
        { pass: 0, fail: 1 },
        {
          mode: 'SeverityWeighted',
          filterKey: 'tp-custom',
          results,
          profiles: [],
          tailoredProfiles: ['custom'],
        },
      ),
    ).toBe(0);
  });
});

describe('aggregateCounts', () => {
  const c = (
    pass: number,
    fail: number,
    manual = 0,
    info = 0,
    error = 0,
    inconsistent = 0,
    waived = 0,
    notApplicable = 0,
  ) => ({
    pass,
    fail,
    manual,
    info,
    error,
    inconsistent,
    waived,
    notApplicable,
  });
  it('sums profiles and tailored profiles together', () => {
    expect(aggregateCounts(c(10, 2, 1, 4, 0, 5, 7), c(40, 8, 3, 1, 0, 6, 2))).toEqual(
      c(50, 10, 4, 5, 0, 11, 9),
    );
  });
  it('returns zeros for no groups', () => {
    expect(aggregateCounts()).toEqual(c(0, 0, 0, 0, 0, 0, 0, 0));
  });
  it('score composition matches: tailored-only results still populate totals', () => {
    // regular profile empty, tailored has results -> totals non-zero
    const totals = aggregateCounts(c(0, 0), c(2, 1));
    expect(totals.pass + totals.fail).toBe(3);
  });
  it('treats missing count fields from older persisted status as zero', () => {
    const totals = aggregateCounts({ pass: 1, fail: 2 } as ResultCounts);
    expect(totals).toEqual(c(1, 2, 0, 0, 0, 0, 0, 0));
  });
  it('folds a non-finite (NaN/Infinity) or non-numeric count to 0, never poisoning totals', () => {
    // A tampered non-numeric/non-finite count must not string-concatenate or
    // spread NaN/Infinity across the donut totals.
    const totals = aggregateCounts(
      { pass: Number.NaN, fail: Number.POSITIVE_INFINITY } as ResultCounts,
      { pass: '5' as unknown as number, fail: 3 } as ResultCounts,
    );
    expect(totals.pass).toBe(5); // NaN -> 0, '5' -> 5
    expect(totals.fail).toBe(3); // Infinity -> 0, 3 -> 3
    expect(Number.isFinite(totals.pass + totals.fail)).toBe(true);
  });
  it('keeps running totals finite when huge-but-finite counts overflow the accumulator', () => {
    // Per-value folding is not enough: two finite 9e307 counts sum to Infinity.
    // The accumulator must saturate (keep the prior finite total) so donut
    // segments and the report total never receive a non-finite y value.
    const totals = aggregateCounts(
      { pass: 9e307, fail: -9e307 } as ResultCounts,
      { pass: 9e307, fail: -9e307 } as ResultCounts,
    );
    expect(Number.isFinite(totals.pass)).toBe(true);
    expect(Number.isFinite(totals.fail)).toBe(true);
    // Saturation keeps the first contribution rather than inventing Infinity.
    expect(totals.pass).toBe(9e307);
    expect(totals.fail).toBe(-9e307);
  });
  // Status count fields may be missing, huge, or negative from stale CRs.
  it('fuzz: never throws; all fields are finite numbers', () => {
    for (let i = 0; i < 500; i++) {
      const g = (): ResultCounts =>
        ({
          pass: i % 7 === 0 ? undefined : (i % 1000) - 50,
          fail: i % 5 === 0 ? undefined : (i % 800) - 20,
          manual: i % 11 === 0 ? undefined : i % 30,
          info: i % 13 === 0 ? undefined : i % 40,
          error: i % 17 === 0 ? undefined : i % 10,
          inconsistent: i % 19 === 0 ? undefined : i % 15,
          waived: i % 23 === 0 ? undefined : i % 25,
          notApplicable: i % 29 === 0 ? undefined : i % 12,
        }) as ResultCounts;
      const totals = aggregateCounts(g(), g(), g());
      for (const v of Object.values(totals)) {
        expect(typeof v).toBe('number');
        expect(Number.isFinite(v)).toBe(true);
      }
    }
  });
});

describe('clusterScore', () => {
  const cb = (name: string, score?: number): ClusterBaseline =>
    ({ metadata: { name }, status: score == null ? {} : { score } }) as ClusterBaseline;

  it('returns null when there is no baseline', () => {
    expect(clusterScore(undefined)).toBeNull();
    expect(clusterScore([])).toBeNull();
  });
  it('prefers the singleton named "cluster"', () => {
    expect(clusterScore([cb('other', 10), cb('cluster', 95)])).toBe(95);
  });
  it('falls back to the first when none is named "cluster"', () => {
    expect(clusterScore([cb('a', 42), cb('b', 7)])).toBe(42);
  });
  it('returns null when the baseline exists but has not scored', () => {
    expect(clusterScore([cb('cluster')])).toBeNull();
  });
  it('treats a zero score as a value, not absent', () => {
    expect(clusterScore([cb('cluster', 0)])).toBe(0);
  });
  // Baseline list shape is untrusted API data; prefer "cluster", else first.
  it('fuzz: never throws; null or a number', () => {
    for (let i = 0; i < 500; i++) {
      const list: ClusterBaseline[] = Array.from({ length: i % 5 }, (_, j) =>
        cb(j === 1 ? 'cluster' : randomString((j + 1) % 12), i % 3 === 0 ? undefined : (i + j) % 101),
      );
      const got = clusterScore(i % 7 === 0 ? undefined : list);
      expect(got === null || typeof got === 'number').toBe(true);
    }
  });
});
