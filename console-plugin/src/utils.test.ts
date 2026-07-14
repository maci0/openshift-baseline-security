import {
  checkResultHref,
  addWaiverPatch,
  isWaived,
  activeWaivedNames,
  waiverExpired,
  findWaiver,
  expiringWaivers,
  resultFilterStatus,
  removeWaiverPatch,
  aggregateCounts,
  clusterScore,
  isNodeRemediation,
  effectiveStatus,
  inconsistentSources,
  machineConfigPoolHref,
  missingDependencySummary,
  compareRemediationsForApplyOrder,
  nodeScanPool,
  remediationObjectText,
  resultsCsv,
  remediationApplyPatch,
  resourceVersionTest,
  checkBody,
  checkTitle,
  errorMessage,
  rescanPatch,
  resultsHref,
  scoreColor,
  scoreLabelColor,
  severityWeight,
  checkSeverity,
  flatProfileScore,
  profileScore,
  effectiveScoringMode,
  historyScoringModeMismatch,
  HISTORY_SCORING_MODE_ANN,
  toggledProfiles,
  isValidCron,
  schedulePatch,
  batchApplyPatch,
  batchApplyRequested,
  buildReportHtml,
  tailoredProfileManifest,
  tailoredProfileSpecMatches,
  tailoredProfileBindingPatch,
  isAlreadyExists,
  changedChecks,
  changedChecksMany,
  dateInputEndOfDayIso,
  localDateInputValue,
  formatLocalDate,
  formatLocalDateTime,
  formatCount,
  formatChartDate,
  profileTitle,
  downloadBlob,
} from './utils';
import { ClusterBaseline, ComplianceCheckResult, ComplianceRemediation, ResultCounts } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';

const result = (name: string, description?: string): ComplianceCheckResult =>
  ({ metadata: { name, namespace: 'ns' }, description }) as ComplianceCheckResult;

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

describe('checkTitle', () => {
  it('uses the first line of the description', () => {
    expect(checkTitle(result('x', 'Title line\nRationale text'))).toBe('Title line');
  });
  it('trims whitespace', () => {
    expect(checkTitle(result('x', '  Title  \nrest'))).toBe('Title');
  });
  it('falls back to the name when description is missing or blank', () => {
    expect(checkTitle(result('fallback'))).toBe('fallback');
    expect(checkTitle(result('fallback', ''))).toBe('fallback');
    expect(checkTitle(result('fallback', '\n\n'))).toBe('fallback');
  });
  // Missing/non-string metadata.name must still yield a non-empty title so
  // Results rows and CSV cells never render undefined.
  it('falls back to unknown when name is missing or non-string', () => {
    expect(
      checkTitle({ metadata: { name: '', namespace: 'ns' } } as ComplianceCheckResult),
    ).toBe('unknown');
    expect(
      checkTitle({
        metadata: { name: 42 as unknown as string, namespace: 'ns' },
      } as ComplianceCheckResult),
    ).toBe('unknown');
    expect(
      checkTitle({
        metadata: {} as ComplianceCheckResult['metadata'],
        description: 'Title only',
      } as ComplianceCheckResult),
    ).toBe('Title only');
  });
  it('fuzz: never throws and never returns empty', () => {
    for (let i = 0; i < 2000; i++) {
      const title = checkTitle(result('name', randomString(i % 64)));
      expect(typeof title).toBe('string');
      expect(title.length).toBeGreaterThan(0);
    }
  });
  // CRs are not runtime type-checked: a tampered non-string description must
  // fall back to the name, not throw on .indexOf/.trim.
  it('fuzz: tolerates non-string (tampered CR) descriptions', () => {
    for (const bad of [0, 42, true, {}, [], null, NaN] as unknown[]) {
      const r = { metadata: { name: 'nm' }, description: bad } as unknown as ComplianceCheckResult;
      expect(checkTitle(r)).toBe('nm');
      expect(checkBody(r)).toBe('');
    }
  });
});

describe('checkBody', () => {
  it('returns everything after the first line, trimmed', () => {
    expect(checkBody(result('x', 'Title\nBody line 1\nBody line 2'))).toBe(
      'Body line 1\nBody line 2',
    );
  });
  it('returns empty for single-line or missing descriptions', () => {
    expect(checkBody(result('x', 'Title only'))).toBe('');
    expect(checkBody(result('x'))).toBe('');
  });
  it('fuzz: never throws', () => {
    for (let i = 0; i < 2000; i++) {
      expect(typeof checkBody(result('n', randomString(i % 64)))).toBe('string');
    }
  });
});

describe('toggledProfiles', () => {
  it('adds and removes keys', () => {
    expect(toggledProfiles(['cis'], 'stig', true)).toEqual(['cis', 'stig']);
    expect(toggledProfiles(['cis', 'stig'], 'stig', false)).toEqual(['cis']);
  });
  it('deduplicates when adding an existing key', () => {
    expect(toggledProfiles(['cis'], 'cis', true)).toEqual(['cis']);
  });
  it('allows clearing the last profile (disables scanning)', () => {
    expect(toggledProfiles(['cis'], 'cis', false)).toEqual([]);
  });
  it('refuses adds past CRD MaxItems=8', () => {
    const full = ['cis', 'pci-dss', 'nist-moderate', 'nist-high', 'stig', 'nerc-cip', 'e8', 'bsi'];
    // 'extra' is also not a ProfileKey enum value; either bound fails closed.
    expect(toggledProfiles(full, 'extra', true)).toEqual(full);
    expect(toggledProfiles(full, 'cis', false)).toEqual(full.filter((k) => k !== 'cis'));
  });
  it('refuses unknown ProfileKey values (CRD enum fail-closed)', () => {
    expect(toggledProfiles(['cis'], 'extra', true)).toEqual(['cis']);
    expect(toggledProfiles(['cis'], 'CIS', true)).toEqual(['cis']);
    expect(toggledProfiles(['cis'], '', true)).toEqual(['cis']);
    // Remove still filters by exact key match (unknown keys can be stripped).
    expect(toggledProfiles(['cis', 'bogus'], 'bogus', false)).toEqual(['cis']);
  });
  it('fuzz: never duplicates when adding; removing the missing key is a no-op', () => {
    const keys = ['cis', 'stig', 'e8', 'bsi', 'pci-dss'];
    for (let i = 0; i < 2000; i++) {
      const n = (i % 5) + 1;
      const current = keys.slice(0, n);
      const key = keys[i % keys.length];
      const checked = i % 2 === 0;
      const next = toggledProfiles(current, key, checked);
      expect(new Set(next).size).toBe(next.length);
      expect(next.length).toBeLessThanOrEqual(8);
      if (checked) {
        expect(next).toContain(key);
      }
    }
  });
  // Toggle involution: enable X then disable X returns the prior set when the
  // add was allowed (not at MaxItems, key not already present). Disable then
  // re-enable restores membership when the set was non-empty after remove.
  it('fuzz: enable then disable is involution when the add is accepted', () => {
    const keys = ['cis', 'stig', 'e8', 'bsi', 'pci-dss', 'nerc-cip', 'nist-high', 'nist-moderate'];
    for (let i = 0; i < 2000; i++) {
      const n = i % 8; // 0..7 so an add can succeed under MaxItems=8
      const current = keys.slice(0, n);
      const key = keys[i % keys.length];
      if (current.includes(key)) {
        // Already present: enable is a no-op; disable then re-enable restores.
        const without = toggledProfiles(current, key, false);
        expect(without).not.toContain(key);
        expect(toggledProfiles(without, key, true).sort()).toEqual([...current].sort());
        continue;
      }
      const added = toggledProfiles(current, key, true);
      if (added.length === current.length) {
        // MaxItems refused the add; leave current unchanged.
        expect(added).toEqual(current);
        continue;
      }
      expect(added).toContain(key);
      expect(toggledProfiles(added, key, false).sort()).toEqual([...current].sort());
    }
  });
});

describe('remediationApplyPatch', () => {
  it('adds the leaf when spec.remediation exists so absent defaulted fields are tolerated', () => {
    expect(remediationApplyPatch(true, true)).toEqual([
      { op: 'add', path: '/spec/remediation/apply', value: 'Automatic' },
    ]);
    expect(remediationApplyPatch(true, false)).toEqual([
      { op: 'add', path: '/spec/remediation/apply', value: 'Manual' },
    ]);
  });
  it('adds the parent object when spec.remediation is absent', () => {
    expect(remediationApplyPatch(false, true)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { apply: 'Automatic' } },
    ]);
    expect(remediationApplyPatch(false, false)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { apply: 'Manual' } },
    ]);
  });
  it('fuzz: always a single op carrying a valid enum value', () => {
    for (const has of [true, false]) {
      for (const automatic of [true, false]) {
        const patch = remediationApplyPatch(has, automatic);
        expect(patch).toHaveLength(1);
        const v = patch[0].value;
        const apply = typeof v === 'string' ? v : (v as { apply: string }).apply;
        expect(['Automatic', 'Manual']).toContain(apply);
      }
    }
  });
});

describe('rescanPatch', () => {
  it('adds nested annotation when annotations map exists', () => {
    expect(rescanPatch(true, 't1')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations/compliance.openshift.io~1rescan',
        value: 't1',
      },
    ]);
  });
  it('adds the annotations object when missing', () => {
    expect(rescanPatch(false, 't2')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations',
        value: { 'compliance.openshift.io/rescan': 't2' },
      },
    ]);
  });
  it('guards whole-map creation against concurrent annotation writes', () => {
    expect(rescanPatch(false, 't3', '42')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '42' },
      {
        op: 'add',
        path: '/metadata/annotations',
        value: { 'compliance.openshift.io/rescan': 't3' },
      },
    ]);
  });
  // Nested annotation add cannot clobber siblings, so resourceVersion is unused
  // when the annotations map already exists.
  it('does not resourceVersion-guard a nested annotation add', () => {
    expect(rescanPatch(true, 't4', '99')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations/compliance.openshift.io~1rescan',
        value: 't4',
      },
    ]);
  });
  it('is a no-op for empty or whitespace-only tokens', () => {
    expect(rescanPatch(true, '')).toEqual([]);
    expect(rescanPatch(false, '   ')).toEqual([]);
    expect(rescanPatch(true, '\t', '99')).toEqual([]);
  });
  it('trims the rescan token before writing', () => {
    expect(rescanPatch(true, '  t5  ')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations/compliance.openshift.io~1rescan',
        value: 't5',
      },
    ]);
  });
  it('fuzz: last op carries the token; RV guard only for whole-map create', () => {
    for (let i = 0; i < 100; i++) {
      const token = String(i);
      const hasAnnotations = i % 2 === 0;
      const rv = i % 3 === 0 ? String(i) : undefined;
      const p = rescanPatch(hasAnnotations, token, rv);
      const expectGuard = !hasAnnotations && rv != null;
      expect(p).toHaveLength(expectGuard ? 2 : 1);
      if (expectGuard) {
        expect(p[0]).toEqual({ op: 'test', path: '/metadata/resourceVersion', value: rv });
      }
      const last = p[p.length - 1];
      if (hasAnnotations) {
        expect(last.value).toBe(token);
      } else {
        expect(
          (last.value as { 'compliance.openshift.io/rescan': string })[
            'compliance.openshift.io/rescan'
          ],
        ).toBe(token);
      }
    }
  });
});

describe('formatLocalDate / formatLocalDateTime', () => {
  it('formats parseable ISO timestamps', () => {
    const iso = '2026-07-11T12:00:00.000Z';
    expect(formatLocalDate(iso, 'en-US')).toMatch(/2026/);
    expect(formatLocalDateTime(iso, 'en-US')).toMatch(/2026/);
  });
  it('accepts underscore locale tags (en_US) as BCP 47', () => {
    const iso = '2026-07-11T12:00:00.000Z';
    // Underscore form must not throw or yield Invalid Date; match hyphen form year.
    expect(formatLocalDate(iso, 'en_US')).toMatch(/2026/);
    expect(formatLocalDateTime(iso, 'de_DE')).toMatch(/2026/);
  });
  it('treats YYYY-MM-DD as a local calendar day (not UTC midnight)', () => {
    // `new Date('2026-07-12')` is UTC midnight; in US zones toLocaleDateString
    // would show July 11. Local-calendar parse must keep the selected day.
    const out = formatLocalDate('2026-07-12', 'en-US');
    expect(out).toMatch(/12/);
    expect(out).toMatch(/2026/);
    // Invalid calendar dates fall through to the raw string (not Invalid Date).
    expect(formatLocalDate('2026-02-31', 'en-US')).toBe('2026-02-31');
  });
  it('returns the raw string for unparseable values (never "Invalid Date")', () => {
    expect(formatLocalDate('not-a-date')).toBe('not-a-date');
    expect(formatLocalDateTime('not-a-date')).toBe('not-a-date');
    expect(formatLocalDate('not-a-date')).not.toBe('Invalid Date');
  });
  it('does not throw on structurally invalid locales (Intl throws RangeError)', () => {
    // htmlLang/i18n.language flow in unvalidated; a bad tag must not crash render.
    const iso = '2026-07-11T12:00:00.000Z';
    for (const bad of ['en-', '123', '*', 'e', '!!', 'a-b-c-d']) {
      expect(() => formatLocalDate(iso, bad)).not.toThrow();
      expect(() => formatLocalDateTime(iso, bad)).not.toThrow();
    }
  });
  // ISO comes from CR/user text and locale from the document/i18n; neither is
  // validated upstream, so arbitrary pairs must never throw.
  it('fuzz: never throws for arbitrary iso and locale inputs', () => {
    for (let i = 0; i < 2000; i++) {
      const iso = randomString(i % 40);
      const locale = i % 3 === 0 ? undefined : randomString(i % 8);
      expect(() => formatLocalDate(iso, locale)).not.toThrow();
      expect(() => formatLocalDateTime(iso, locale)).not.toThrow();
    }
  });
});

describe('formatCount', () => {
  it('formats with locale grouping', () => {
    expect(formatCount(1234, 'en-US')).toBe('1,234');
    expect(formatCount(1234, 'de-DE')).toBe('1.234');
  });
  it('accepts underscore locale tags and invalid tags without throwing', () => {
    expect(formatCount(42, 'en_US')).toMatch(/42/);
    expect(() => formatCount(42, '!!')).not.toThrow();
  });
  it('returns empty for non-finite values (no English NaN/Infinity)', () => {
    expect(formatCount(NaN, 'en-US')).toBe('');
    expect(formatCount(Infinity, 'en-US')).toBe('');
    expect(formatCount(-Infinity, 'de-DE')).toBe('');
  });
});

describe('formatChartDate', () => {
  it('formats a valid Date with the given locale', () => {
    const d = new Date(2026, 6, 12, 15, 30, 0);
    expect(formatChartDate(d, 'en-US')).toMatch(/2026/);
    expect(formatChartDate(d.getTime(), 'en_US')).toMatch(/2026/);
  });
  it('returns empty for invalid instants (no English Invalid Date)', () => {
    expect(formatChartDate(new Date(NaN), 'en-US')).toBe('');
    expect(formatChartDate(Number.NaN, 'en-US')).toBe('');
  });
  it('does not throw on invalid locale tags', () => {
    expect(() => formatChartDate(new Date(0), '!!')).not.toThrow();
  });
});

describe('localDateInputValue', () => {
  it('formats the local calendar day as YYYY-MM-DD (not UTC)', () => {
    // Midday UTC so local-date vs UTC-date is stable in any common offset.
    const d = new Date(2026, 6, 12, 15, 30, 0);
    expect(localDateInputValue(d)).toBe('2026-07-12');
  });

  it('does not use UTC when local day differs from UTC day', () => {
    // 2026-07-12 01:00 local: toISOString may be the previous UTC day in
    // western zones, or still the 12th in eastern zones. Either way the local
    // calendar day must be 2026-07-12.
    const d = new Date(2026, 6, 12, 1, 0, 0);
    expect(localDateInputValue(d)).toBe('2026-07-12');
    // Contrasting wrong pattern: UTC slice can disagree with local day.
    const utcSlice = d.toISOString().slice(0, 10);
    if (utcSlice !== '2026-07-12') {
      expect(localDateInputValue(d)).not.toBe(utcSlice);
    }
  });
});

describe('profileTitle', () => {
  it('returns the display title for known profile keys', () => {
    expect(profileTitle('cis')).toBe('CIS');
    expect(profileTitle('nist-moderate')).toBe('NIST 800-53 Moderate');
    expect(profileTitle('e8')).toBe('ACSC Essential Eight');
  });

  it('uppercases unknown keys', () => {
    expect(profileTitle('custom-suite')).toBe('CUSTOM-SUITE');
  });
});

describe('dateInputEndOfDayIso', () => {
  it('keeps a date-only deadline active through the selected local day', () => {
    const parsed = new Date(dateInputEndOfDayIso('2026-07-12') ?? 'invalid');
    expect(parsed.getFullYear()).toBe(2026);
    expect(parsed.getMonth()).toBe(6);
    expect(parsed.getDate()).toBe(12);
    expect(parsed.getHours()).toBe(23);
    expect(parsed.getMinutes()).toBe(59);
    expect(parsed.getSeconds()).toBe(59);
    expect(parsed.getMilliseconds()).toBe(999);
  });

  it.each(['', '2026-02-30', '2026-13-01', 'not-a-date'])('rejects invalid input %p', (value) => {
    expect(dateInputEndOfDayIso(value)).toBeUndefined();
  });

  // User-typed date input for waiver expires/review; never throws, and a
  // defined result must be a parseable ISO string for a real calendar day.
  it('fuzz: never throws; undefined or valid ISO end-of-day', () => {
    for (let i = 0; i < 2000; i++) {
      const value =
        i % 4 === 0
          ? randomString(i % 20)
          : i % 4 === 1
            ? `${2000 + (i % 50)}-${String((i % 14) + 1).padStart(2, '0')}-${String((i % 32) + 1).padStart(2, '0')}`
            : i % 4 === 2
              ? ''
              : `2026-07-${String((i % 28) + 1).padStart(2, '0')}`;
      const got = dateInputEndOfDayIso(value);
      if (got === undefined) continue;
      expect(typeof got).toBe('string');
      const d = new Date(got);
      expect(Number.isNaN(d.getTime())).toBe(false);
      expect(d.getHours()).toBe(23);
      expect(d.getMinutes()).toBe(59);
      expect(d.getSeconds()).toBe(59);
      expect(d.getMilliseconds()).toBe(999);
    }
  });
});

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

// scoreLabelColor uses the same 60/90 bands as scoreColor (PatternFly Label tokens).
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

describe('errorMessage', () => {
  it('returns null for empty values', () => {
    expect(errorMessage(null)).toBeNull();
    expect(errorMessage(undefined)).toBeNull();
    expect(errorMessage('')).toBeNull();
  });
  it('handles string and Error', () => {
    expect(errorMessage('boom')).toBe('boom');
    expect(errorMessage(new Error('nope'))).toBe('nope');
  });
  it('handles message-bearing objects (k8s / HttpError shapes)', () => {
    expect(errorMessage({ message: 'forbidden' })).toBe('forbidden');
  });
  // Console HttpError often has message "Conflict" while Status text is on .json.
  it('prefers Kubernetes Status message on .json over generic HTTP phrases', () => {
    const httpErr = Object.assign(new Error('Conflict'), {
      name: 'HttpError',
      code: 409,
      json: { message: 'tailoredprofiles "x" already exists', reason: 'AlreadyExists' },
    });
    expect(errorMessage(httpErr)).toBe('tailoredprofiles "x" already exists');
    expect(
      errorMessage({
        message: 'Conflict',
        json: { message: 'the object has been modified' },
      }),
    ).toBe('the object has been modified');
    // Specific top-level message still wins over json.
    expect(
      errorMessage({
        message: 'custom detail',
        json: { message: 'status body' },
      }),
    ).toBe('custom detail');
  });
  // Every generic HTTP status phrase (not just Conflict) must defer to a real
  // Status detail; dropping any case label would surface the useless phrase.
  it('treats every generic HTTP status phrase as generic (json detail wins)', () => {
    for (const phrase of [
      'Conflict',
      'Forbidden',
      'Bad Request',
      'Not Found',
      'Unauthorized',
      'Too Many Requests',
      'Service Unavailable',
      'Gateway Timeout',
      'Internal Server Error',
    ]) {
      expect(errorMessage({ message: phrase, json: { message: 'real detail' } })).toBe(
        'real detail',
      );
    }
  });
  // Bare objects stringify to "[object Object]", which is useless in Alerts;
  // return null so callers fall back to a translated fail message.
  it('returns null for message-less plain objects (not "[object Object]")', () => {
    expect(errorMessage({})).toBeNull();
    expect(errorMessage({ code: 409 })).toBeNull();
    expect(errorMessage({ message: '' })).toBeNull();
    expect(errorMessage({ message: 42 })).toBeNull();
  });
  it('still stringifies arrays, numbers, and booleans', () => {
    expect(errorMessage(123)).toBe('123');
    expect(errorMessage(true)).toBe('true');
    expect(errorMessage([1, 2])).toBe('1,2');
  });
  it('never throws on hostile values (null-proto, throwing toString, symbol)', () => {
    const hostile: unknown[] = [
      Object.create(null),
      { toString() {
        throw new Error('boom');
      } },
      { get message() {
        throw new Error('getter boom');
      } },
      { message: 42 },
      { message: null },
      [1, 2, 3],
      Symbol('s'),
      123,
      true,
      () => 0,
      new Map(),
    ];
    for (const h of hostile) {
      const out = errorMessage(h);
      expect(out === null || typeof out === 'string').toBe(true);
    }
  });
  it('fuzz: returns string|null and never throws for arbitrary input', () => {
    for (let i = 0; i < 2000; i++) {
      const pool: unknown[] = [
        randomString(i % 40),
        i,
        i % 2 === 0,
        { message: randomString(i % 20) },
        { message: i },
        [randomString(i % 8)],
        i % 7 === 0 ? Object.create(null) : {},
        i % 11 === 0 ? new Error(randomString(i % 16)) : null,
      ];
      const out = errorMessage(pool[i % pool.length]);
      expect(out === null || typeof out === 'string').toBe(true);
    }
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

describe('resultsHref', () => {
  it('builds a filtered results path', () => {
    expect(resultsHref('FAIL')).toBe(
      '/baseline-security/results?rowFilter-result-status=FAIL',
    );
  });
  it('includes optional profile filter', () => {
    expect(resultsHref('PASS', 'cis')).toBe(
      '/baseline-security/results?rowFilter-result-status=PASS&rowFilter-result-profile=cis',
    );
  });
  it('encodes special characters', () => {
    expect(resultsHref('NOT-APPLICABLE')).toContain('NOT-APPLICABLE');
    expect(resultsHref('a b')).toMatch(/a(\+|%20)b/);
    expect(resultsHref('x&y')).toContain(encodeURIComponent('x&y'));
  });
  it('fuzz: always under /baseline-security/results and never throws', () => {
    for (let i = 0; i < 1000; i++) {
      const href = resultsHref(randomString(i % 32), i % 3 === 0 ? 'cis' : undefined);
      expect(href.startsWith('/baseline-security/results?')).toBe(true);
      expect(href).toContain('rowFilter-result-status=');
    }
  });
});

describe('resultsCsv', () => {
  const r = (name: string, status: string, severity: string, description?: string) =>
    ({ metadata: { name, namespace: 'ns' }, status, severity, description }) as ComplianceCheckResult;
  // Strip the UTF-8 BOM so line assertions stay readable.
  const csvLines = (csv: string): string[] => csv.replace(/^\uFEFF/, '').split('\r\n');

  it('emits a header and one row per result', () => {
    const csv = resultsCsv([r('a', 'PASS', 'low', 'Title A'), r('b', 'FAIL', 'high', 'Title B')]);
    expect(csv.startsWith('\uFEFF')).toBe(true);
    const lines = csvLines(csv);
    expect(lines[0]).toBe('name,title,status,severity,waived');
    expect(lines[1]).toBe('a,Title A,PASS,low,false');
    expect(lines[2]).toBe('b,Title B,FAIL,high,false');
  });
  it('marks waived checks so export matches score exclusions', () => {
    // Status is WAIVED (same as Results filter/table), waived column true.
    const csv = resultsCsv([r('b', 'FAIL', 'high', 'Fail B')], [{ name: 'b', reason: 'risk' }]);
    expect(csvLines(csv)[1]).toBe('b,Fail B,WAIVED,high,true');
  });
  it('does not mark a waived PASS as score-excluded (self-healing)', () => {
    // Operator only excludes FAIL+waiver; a waived check that now PASSes
    // still counts toward the score, so the CSV must not claim waived=true.
    const csv = resultsCsv([r('b', 'PASS', 'high', 'Pass B')], [{ name: 'b', reason: 'stale' }]);
    expect(csvLines(csv)[1]).toBe('b,Pass B,PASS,high,false');
  });
  // Expired waivers re-enter the score denominator; CSV waived=false must match.
  // Far-past expiresAt so wall-clock CI drift cannot flip the column.
  it('does not mark expired waivers as score-excluded', () => {
    const csv = resultsCsv(
      [r('b', 'FAIL', 'high', 'Fail B')],
      [{ name: 'b', reason: 'risk', expiresAt: '2000-01-01T00:00:00Z' }],
    );
    expect(csvLines(csv)[1]).toBe('b,Fail B,FAIL,high,false');
  });
  it('quotes and escapes cells containing comma, quote, or newline', () => {
    const csv = resultsCsv([r('x,y', 'FAIL', 'high', 'He said "hi"\nline2')]);
    const row = csvLines(csv)[1];
    expect(row).toBe('"x,y","He said ""hi""",FAIL,high,false');
  });
  it('neutralizes spreadsheet formula-looking cells from untrusted CR data', () => {
    const csv = resultsCsv([
      r('=cmd', '-1', '@import', '+SUM(1,1)'),
      r('\tTabbed', '\nNewline', 'low'),
      r('\rCarriage', 'PASS', 'low'),
      r(' =cmd', 'PASS', 'low'), // leading space then formula
      r('a\0b', 'PASS', 'low'), // NUL stripped (can truncate cells)
      r('\uFF1Dcmd', 'PASS', 'low'), // fullwidth equals
      r('|DDE', 'PASS', 'low'), // legacy Excel DDE
    ]);
    const lines = csvLines(csv);
    // status "-1" is an unknown CO status: effectiveStatus folds it to ERROR (the
    // operator tally's default bucket), so the status cell is a fixed enum, not a
    // passed-through formula. name/title/severity still exercise formula-escaping.
    expect(lines[1]).toBe(`'=cmd,"'+SUM(1,1)",ERROR,'@import,false`);
    // status "\nNewline" is unknown too -> ERROR; the escaped name/title cols still
    // exercise tab/newline quoting.
    expect(csv).toContain(`"'\tTabbed","'\tTabbed",ERROR,low,false`);
    expect(csv).toContain(`"'\rCarriage","'\rCarriage",PASS,low,false`);
    expect(csv).toContain(`' =cmd`);
    expect(csv).toContain('ab,ab,PASS,low,false');
    expect(csv).toContain(`'\uFF1Dcmd`);
    expect(csv).toContain(`'|DDE`);
    expect(csv).not.toContain('\0');
  });
  it('handles empty input (header only)', () => {
    expect(resultsCsv([])).toBe('\uFEFFname,title,status,severity,waived');
  });
  // Export must match the Results table: benign INCONSISTENT collapses so CSV
  // status is not a raw "INCONSISTENT" that fails filters and score math.
  it('collapses benign INCONSISTENT via resultFilterStatus', () => {
    const inconsistent = {
      metadata: {
        name: 'inc',
        namespace: 'ns',
        annotations: {
          'compliance.openshift.io/inconsistent-source': 'node0:PASS',
          'compliance.openshift.io/most-common-status': 'NOT-APPLICABLE',
        },
      },
      status: 'INCONSISTENT',
      severity: 'medium',
      description: 'Benign split',
    } as ComplianceCheckResult;
    const csv = resultsCsv([inconsistent]);
    expect(csvLines(csv)[1]).toBe('inc,Benign split,PASS,medium,false');
  });
  // Operator folds SKIP into notApplicable; CSV must match Overview N/A export.
  it('folds SKIP into NOT-APPLICABLE via resultFilterStatus', () => {
    const csv = resultsCsv([r('s1', 'SKIP', 'low', 'Skipped rule')]);
    expect(csvLines(csv)[1]).toBe('s1,Skipped rule,NOT-APPLICABLE,low,false');
  });
  // Missing/non-string status must not throw (csvCell used to call .replace on
  // undefined) and must export ERROR so the row matches operator ResultCounts.
  it('tolerates missing status and exports ERROR (operator tally parity)', () => {
    const missing = {
      metadata: { name: 'orphan', namespace: 'ns' },
      severity: 'low',
      description: 'No status field',
    } as unknown as ComplianceCheckResult;
    expect(() => resultsCsv([missing])).not.toThrow();
    expect(csvLines(resultsCsv([missing]))[1]).toBe(
      'orphan,No status field,ERROR,low,false',
    );
  });
  // Partial list items (no metadata) must not abort CSV export.
  it('tolerates missing metadata on a result row', () => {
    const bare = { status: 'PASS', severity: 'low' } as unknown as ComplianceCheckResult;
    expect(() => resultsCsv([bare])).not.toThrow();
    expect(csvLines(resultsCsv([bare]))[1]).toBe(',unknown,PASS,low,false');
  });
  it('fuzz: valid CSV (quotes balanced) for arbitrary CR text', () => {
    const rand = () =>
      Array.from({ length: Math.floor(fuzzRand() * 40) }, () =>
        String.fromCharCode(Math.floor(fuzzRand() * 128)),
      ).join('');
    // Formula-looking prefixes (CWE-1236) that must be neutralized with a leading '.
    const formulaSeeds = ['=cmd', '+SUM(1)', '-1', '@import', '|DDE', '\tTab', '\rCR', ' =eq', '\uFF1Dfull'];
    const formulaRe = /^\s*[=+\-@|\t\r\n\uFF1D\uFF0B\uFF0D\uFF20\u2212]/;
    for (let i = 0; i < 2000; i++) {
      const name = i < formulaSeeds.length ? formulaSeeds[i] : rand();
      const title = i % 3 === 0 ? formulaSeeds[i % formulaSeeds.length] : rand();
      const csv = resultsCsv([r(name, 'FAIL', 'high', title)]);
      expect(typeof csv).toBe('string');
      expect(csv.startsWith('\uFEFF')).toBe(true);
      // Total double-quotes are even (all escapes balanced).
      expect((csv.match(/"/g) ?? []).length % 2).toBe(0);
      // Five columns: name,title,status,severity,waived (header + one data row).
      const lines = csv.replace(/^\uFEFF/, '').split('\r\n');
      expect(lines).toHaveLength(2);
      // RFC 4180: split on commas outside quotes.
      const cols: string[] = [];
      let cell = '';
      let inQ = false;
      for (let j = 0; j < lines[1].length; j++) {
        const ch = lines[1][j];
        if (ch === '"') {
          inQ = !inQ;
          cell += ch;
        } else if (ch === ',' && !inQ) {
          cols.push(cell);
          cell = '';
        } else {
          cell += ch;
        }
      }
      cols.push(cell);
      expect(cols).toHaveLength(5);
      // No NULs survive export (can truncate cells in spreadsheet tools).
      expect(csv).not.toContain('\0');
      // Unquote RFC 4180 cells (outer quotes + doubled inner quotes).
      const unquote = (c: string): string => {
        if (c.length >= 2 && c.startsWith('"') && c.endsWith('"')) {
          return c.slice(1, -1).replace(/""/g, '"');
        }
        return c;
      };
      // Formula-looking name / rendered title must be apostrophe-prefixed in the row.
      const renderedTitle = checkTitle(r(name, 'FAIL', 'high', title));
      const nameClean = String(name ?? '').replace(/\0/g, '');
      const titleClean = String(renderedTitle ?? '').replace(/\0/g, '');
      if (formulaRe.test(nameClean) && nameClean.length > 0) {
        expect(unquote(cols[0]).startsWith("'")).toBe(true);
      }
      if (formulaRe.test(titleClean) && titleClean.length > 0) {
        expect(unquote(cols[1]).startsWith("'")).toBe(true);
      }
    }
  });
});

describe('checkResultHref', () => {
  it('builds a namespaced ComplianceCheckResult console path', () => {
    expect(checkResultHref('ocp4-cis-audit')).toBe(
      '/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/ocp4-cis-audit',
    );
  });
  it('encodes special characters in the name', () => {
    expect(checkResultHref('a b/c')).toContain(encodeURIComponent('a b/c'));
  });
  it('fuzz: always under the compliance path, encoded, never throws', () => {
    const prefix =
      '/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/';
    for (let i = 0; i < 1000; i++) {
      const name = randomString(i % 40);
      const href = checkResultHref(name);
      expect(href.startsWith(prefix)).toBe(true);
      // The name segment carries no unescaped path separator or whitespace.
      expect(href.slice(prefix.length)).not.toMatch(/[/\s#?]/);
    }
  });
});

describe('nodeScanPool', () => {
  const withScan = (scan?: string): ComplianceCheckResult =>
    ({
      metadata: { name: 'r', namespace: 'ns', labels: scan ? { 'compliance.openshift.io/scan-name': scan } : {} },
      status: 'INCONSISTENT',
    }) as ComplianceCheckResult;

  it('extracts the MachineConfigPool from a node scan name', () => {
    expect(nodeScanPool(withScan('ocp4-cis-node-worker'))).toBe('worker');
    expect(nodeScanPool(withScan('ocp4-cis-node-master'))).toBe('master');
    expect(nodeScanPool(withScan('ocp4-pci-dss-node-infra'))).toBe('infra');
    expect(nodeScanPool(withScan('custom-node-profile-node-worker'))).toBe('worker');
  });
  it('returns null for a platform (non-node) scan or missing label', () => {
    expect(nodeScanPool(withScan('ocp4-cis'))).toBeNull();
    expect(nodeScanPool(withScan())).toBeNull();
    expect(nodeScanPool(withScan('ocp4-cis-node-'))).toBeNull();
  });
  it('fuzz: never throws for arbitrary scan names', () => {
    for (let i = 0; i < 500; i++) {
      const out = nodeScanPool(withScan(randomString(i % 30)));
      expect(out === null || typeof out === 'string').toBe(true);
    }
  });
  it('machineConfigPoolHref builds an encoded MCP console path', () => {
    expect(machineConfigPoolHref('worker')).toBe(
      '/k8s/cluster/machineconfiguration.openshift.io~v1~MachineConfigPool/worker',
    );
    expect(machineConfigPoolHref('a b')).toContain(encodeURIComponent('a b'));
  });
});

describe('inconsistentSources', () => {
  const withAnn = (ann?: Record<string, string>): ComplianceCheckResult =>
    ({ metadata: { name: 'r', namespace: 'ns', annotations: ann }, status: 'INCONSISTENT' }) as ComplianceCheckResult;

  it('parses node:status pairs and the most-common status', () => {
    const { sources, mostCommon } = inconsistentSources(
      withAnn({
        'compliance.openshift.io/inconsistent-source': 'node0:PASS,worker1:FAIL',
        'compliance.openshift.io/most-common-status': 'NOT-APPLICABLE',
      }),
    );
    expect(sources).toEqual([
      { node: 'node0', status: 'PASS' },
      { node: 'worker1', status: 'FAIL' },
    ]);
    expect(mostCommon).toBe('NOT-APPLICABLE');
  });
  it('returns empty when the annotation is absent', () => {
    expect(inconsistentSources(withAnn())).toEqual({ sources: [], mostCommon: null });
    expect(inconsistentSources(withAnn({}))).toEqual({ sources: [], mostCommon: null });
  });
  it('tolerates a node name without a status and trims blanks', () => {
    const parsed = inconsistentSources(
      withAnn({
        'compliance.openshift.io/inconsistent-source': ' node0 , , n1 : PASS ',
        'compliance.openshift.io/most-common-status': ' NOT-APPLICABLE ',
      }),
    );
    expect(parsed.sources).toEqual([
      { node: 'node0', status: '' },
      { node: 'n1', status: 'PASS' },
    ]);
    expect(parsed.mostCommon).toBe('NOT-APPLICABLE');
    expect(effectiveStatus(withAnn({
      'compliance.openshift.io/inconsistent-source': 'n1 : PASS',
      'compliance.openshift.io/most-common-status': ' NOT-APPLICABLE ',
    }))).toBe('PASS');
  });
  // Lockstep with effectiveStatus / operator: odd casing must still map to labels.
  it('uppercases status tokens (matches effectiveStatus collapse)', () => {
    const { sources, mostCommon } = inconsistentSources(
      withAnn({
        'compliance.openshift.io/inconsistent-source': 'n0:pass,n1:fail',
        'compliance.openshift.io/most-common-status': ' not-applicable ',
      }),
    );
    expect(sources).toEqual([
      { node: 'n0', status: 'PASS' },
      { node: 'n1', status: 'FAIL' },
    ]);
    expect(mostCommon).toBe('NOT-APPLICABLE');
  });
  it('fuzz: never throws for arbitrary annotation strings', () => {
    for (let i = 0; i < 1000; i++) {
      const { sources } = inconsistentSources(
        withAnn({ 'compliance.openshift.io/inconsistent-source': randomString(i % 40) }),
      );
      expect(Array.isArray(sources)).toBe(true);
    }
  });
});

describe('changedChecks', () => {
  const res = (name: string, description?: string) =>
    ({ metadata: { name, namespace: 'openshift-compliance' }, description }) as ComplianceCheckResult;

  it('resolves names to title + deep-link, name as title fallback', () => {
    const results = [res('ocp4-cis-a', 'Audit profile set\nrationale')];
    const items = changedChecks(['ocp4-cis-a', 'ocp4-cis-missing'], results);
    expect(items).toHaveLength(2);
    expect(items[0]).toEqual({
      name: 'ocp4-cis-a',
      title: 'Audit profile set',
      href: expect.stringContaining('ocp4-cis-a'),
    });
    // Unknown name falls back to the raw name as its title.
    expect(items[1].title).toBe('ocp4-cis-missing');
  });
  it('filters empty names and tolerates undefined inputs', () => {
    expect(changedChecks(undefined, undefined)).toEqual([]);
    expect(changedChecks(['', 'x'], [])).toHaveLength(1);
  });
  // Regression / newlyFailed names and CCR descriptions are untrusted cluster text.
  it('fuzz: never throws; drops empty names; every item has name/title/href', () => {
    for (let i = 0; i < 500; i++) {
      const names = Array.from({ length: i % 8 }, (_, j) =>
        j % 3 === 0 ? '' : randomString(j % 24),
      );
      const results = names
        .filter(Boolean)
        .slice(0, 3)
        .map((name) => res(name, randomString(i % 40)));
      const items = changedChecks(names, results);
      expect(items.length).toBe(names.filter(Boolean).length);
      for (const x of items) {
        expect(x.name.length).toBeGreaterThan(0);
        expect(typeof x.title).toBe('string');
        expect(x.href).toContain(
          '/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/',
        );
      }
    }
  });
  it('changedChecksMany indexes multiple lists in one pass', () => {
    const results = [
      res('a', 'Title A'),
      res('b', 'Title B'),
      res('c', 'Title C'),
    ];
    const [nf, fx] = changedChecksMany(
      [
        ['a', 'missing'],
        ['c', ''],
      ],
      results,
    );
    expect(nf.map((x) => x.title)).toEqual(['Title A', 'missing']);
    expect(fx.map((x) => x.title)).toEqual(['Title C']);
    expect(changedChecksMany([undefined, []], results)).toEqual([[], []]);
  });
});

describe('isAlreadyExists', () => {
  it('detects an AlreadyExists apiserver rejection', () => {
    expect(isAlreadyExists({ reason: 'AlreadyExists' })).toBe(true);
    expect(isAlreadyExists({ code: 409, reason: 'AlreadyExists' })).toBe(true);
    expect(isAlreadyExists({ message: 'tailoredprofiles "x" already exists' })).toBe(true);
    expect(isAlreadyExists({ code: 409, message: 'tailoredprofiles "x" already exists' })).toBe(
      true,
    );
    expect(isAlreadyExists('tailoredprofiles "x" already exists')).toBe(true);
    expect(isAlreadyExists(new Error('tailoredprofiles "x" already exists'))).toBe(true);
    const named = new Error('conflict');
    named.name = 'AlreadyExists';
    expect(isAlreadyExists(named)).toBe(true);
  });
  // Console SDK HttpError: name is "HttpError", reason lives on .json (Status body).
  // Message may be the generic "Conflict" status text while reason is AlreadyExists.
  it('detects console HttpError with Status reason on .json', () => {
    const httpAlready = Object.assign(new Error('Conflict'), {
      name: 'HttpError',
      code: 409,
      json: {
        reason: 'AlreadyExists',
        message: 'tailoredprofiles.compliance.openshift.io "x" already exists',
      },
    });
    expect(isAlreadyExists(httpAlready)).toBe(true);
    const httpConflict = Object.assign(new Error('Conflict'), {
      name: 'HttpError',
      code: 409,
      json: { reason: 'Conflict', message: 'the object has been modified' },
    });
    expect(isAlreadyExists(httpConflict)).toBe(false);
    // Nested json on a plain object (serialized error shape).
    expect(
      isAlreadyExists({
        code: 409,
        message: 'Conflict',
        json: { reason: 'AlreadyExists', message: 'foo already exists' },
      }),
    ).toBe(true);
  });
  it('is false for Conflict (also HTTP 409) and other errors', () => {
    // Bare 409 is ambiguous (AlreadyExists vs Conflict); do not guess.
    expect(isAlreadyExists({ code: 409 })).toBe(false);
    expect(isAlreadyExists({ code: 409, reason: 'Conflict' })).toBe(false);
    expect(isAlreadyExists({ code: 409, message: 'the object has been modified' })).toBe(false);
    expect(isAlreadyExists({ code: 403 })).toBe(false);
    expect(isAlreadyExists(new Error('boom'))).toBe(false);
    expect(isAlreadyExists('forbidden')).toBe(false);
    expect(isAlreadyExists(null)).toBe(false);
  });
  // Untrusted watch/fetch error shapes (partial Status, throwing getters) must
  // never throw; create-retry classification cannot become a second failure mode.
  it('fuzz: never throws; always returns boolean', () => {
    const hostile: unknown[] = [
      null,
      undefined,
      '',
      'already exists',
      'forbidden',
      409,
      true,
      Symbol('s'),
      Object.create(null),
      {
        get reason() {
          throw new Error('getter boom');
        },
      },
      {
        get message() {
          throw new Error('msg boom');
        },
      },
      {
        get json() {
          throw new Error('json boom');
        },
      },
      {
        reason: 'AlreadyExists',
        get message() {
          throw new Error('nested');
        },
      },
      Object.assign(new Error('Conflict'), {
        name: 'HttpError',
        json: {
          get reason() {
            throw new Error('json.reason');
          },
        },
      }),
    ];
    for (let i = 0; i < 2000; i++) {
      const pool: unknown[] = [
        ...hostile,
        randomString(i % 40),
        { reason: i % 3 === 0 ? 'AlreadyExists' : randomString(i % 12) },
        { code: 409, reason: randomString(i % 10), message: randomString(i % 24) },
        { message: i % 5 === 0 ? 'foo already exists' : randomString(i % 20) },
        new Error(i % 4 === 0 ? 'x already exists' : randomString(i % 16)),
        Object.assign(new Error('Conflict'), {
          name: 'HttpError',
          code: 409,
          json: { reason: i % 2 === 0 ? 'AlreadyExists' : 'Conflict', message: randomString(i % 18) },
        }),
        { json: { reason: 'AlreadyExists', message: randomString(i % 10) } },
        [randomString(i % 8)],
      ];
      let out: boolean | undefined;
      expect(() => {
        out = isAlreadyExists(pool[i % pool.length]);
      }).not.toThrow();
      expect(typeof out).toBe('boolean');
    }
  });
});

describe('effectiveStatus', () => {
  const inc = (ann: Record<string, string>) =>
    ({ status: 'INCONSISTENT', metadata: { annotations: ann } }) as unknown as ComplianceCheckResult;

  it('passes through a non-inconsistent status unchanged', () => {
    expect(effectiveStatus({ status: 'FAIL', metadata: {} })).toBe('FAIL');
    expect(effectiveStatus({ status: 'PASS', metadata: {} })).toBe('PASS');
  });
  // Operator tally maps unknown/empty status to ERROR; UI/CSV must match so a
  // missing field is not a blank filter chip or a thrown CSV export.
  it('maps empty or non-string status to ERROR (operator tally parity)', () => {
    expect(effectiveStatus({ status: '', metadata: {} })).toBe('ERROR');
    expect(effectiveStatus({ status: undefined as unknown as string, metadata: {} })).toBe(
      'ERROR',
    );
    expect(effectiveStatus({ status: null as unknown as string, metadata: {} })).toBe('ERROR');
    expect(effectiveStatus({ status: 42 as unknown as string, metadata: {} })).toBe('ERROR');
  });
  // Operator ResultCounts fold SKIP into notApplicable; Overview N/A deep-links
  // and the Results filter must match that bucket.
  it('folds top-level SKIP into NOT-APPLICABLE', () => {
    expect(effectiveStatus({ status: 'SKIP', metadata: {} })).toBe('NOT-APPLICABLE');
  });
  it('collapses PASS + NOT-APPLICABLE to PASS', () => {
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'node0:PASS',
          'compliance.openshift.io/most-common-status': 'NOT-APPLICABLE',
        }),
      ),
    ).toBe('PASS');
  });
  // All nodes agree PASS must not remain INCONSISTENT (uniform multi-node result).
  it('collapses multi-node all-PASS to PASS', () => {
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'n0:PASS,n1:PASS,n2:PASS',
          'compliance.openshift.io/most-common-status': 'PASS',
        }),
      ),
    ).toBe('PASS');
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'n0:PASS,n1:PASS',
        }),
      ),
    ).toBe('PASS');
  });
  it('collapses all-not-applicable to NOT-APPLICABLE', () => {
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'node0:NOT-APPLICABLE',
          'compliance.openshift.io/most-common-status': 'NOT-APPLICABLE',
        }),
      ),
    ).toBe('NOT-APPLICABLE');
  });
  it('keeps a genuine PASS/FAIL split as INCONSISTENT', () => {
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'node0:FAIL',
          'compliance.openshift.io/most-common-status': 'PASS',
        }),
      ),
    ).toBe('INCONSISTENT');
  });
  // Operator parity: ERROR among nodes is a genuine conflict; SKIP-only is benign.
  it('keeps ERROR among nodes as INCONSISTENT', () => {
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'node0:ERROR',
          'compliance.openshift.io/most-common-status': 'PASS',
        }),
      ),
    ).toBe('INCONSISTENT');
  });
  it('collapses SKIP-only disagreement to NOT-APPLICABLE', () => {
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'node0:SKIP',
          'compliance.openshift.io/most-common-status': 'SKIP',
        }),
      ),
    ).toBe('NOT-APPLICABLE');
  });
  it('keeps unknown/empty states as INCONSISTENT', () => {
    // Empty annotations: no node states to collapse -> stay INCONSISTENT.
    expect(effectiveStatus(inc({}))).toBe('INCONSISTENT');
    expect(
      effectiveStatus(
        inc({
          'compliance.openshift.io/inconsistent-source': 'node0:FUTURE-STATE',
          'compliance.openshift.io/most-common-status': 'PASS',
        }),
      ),
    ).toBe('INCONSISTENT');
  });
  // Untrusted CO annotations; collapse must stay in the known status set and
  // never throw. FAIL/ERROR among nodes must fail closed as INCONSISTENT.
  it('fuzz: never throws; result in {PASS,NOT-APPLICABLE,INCONSISTENT,passthrough}', () => {
    const passthrough = ['PASS', 'FAIL', 'ERROR', 'MANUAL', 'INFO', 'SKIP', 'NOT-APPLICABLE'];
    for (let i = 0; i < 2000; i++) {
      const rawStatus = i % 7 === 0 ? 'INCONSISTENT' : passthrough[i % passthrough.length];
      const r = {
        status: rawStatus,
        metadata: {
          annotations: {
            'compliance.openshift.io/inconsistent-source':
              i % 3 === 0
                ? randomString(i % 40)
                : i % 3 === 1
                  ? `n0:${['PASS', 'FAIL', 'ERROR', 'SKIP', 'NOT-APPLICABLE', 'X'][i % 6]}`
                  : `n0:PASS,n1:${randomString(i % 8)}`,
            'compliance.openshift.io/most-common-status':
              i % 4 === 0 ? randomString(i % 12) : ['PASS', 'NOT-APPLICABLE', ''][i % 3],
          },
        },
      };
      let got: string;
      expect(() => {
        got = effectiveStatus(r);
      }).not.toThrow();
      if (rawStatus !== 'INCONSISTENT') {
        // SKIP is folded into NOT-APPLICABLE (operator ResultCounts parity).
        expect(got!).toBe(rawStatus === 'SKIP' ? 'NOT-APPLICABLE' : rawStatus);
        continue;
      }
      expect(['PASS', 'NOT-APPLICABLE', 'INCONSISTENT']).toContain(got!);
      const src = r.metadata.annotations['compliance.openshift.io/inconsistent-source'];
      const mc = r.metadata.annotations['compliance.openshift.io/most-common-status'];
      // Fail-closed: FAIL/ERROR as a per-node or most-common status must not collapse.
      if (
        /:(?:\s*)(FAIL|ERROR)(?:\s*(?:,|$))/i.test(src) ||
        /^(FAIL|ERROR)$/i.test(mc.trim())
      ) {
        expect(got!).toBe('INCONSISTENT');
      }
    }
  });
});

describe('remediation helpers', () => {
  const rem = (
    kind?: string,
    obj?: Record<string, unknown>,
    extra?: Partial<ComplianceRemediation>,
  ): ComplianceRemediation =>
    ({
      metadata: { name: 'r', namespace: 'openshift-compliance', ...(extra?.metadata ?? {}) },
      spec: {
        apply: false,
        current: obj ? { object: obj } : kind ? { object: { kind } } : undefined,
        ...(extra?.spec ?? {}),
      },
      status: extra?.status,
    }) as ComplianceRemediation;

  it('isNodeRemediation detects MachineConfig', () => {
    expect(isNodeRemediation(rem('MachineConfig'))).toBe(true);
    expect(isNodeRemediation(rem('APIServer'))).toBe(false);
    expect(isNodeRemediation(rem())).toBe(false);
  });
  // Parity with operator poolFromRemediation: empty kind + node scan-name is a
  // node remediation (reboot warning / batch eligibility).
  it('isNodeRemediation falls back to scan-name when kind is empty', () => {
    expect(
      isNodeRemediation(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-node-worker' },
          },
        }),
      ),
    ).toBe(true);
    expect(
      isNodeRemediation(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis' },
          },
        }),
      ),
    ).toBe(false);
    // Operator validMCPPoolName: non-DNS-1123 pool suffix is not a batch target.
    expect(
      isNodeRemediation(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-node-UPPER' },
          },
        }),
      ),
    ).toBe(false);
    // A non-MachineConfig kind rendered by a node scan (e.g. a KubeletConfig, which
    // the MCO applies with a reboot) is STILL a node remediation: the "…-node-<pool>"
    // scan-name is authoritative, matching operator poolFromRemediation. The kind
    // must not short-circuit the fallback, or such a remediation would reboot the
    // pool with no warning and outside the batch pause window.
    expect(
      isNodeRemediation(
        rem('KubeletConfig', undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-node-worker' },
          },
        }),
      ),
    ).toBe(true);
    // But a platform kind on a scan with no "-node-" is not a node remediation.
    expect(
      isNodeRemediation(
        rem('APIServer', undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-api-server' },
          },
        }),
      ),
    ).toBe(false);
  });
  it('remediationObjectText pretty-prints the object, empty when absent', () => {
    expect(remediationObjectText(rem('MachineConfig', { kind: 'MachineConfig', x: 1 }))).toContain(
      '"kind": "MachineConfig"',
    );
    expect(remediationObjectText(rem())).toBe('');
  });
  it('remediationObjectText does not throw on circular rendered objects', () => {
    const circular: Record<string, unknown> = { kind: 'MachineConfig' };
    circular.self = circular;
    // Non-empty marker: UI must not show the same empty state as missing object.
    expect(remediationObjectText(rem('MachineConfig', circular))).toBe(
      '/* unserializable remediation object */',
    );
  });
  // openspec guided-remediation: MissingDependencies must name the dependency.
  it('missingDependencySummary reads depends-on, depends-on-obj, and unset-value', () => {
    expect(missingDependencySummary(rem())).toBeNull();
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: {
              'compliance.openshift.io/depends-on': 'xccdf_org.ssgproject.content_rule_a, rule_b',
            },
          },
        }),
      ),
    ).toBe('xccdf_org.ssgproject.content_rule_a, rule_b');
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: {
              'compliance.openshift.io/depends-on-obj': JSON.stringify([
                {
                  apiVersion: 'v1',
                  kind: 'ConfigMap',
                  name: 'foo',
                  namespace: 'openshift-compliance',
                },
              ]),
            },
          },
        }),
      ),
    ).toBe('ConfigMap openshift-compliance/foo');
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: { 'compliance.openshift.io/unset-value': 'var_password_min_len' },
          },
        }),
      ),
    ).toBe('value:var_password_min_len');
    // Malformed JSON: surface raw rather than empty.
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: { 'compliance.openshift.io/depends-on-obj': '{not-json' },
          },
        }),
      ),
    ).toBe('{not-json');
    // Fall back to status.errorMessage when annotations are empty.
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          status: { applicationState: 'Error', errorMessage: 'apply failed: conflict' },
        }),
      ),
    ).toBe('apply failed: conflict');
  });
  it('compareRemediationsForApplyOrder puts MissingDependencies last', () => {
    const a = rem(undefined, undefined, {
      metadata: { name: 'z-ready', namespace: 'ns' },
      status: { applicationState: 'NotApplied' },
    });
    const b = rem(undefined, undefined, {
      metadata: { name: 'a-blocked', namespace: 'ns' },
      status: { applicationState: 'MissingDependencies' },
    });
    const c = rem(undefined, undefined, {
      metadata: { name: 'm-ready', namespace: 'ns' },
      status: { applicationState: 'NotApplied' },
    });
    const sorted = [b, a, c].sort(compareRemediationsForApplyOrder);
    expect(sorted.map((r) => r.metadata.name)).toEqual(['m-ready', 'z-ready', 'a-blocked']);
  });
  it('fuzz: returns a string and never throws for arbitrary rendered objects', () => {
    for (let i = 0; i < 1000; i++) {
      const obj: Record<string, unknown> = {
        kind: randomString(i % 12),
        [randomString((i % 6) + 1)]: i,
        nested: { a: [randomString(i % 5)], b: i % 2 === 0 },
        weird: i % 13 === 0 ? undefined : randomString(i % 10),
      };
      const out = remediationObjectText(rem('X', obj));
      expect(typeof out).toBe('string');
      expect(missingDependencySummary(rem('X', obj))).toBeNull();
    }
  });
  it('fuzz: missingDependencySummary never throws on hostile annotations', () => {
    for (let i = 0; i < 500; i++) {
      const summary = missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'ns',
            annotations: {
              'compliance.openshift.io/depends-on': randomString(i % 40),
              'compliance.openshift.io/depends-on-obj': randomString(i % 40),
              'compliance.openshift.io/unset-value': randomString(i % 20),
            },
          },
          status: { errorMessage: randomString(i % 20) },
        }),
      );
      expect(summary === null || typeof summary === 'string').toBe(true);
    }
  });
  // Kind + scan-name are untrusted CO fields; reboot/batch eligibility must not
  // throw, and node-ness is MachineConfig OR a DNS-valid "-node-<pool>" scan name.
  it('fuzz: isNodeRemediation never throws and matches the node-scan invariant', () => {
    for (let i = 0; i < 500; i++) {
      const kind =
        i % 7 === 0 ? 'MachineConfig' : i % 7 === 1 ? '' : randomString(i % 12);
      const scan =
        i % 5 === 0
          ? `ocp4-cis-node-${randomString((i % 8) + 1)}`
          : randomString(i % 20);
      const out = isNodeRemediation(
        rem(kind || undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'ns',
            labels: { 'compliance.openshift.io/scan-name': scan },
          },
        }),
      );
      expect(typeof out).toBe('boolean');
      // Exact invariant (lockstep with operator poolFromRemediation): a
      // MachineConfig is always a node remediation; any other kind is node iff its
      // scan name ends in a DNS-valid "-node-<pool>". Non-vacuous: asserts the
      // real boolean, so a kind that wrongly short-circuits the scan fallback
      // (the round-10 bug) would fail here.
      const di = scan.lastIndexOf('-node-');
      const pool = di < 0 ? '' : scan.slice(di + '-node-'.length);
      const want = kind === 'MachineConfig' || (pool !== '' && isValidK8sName(pool));
      expect(out).toBe(want);
    }
  });
  it('fuzz: compareRemediationsForApplyOrder is antisymmetric and total', () => {
    for (let i = 0; i < 200; i++) {
      const a = rem(undefined, undefined, {
        metadata: { name: randomString((i % 10) + 1), namespace: 'ns' },
        status: {
          applicationState: i % 3 === 0 ? 'MissingDependencies' : 'NotApplied',
        },
      });
      const b = rem(undefined, undefined, {
        metadata: { name: randomString((i % 10) + 1), namespace: 'ns' },
        status: {
          applicationState: i % 5 === 0 ? 'MissingDependencies' : 'NotApplied',
        },
      });
      const ab = compareRemediationsForApplyOrder(a, b);
      const ba = compareRemediationsForApplyOrder(b, a);
      expect(Number.isFinite(ab)).toBe(true);
      expect(Number.isFinite(ba)).toBe(true);
      if (ab === 0) {
        expect(ba).toBe(0);
      } else {
        expect(Math.sign(ab)).toBe(-Math.sign(ba));
      }
      expect(compareRemediationsForApplyOrder(a, a)).toBe(0);
    }
  });
  // Partial list-watch items must not throw mid-sort.
  it('compareRemediationsForApplyOrder tolerates missing names', () => {
    const a = rem(undefined, undefined, {
      metadata: { name: undefined as unknown as string, namespace: 'ns' },
    });
    const b = rem(undefined, undefined, {
      metadata: { name: 'b', namespace: 'ns' },
    });
    expect(() => compareRemediationsForApplyOrder(a, b)).not.toThrow();
    expect(Number.isFinite(compareRemediationsForApplyOrder(a, b))).toBe(true);
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
      } else if (status === '') {
        // Empty status maps to ERROR (operator tally parity).
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

describe('schedule editor helpers', () => {
  it('isValidCron accepts 5 fields, rejects otherwise', () => {
    expect(isValidCron('0 1 * * *')).toBe(true);
    expect(isValidCron('*/5 0-6 1,15 * 1-5')).toBe(true);
    expect(isValidCron('0 1 * JAN MON-FRI')).toBe(true);
    expect(isValidCron('0 1 ? * MON')).toBe(true);
    expect(isValidCron('0 1 * *')).toBe(false); // 4 fields
    expect(isValidCron('0 1 * * * *')).toBe(false); // 6 fields
    expect(isValidCron('not a cron')).toBe(false);
    expect(isValidCron('@every 1s')).toBe(false);
    expect(isValidCron('60 1 * * *')).toBe(false);
    expect(isValidCron('0 24 * * *')).toBe(false);
    expect(isValidCron('0 1 * * 7')).toBe(false);
    expect(isValidCron('*/0 1 * * *')).toBe(false);
    expect(isValidCron('')).toBe(false);
    // CRD MaxLength=128: a long-but-five-field string must not pass client-side.
    expect(isValidCron(`0 1 * * ${'1'.repeat(200)}`)).toBe(false);
  });

  // Schedule editor feeds free-form text into isValidCron before patching the
  // CR; arbitrary input must never throw and must only accept 5-field forms.
  it('fuzz: isValidCron never throws; true implies five fields', () => {
    const seeds = [
      '0 1 * * *',
      '*/5 0-6 1,15 * 1-5',
      '@daily',
      '',
      '0 1 * * * *',
      '0 1 * JAN MON',
      '60 1 * * *',
    ];
    for (let i = 0; i < 2000; i++) {
      const s = i < seeds.length ? seeds[i] : randomString(i % 64);
      let ok: boolean;
      expect(() => {
        ok = isValidCron(s);
      }).not.toThrow();
      expect(typeof ok!).toBe('boolean');
      if (ok!) {
        expect(s.trim().split(/\s+/)).toHaveLength(5);
      }
    }
  });

  it('schedulePatch always adds a valid cron (creates or replaces the leaf)', () => {
    expect(schedulePatch(true, '0 2 * * *')).toEqual([
      { op: 'add', path: '/spec/schedule', value: '0 2 * * *' },
    ]);
    expect(schedulePatch(false, '0 2 * * *')).toEqual([
      { op: 'add', path: '/spec/schedule', value: '0 2 * * *' },
    ]);
    // Trims before validate/write so surrounding whitespace does not fail admission.
    expect(schedulePatch(true, '  0 3 * * *  ')).toEqual([
      { op: 'add', path: '/spec/schedule', value: '0 3 * * *' },
    ]);
  });

  it('schedulePatch is a no-op for invalid cron (fail closed before admission)', () => {
    expect(schedulePatch(true, '')).toEqual([]);
    expect(schedulePatch(false, '@every 1s')).toEqual([]);
    expect(schedulePatch(true, '0 1 * *')).toEqual([]);
    expect(schedulePatch(true, '60 1 * * *')).toEqual([]);
  });
});

describe('resourceVersionTest', () => {
  // Optimistic concurrency op prepended to every ClusterBaseline mutation path.
  it('emits a resourceVersion test op when RV is known', () => {
    expect(resourceVersionTest('42')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '42' },
    ]);
  });
  it('emits nothing when RV is missing (no false conflict)', () => {
    expect(resourceVersionTest(undefined)).toEqual([]);
    expect(resourceVersionTest('')).toEqual([]);
  });
});

describe('tailoredProfileBindingPatch', () => {
  it('is idempotent when the profile is already bound', () => {
    expect(tailoredProfileBindingPatch(['custom'], 'custom', '12')).toEqual([]);
  });
  it('guards and appends to an existing list', () => {
    expect(tailoredProfileBindingPatch(['old'], 'custom', '12')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '12' },
      { op: 'test', path: '/spec/tailoredProfiles', value: ['old'] },
      { op: 'add', path: '/spec/tailoredProfiles/-', value: 'custom' },
    ]);
  });
  it('guards creation of an absent list against concurrent replacement', () => {
    expect(tailoredProfileBindingPatch(undefined, 'custom', '12')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '12' },
      { op: 'add', path: '/spec/tailoredProfiles', value: ['custom'] },
    ]);
  });
  // Without resourceVersion the list-test/add still applies, but no RV guard.
  it('appends without a resourceVersion guard when RV is absent', () => {
    expect(tailoredProfileBindingPatch(['old'], 'custom')).toEqual([
      { op: 'test', path: '/spec/tailoredProfiles', value: ['old'] },
      { op: 'add', path: '/spec/tailoredProfiles/-', value: 'custom' },
    ]);
    expect(tailoredProfileBindingPatch(undefined, 'custom')).toEqual([
      { op: 'add', path: '/spec/tailoredProfiles', value: ['custom'] },
    ]);
  });
  it('is a no-op for invalid tailored names (CRD MaxLength 51 / DNS-1123)', () => {
    expect(tailoredProfileBindingPatch(undefined, '')).toEqual([]);
    expect(tailoredProfileBindingPatch(undefined, 'Bad_Name')).toEqual([]);
    expect(tailoredProfileBindingPatch(undefined, 'a'.repeat(52))).toEqual([]);
  });
  it('refuses a new bind past CRD MaxItems=32 (already-bound stays no-op)', () => {
    const full = Array.from({ length: 32 }, (_, i) => `tp-${i}`);
    expect(tailoredProfileBindingPatch(full, 'tp-new', '9')).toEqual([]);
    // Already bound: still a no-op even at the limit (not an over-limit reject path).
    expect(tailoredProfileBindingPatch(full, 'tp-0', '9')).toEqual([]);
  });
});

describe('batchApplyPatch', () => {
  it('adds the annotation, creating the map when absent', () => {
    expect(batchApplyPatch(true, ['a', 'b'])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.openshift.io~1batch-apply', value: 'a,b' },
    ]);
    expect(batchApplyPatch(false, ['a'])).toEqual([
      { op: 'add', path: '/metadata/annotations', value: { 'baselinesecurity.openshift.io/batch-apply': 'a' } },
    ]);
  });
  it('is a no-op for empty, blank, or invalid names', () => {
    expect(batchApplyPatch(true, [])).toEqual([]);
    expect(batchApplyPatch(true, ['', '  ', ','])).toEqual([]);
    expect(batchApplyPatch(false, ['Bad_Name', 'UPPER'])).toEqual([]);
  });
  it('dedupes, trims, and caps at the operator batch limit', () => {
    expect(batchApplyPatch(true, [' a ', 'b', 'a', 'b '])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.openshift.io~1batch-apply', value: 'a,b' },
    ]);
    const many = Array.from({ length: 300 }, (_, i) => `rem-${i}`);
    const patch = batchApplyPatch(true, many);
    expect(patch).toHaveLength(1);
    const value = (patch[0] as { value: string }).value;
    expect(value.split(',')).toHaveLength(256);
    expect(value.startsWith('rem-0,')).toBe(true);
    expect(value.endsWith(',rem-255')).toBe(true);
  });
  // Free-form remediation names from multi-select / deep-links before annotation write.
  it('fuzz: never throws; empty or single add; value names are DNS-1123 and <=256', () => {
    for (let i = 0; i < 500; i++) {
      const names = Array.from({ length: i % 20 }, (_, j) =>
        j % 4 === 0
          ? `rem-${j}`
          : j % 4 === 1
            ? randomString(j % 30)
            : j % 4 === 2
              ? `  rem-${j}  `
              : '',
      );
      const patch = batchApplyPatch(i % 2 === 0, names);
      expect(Array.isArray(patch)).toBe(true);
      expect(patch.length).toBeLessThanOrEqual(1);
      if (patch.length === 0) continue;
      const value =
        typeof patch[0].value === 'string'
          ? patch[0].value
          : (patch[0].value as { 'baselinesecurity.openshift.io/batch-apply': string })[
              'baselinesecurity.openshift.io/batch-apply'
            ];
      const parts = value.split(',');
      expect(parts.length).toBeLessThanOrEqual(256);
      expect(new Set(parts).size).toBe(parts.length);
      for (const p of parts) {
        expect(isValidK8sName(p)).toBe(true);
      }
    }
  });
});

// Operator treats empty / comma-only batch-apply values as no request; key presence alone is not enough.
describe('batchApplyRequested', () => {
  const key = 'baselinesecurity.openshift.io/batch-apply';
  it('is false when missing, empty, whitespace, or comma-only', () => {
    expect(batchApplyRequested(undefined)).toBe(false);
    expect(batchApplyRequested(null)).toBe(false);
    expect(batchApplyRequested({})).toBe(false);
    expect(batchApplyRequested({ [key]: '' })).toBe(false);
    expect(batchApplyRequested({ [key]: '   ' })).toBe(false);
    expect(batchApplyRequested({ [key]: ',, , ' })).toBe(false);
  });
  it('is true when any non-empty remediation name token is present', () => {
    expect(batchApplyRequested({ [key]: 'a' })).toBe(true);
    expect(batchApplyRequested({ [key]: ' a , b ' })).toBe(true);
    expect(batchApplyRequested({ [key]: ',,rem-1,' })).toBe(true);
  });
  // Annotation value is CR-editable; must never throw and true only when a
  // non-empty comma token exists after trim (operator batchRemediationNames).
  it('fuzz: never throws; true iff a non-empty token exists', () => {
    for (let i = 0; i < 1000; i++) {
      const raw =
        i % 6 === 0
          ? undefined
          : i % 6 === 1
            ? null
            : i % 6 === 2
              ? {}
              : i % 6 === 3
                ? { [key]: '' }
                : i % 6 === 4
                  ? { [key]: ',, ,\t,' }
                  : { [key]: randomString(i % 64) + (i % 3 === 0 ? ',rem' : '') };
      let got: boolean;
      expect(() => {
        got = batchApplyRequested(raw as never);
      }).not.toThrow();
      expect(typeof got!).toBe('boolean');
      if (raw == null || typeof raw !== 'object') {
        expect(got!).toBe(false);
        continue;
      }
      const val = (raw as Record<string, unknown>)[key];
      if (typeof val !== 'string') {
        expect(got!).toBe(false);
        continue;
      }
      const hasToken = val.split(',').some((p) => p.trim());
      expect(got!).toBe(hasToken);
    }
  });
});

describe('buildReportHtml', () => {
  const cb = {
    metadata: { name: 'cluster' },
    spec: {
      profiles: ['cis'],
      waivers: [
        { name: 'chk', reason: '<script>x</script>', requestedBy: 'a', expiresAt: '2099-01-01T00:00:00Z' },
        { name: 'old', reason: 'r', expiresAt: '2000-01-01T00:00:00Z' },
      ],
    },
    status: {
      score: 94,
      lastScanTime: '2026-07-11T09:00:00Z',
      profiles: [{ key: 'cis', profileNames: [], pass: 212, fail: 7, manual: 21, info: 0, error: 0, inconsistent: 37, waived: 0, notApplicable: 0 }],
    },
  } as unknown as ClusterBaseline;
  const now = new Date('2026-07-11T00:00:00Z');
  const reportResults = [
    {
      metadata: {
        name: 'fail-check',
        namespace: 'openshift-compliance',
        labels: { 'compliance.openshift.io/suite': 'baseline-cis' },
      },
      status: 'FAIL',
      severity: 'high',
      description: 'Fail <script>title</script>',
    },
    {
      metadata: {
        name: 'foreign-fail',
        namespace: 'openshift-compliance',
        labels: { 'compliance.openshift.io/suite': 'foreign' },
      },
      status: 'FAIL',
      severity: 'high',
    },
  ] as ComplianceCheckResult[];
  const html = buildReportHtml(cb, reportResults, now);
  it('includes score and per-profile counts', () => {
    expect(html).toContain('94 / 100');
    expect(html).toContain('CIS');
    expect(html).toContain('212');
  });
  it('escapes untrusted waiver text (no raw script tag)', () => {
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>x</script>');
  });
  it('sets a no-script Content-Security-Policy on the report document', () => {
    expect(html).toContain('Content-Security-Policy');
    expect(html).toContain("default-src 'none'");
    // Embedded chrome CSS needs style-src; scripts blocked via default-src only.
    expect(html).toMatch(/style-src 'unsafe-inline'/);
    expect(html).not.toMatch(/script-src/);
  });
  it('lists only active (non-expired) waivers', () => {
    expect(html).toContain('chk');
    expect(html).not.toContain('>old<');
    expect(html).toContain('Active waivers (1)');
  });
  it('lists owned unwaived failures and escapes their titles', () => {
    expect(html).toContain('fail-check');
    expect(html).toContain('Fail &lt;script&gt;title&lt;/script&gt;');
    expect(html).not.toContain('foreign-fail');
  });
  // Waiver reasons, check names/titles, and profile keys are untrusted CR text.
  // The report must never throw and must never emit raw & < > " ' from those fields.
  it('fuzz: never throws; escapes untrusted waiver/check text', () => {
    for (let i = 0; i < 500; i++) {
      const hostile = randomString(i % 48);
      const baseline = {
        metadata: { name: 'cluster' },
        spec: {
          profiles: ['cis'],
          waivers: [
            {
              name: hostile || 'n',
              reason: hostile,
              requestedBy: hostile,
              approvedBy: hostile,
              expiresAt: '2099-01-01T00:00:00Z',
            },
          ],
        },
        status: {
          score: i % 101,
          profiles: [
            {
              key: hostile || 'cis',
              profileNames: [],
              pass: 1,
              fail: 0,
              manual: 0,
              info: 0,
              error: 0,
              inconsistent: 0,
              waived: 0,
              notApplicable: 0,
            },
          ],
        },
      } as unknown as ClusterBaseline;
      const results = [
        {
          metadata: {
            name: hostile || 'chk',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/suite': 'baseline-cis' },
          },
          status: 'FAIL',
          severity: 'high',
          description: hostile,
        },
      ] as ComplianceCheckResult[];
      let out: string;
      expect(() => {
        out = buildReportHtml(baseline, results, now);
      }).not.toThrow();
      expect(typeof out!).toBe('string');
      // Non-string tampered CR fields must not throw through esc()/checkTitle.
      if (i === 0) {
        const tampered = {
          metadata: { name: 42 },
          spec: {
            profiles: ['cis'],
            waivers: [{ name: 7, reason: {}, requestedBy: null, approvedBy: [], expiresAt: '2099-01-01T00:00:00Z' }],
          },
          status: { score: 'x', profiles: [{ key: 3, pass: 'a', fail: null }] },
        } as unknown as ClusterBaseline;
        const tamperedResults = [
          { metadata: { name: 9, labels: { 'compliance.openshift.io/suite': 'baseline-cis' } }, status: 'FAIL', severity: 5, description: 12 },
        ] as unknown as ComplianceCheckResult[];
        expect(() => buildReportHtml(tampered, tamperedResults, now)).not.toThrow();
      }
      // Raw angle-bracket script from untrusted fields must not appear unescaped.
      if (hostile.includes('<') || hostile.includes('>') || hostile.includes('&')) {
        // At least one escaped entity should appear when specials were present.
        const hasEntity =
          out!.includes('&lt;') ||
          out!.includes('&gt;') ||
          out!.includes('&amp;') ||
          out!.includes('&quot;') ||
          out!.includes('&#39;');
        // Empty/whitespace-only hostile may not land in a cell; only assert when
        // the raw special would otherwise be injectable as element text.
        if (hostile.trim()) {
          expect(hasEntity || !out!.includes(hostile)).toBe(true);
        }
      }
    }
  });
});

describe('tailoredProfileManifest', () => {
  it('keeps a rule present in both enable and disable only in disable (fail closed)', () => {
    // (name, extends, disableRules, enableRules); a rule in both must not ship in
    // both enableRules and disableRules (self-conflicting manifest).
    const m = tailoredProfileManifest('cis-custom', 'ocp4-cis', ['dup', 'off-only'], [
      'dup',
      'on-only',
    ]);
    const spec = m.spec as {
      enableRules?: { name: string }[];
      disableRules?: { name: string }[];
    };
    const enabled = (spec.enableRules ?? []).map((r) => r.name);
    const disabled = (spec.disableRules ?? []).map((r) => r.name);
    expect(disabled).toContain('dup');
    expect(enabled).not.toContain('dup');
    expect(enabled).toContain('on-only');
  });
  it('builds a TailoredProfile CR, omitting empty rule lists', () => {
    const m = tailoredProfileManifest('cis-custom', 'ocp4-cis', []);
    expect(m.kind).toBe('TailoredProfile');
    expect((m.metadata as { name: string }).name).toBe('cis-custom');
    expect((m.spec as { extends: string }).extends).toBe('ocp4-cis');
    expect((m.spec as Record<string, unknown>).disableRules).toBeUndefined();
  });
  it('includes enable/disable rules when provided', () => {
    const m = tailoredProfileManifest('x', 'ocp4-cis', ['r1', 'r2'], ['r3']);
    const spec = m.spec as { enableRules: { name: string }[]; disableRules: { name: string }[] };
    expect(spec.disableRules.map((r) => r.name)).toEqual(['r1', 'r2']);
    expect(spec.enableRules.map((r) => r.name)).toEqual(['r3']);
  });
  it('drops non-DNS-1123 rule names; empty extends defaults to ocp4-cis', () => {
    const m = tailoredProfileManifest(
      'x',
      '',
      ['ok-rule', 'bad name', '../x', 'ok-rule', ''],
      ['also-ok', 'has spaces'],
    );
    const spec = m.spec as { extends: string; enableRules?: { name: string }[]; disableRules?: { name: string }[] };
    expect(spec.extends).toBe('ocp4-cis');
    expect(spec.disableRules?.map((r) => r.name)).toEqual(['ok-rule']);
    expect(spec.enableRules?.map((r) => r.name)).toEqual(['also-ok']);
  });
  it('refuses invalid base profile extends (no silent CIS substitution)', () => {
    expect(() => tailoredProfileManifest('x', 'not a profile!!!', [])).toThrow(
      /invalid base profile name/,
    );
    expect(() => tailoredProfileManifest('x', '../evil', [])).toThrow(/invalid base profile name/);
  });
  it('refuses invalid metadata.name (path-shaped / over-long / empty)', () => {
    expect(() => tailoredProfileManifest('../x', 'ocp4-cis', [])).toThrow(/invalid TailoredProfile name/);
    expect(() => tailoredProfileManifest('', 'ocp4-cis', [])).toThrow(/invalid TailoredProfile name/);
    expect(() => tailoredProfileManifest('a'.repeat(52), 'ocp4-cis', [])).toThrow(
      /invalid TailoredProfile name/,
    );
    expect(() => tailoredProfileManifest('has spaces', 'ocp4-cis', [])).toThrow(
      /invalid TailoredProfile name/,
    );
  });
  it('trims a valid name before writing metadata and title', () => {
    const m = tailoredProfileManifest('  cis-custom  ', 'ocp4-cis', []);
    expect((m.metadata as { name: string }).name).toBe('cis-custom');
    expect((m.spec as { title: string }).title).toBe('cis-custom');
  });
  // Form free-text (name, extends, rule lists) is untrusted. Invalid inputs must
  // throw a typed Error (fail closed) or produce a DNS-1123-only create payload.
  it('fuzz: invalid names throw; accepted payloads stay DNS-1123 and kind-correct', () => {
    const seeds = [
      '',
      '../x',
      'has spaces',
      'a'.repeat(52),
      'ok-name',
      'ocp4-cis',
      'rule-1',
      '!!!',
      'UPPER',
      'ends-',
      '-starts',
    ];
    for (let i = 0; i < 1000; i++) {
      const name =
        i < seeds.length ? seeds[i] : i % 4 === 0 ? `tp-${i}` : randomString(i % 60);
      const extendsBase =
        i % 5 === 0 ? 'ocp4-cis' : i % 7 === 0 ? '' : randomString(i % 40);
      const rules = [
        i % 3 === 0 ? `rule-${i}` : randomString(i % 20),
        '../x',
        'has spaces',
        '',
        seeds[i % seeds.length],
      ];
      let threw = false;
      let m: Record<string, unknown> | undefined;
      try {
        m = tailoredProfileManifest(name, extendsBase, rules, rules);
      } catch (e) {
        threw = true;
        expect(e).toBeInstanceOf(Error);
        expect((e as Error).message).toMatch(/invalid (TailoredProfile|base profile) name/);
      }
      if (!threw) {
        expect(m).toBeDefined();
        expect(m!.kind).toBe('TailoredProfile');
        expect(m!.apiVersion).toBe('compliance.openshift.io/v1alpha1');
        const metaName = (m!.metadata as { name: string }).name;
        expect(isValidTailoredProfileName(metaName)).toBe(true);
        const spec = m!.spec as {
          extends: string;
          enableRules?: { name: string }[];
          disableRules?: { name: string }[];
        };
        expect(isValidK8sName(spec.extends)).toBe(true);
        for (const r of [...(spec.enableRules ?? []), ...(spec.disableRules ?? [])]) {
          expect(isValidK8sName(r.name)).toBe(true);
        }
      }
    }
  });
});

// On an AlreadyExists create, the authoring form adopts the existing CR only if
// its content matches what we would have created (a genuine retry). A collision
// with an unrelated profile must NOT match, or the user's edits are silently
// discarded and a foreign profile is bound under a false success.
describe('tailoredProfileSpecMatches', () => {
  const specOf = (extendsBase: string, disable: string[], enable: string[] = []) =>
    tailoredProfileManifest('x', extendsBase, disable, enable) as { spec: Record<string, unknown> };
  it('matches an identical spec regardless of rule order', () => {
    const existing = specOf('ocp4-cis', ['b-rule', 'a-rule']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['a-rule', 'b-rule'])).toBe(true);
  });
  it('matches when both sides default extends to ocp4-cis', () => {
    const existing = specOf('ocp4-cis', []);
    expect(tailoredProfileSpecMatches(existing, '', [])).toBe(true);
  });
  it('does not match a different base profile', () => {
    const existing = specOf('ocp4-cis', ['a-rule']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-pci-dss', ['a-rule'])).toBe(false);
  });
  it('does not match a different disable-rule set (the collision case)', () => {
    const existing = specOf('ocp4-cis', ['rule-x']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['rule-y'])).toBe(false);
  });
  it('ignores invalid rule names the manifest would have dropped', () => {
    const existing = specOf('ocp4-cis', ['good-rule']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['good-rule', 'bad name'])).toBe(true);
  });
  it('treats a rule in both enable and disable as disabled (mirrors the manifest)', () => {
    const existing = specOf('ocp4-cis', ['dup'], ['dup', 'on-only']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['dup'], ['dup', 'on-only'])).toBe(true);
  });
  it('does not match undefined / empty existing against a real spec', () => {
    expect(tailoredProfileSpecMatches(undefined, 'ocp4-cis', ['a-rule'])).toBe(false);
    expect(tailoredProfileSpecMatches({}, 'ocp4-cis', ['a-rule'])).toBe(false);
  });
});

// downloadBlob is DOM-only; mock the minimal browser surface so we can assert
// the safeDownloadName sanitization that defends the save path.
describe('downloadBlob', () => {
  type AnchorStub = {
    href: string;
    download: string;
    rel: string;
    style: { display: string };
    click: jest.Mock;
    remove: jest.Mock;
  };

  const installDom = (): {
    anchor: AnchorStub;
    createObjectURL: jest.Mock;
    revokeObjectURL: jest.Mock;
    restore: () => void;
  } => {
    const anchor: AnchorStub = {
      href: '',
      download: '',
      rel: '',
      style: { display: '' },
      click: jest.fn(),
      remove: jest.fn(),
    };
    const createObjectURL = jest.fn(() => 'blob:mock-url');
    const revokeObjectURL = jest.fn();
    const g = globalThis as Record<string, unknown>;
    const prev = {
      URL: g.URL,
      document: g.document,
      window: g.window,
      Blob: g.Blob,
    };
    g.URL = { createObjectURL, revokeObjectURL };
    g.document = {
      createElement: () => anchor,
      body: { appendChild: jest.fn() },
    };
    // Run revoke callbacks inline so the test can assert without waiting.
    g.window = { setTimeout: (fn: () => void) => { fn(); return 0; } };
    if (typeof g.Blob === 'undefined') {
      g.Blob = class {
        // Minimal Blob stand-in for node; production uses the real DOM Blob.
        constructor(public parts: unknown[]) {}
      };
    }
    return {
      anchor,
      createObjectURL,
      revokeObjectURL,
      restore: () => {
        g.URL = prev.URL;
        g.document = prev.document;
        g.window = prev.window;
        g.Blob = prev.Blob;
      },
    };
  };

  it('sanitizes path traversal and control chars in the download filename', () => {
    const dom = installDom();
    try {
      downloadBlob(new Blob(['x']), '../../../etc/passwd');
      expect(dom.anchor.download).toBe('______etc_passwd');
      expect(dom.anchor.download).not.toContain('/');
      expect(dom.anchor.download).not.toContain('..');
      // Defense in depth when a browser ignores the download attribute.
      expect(dom.anchor.rel).toBe('noopener noreferrer');
      expect(dom.createObjectURL).toHaveBeenCalledTimes(1);
      expect(dom.anchor.click).toHaveBeenCalledTimes(1);
      expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
    } finally {
      dom.restore();
    }
  });

  it('falls back to "download" for empty/dot-only names and keeps safe names', () => {
    const cases: [string, string][] = [
      ['', 'download'],
      ['.', '_'],
      ['..', '_'],
      ['ok.csv', 'ok.csv'],
      ['a/b\\c:d', 'a_b_c_d'],
      ['report.html', 'report.html'],
      // BIDI override must not spoof extension direction (e.g. exe.gpj\\u202E).
      ['safe\u202Eexe.csv', 'safe_exe.csv'],
      ['a\u200E\u2066b.csv', 'a__b.csv'],
      // Zero-width / BOM must not hide path segments or extension spoofing.
      ['a\u200Bb.csv', 'a_b.csv'],
      ['x\uFEFFy.csv', 'x_y.csv'],
    ];
    for (const [input, want] of cases) {
      const dom = installDom();
      try {
        downloadBlob(new Blob(['x']), input);
        expect(dom.anchor.download).toBe(want);
      } finally {
        dom.restore();
      }
    }
  });

  it('caps oversized filenames at 200 characters', () => {
    const dom = installDom();
    try {
      downloadBlob(new Blob(['x']), `${'a'.repeat(300)}.csv`);
      expect(dom.anchor.download.length).toBe(200);
      expect(dom.anchor.download.startsWith('aaa')).toBe(true);
    } finally {
      dom.restore();
    }
  });

  it('revokes the object URL even when click throws', () => {
    const dom = installDom();
    dom.anchor.click.mockImplementation(() => {
      throw new Error('click failed');
    });
    try {
      expect(() => downloadBlob(new Blob(['x']), 'ok.csv')).toThrow('click failed');
      expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
    } finally {
      dom.restore();
    }
  });

  // Filename is sometimes CR-derived (export name). Sanitization must never throw
  // and must strip path/control/BIDI noise that could bias the browser save path.
  it('fuzz: download filename is non-empty, capped, and free of path/control/BIDI', () => {
    const seeds = [
      '',
      '.',
      '..',
      '../../../etc/passwd',
      'a/b\\c:d',
      'safe\u202Eexe.csv',
      'a\u200E\u2066b.csv',
      'a'.repeat(300),
      'report\0.csv',
      'ok.csv',
    ];
    for (let i = 0; i < 500; i++) {
      const name =
        i < seeds.length
          ? seeds[i]
          : randomString(i % 80) + (i % 7 === 0 ? '/../' : '') + (i % 5 === 0 ? '\u202E' : '');
      const dom = installDom();
      try {
        expect(() => downloadBlob(new Blob(['x']), name)).not.toThrow();
        const d = dom.anchor.download;
        expect(typeof d).toBe('string');
        expect(d.length).toBeGreaterThan(0);
        expect(d.length).toBeLessThanOrEqual(200);
        expect(d).not.toContain('/');
        expect(d).not.toContain('\\');
        expect(d).not.toContain('..');
        expect(d).not.toMatch(/[\0-\x1f\x7f]/);
        expect(d).not.toMatch(/[\u200B-\u200D\u200E\u200F\u202A-\u202E\u2066-\u2069\uFEFF]/);
        expect(dom.anchor.rel).toBe('noopener noreferrer');
        expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
      } finally {
        dom.restore();
      }
    }
  });
});
