import {
  checkResultHref,
  addWaiverPatch,
  isWaived,
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
  nodeScanPool,
  remediationObjectText,
  resultsCsv,
  remediationApplyPatch,
  checkBody,
  checkTitle,
  errorMessage,
  rescanPatch,
  resultsHref,
  scoreColor,
  severityWeight,
  flatProfileScore,
  profileScore,
  toggledProfiles,
  isValidCron,
  schedulePatch,
  batchApplyPatch,
  buildReportHtml,
  tailoredProfileManifest,
  tailoredProfileBindingPatch,
  isValidK8sName,
  isValidTailoredProfileName,
  isAlreadyExists,
  changedChecks,
  dateInputEndOfDayIso,
} from './utils';
import { ClusterBaseline, ComplianceCheckResult, ComplianceRemediation, ResultCounts } from './models';

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
  it('fuzz: never throws and never returns empty', () => {
    for (let i = 0; i < 2000; i++) {
      const title = checkTitle(result('name', randomString(i % 64)));
      expect(typeof title).toBe('string');
      expect(title.length).toBeGreaterThan(0);
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
  it('fuzz: never duplicates when adding; removing the missing key is a no-op', () => {
    const keys = ['cis', 'stig', 'e8', 'bsi', 'pci-dss'];
    for (let i = 0; i < 2000; i++) {
      const n = (i % 5) + 1;
      const current = keys.slice(0, n);
      const key = keys[i % keys.length];
      const checked = i % 2 === 0;
      const next = toggledProfiles(current, key, checked);
      expect(new Set(next).size).toBe(next.length);
      if (checked) {
        expect(next).toContain(key);
      }
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
  ])('score %p -> %s', (score, status) => {
    expect(scoreColor(score as number | undefined)).toContain(`status--${status}`);
  });
  it('fuzz: always a CSS var token', () => {
    for (let i = 0; i < 500; i++) {
      const s = i === 0 ? undefined : Math.floor(fuzzRand() * 200) - 50;
      const color = scoreColor(s);
      expect(color.startsWith('var(--pf-t--')).toBe(true);
    }
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
  it('flatProfileScore floors pass/(pass+fail)', () => {
    expect(flatProfileScore(1, 1)).toBe(50);
    expect(flatProfileScore(3, 1)).toBe(75);
    expect(flatProfileScore(0, 0)).toBeNull();
  });
  it('profileScore uses flat counts by default', () => {
    expect(profileScore({ pass: 1, fail: 1 })).toBe(50);
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

  it('emits a header and one row per result', () => {
    const csv = resultsCsv([r('a', 'PASS', 'low', 'Title A'), r('b', 'FAIL', 'high', 'Title B')]);
    const lines = csv.split('\r\n');
    expect(lines[0]).toBe('name,title,status,severity,waived');
    expect(lines[1]).toBe('a,Title A,PASS,low,false');
    expect(lines[2]).toBe('b,Title B,FAIL,high,false');
  });
  it('marks waived checks so export matches score exclusions', () => {
    const csv = resultsCsv([r('b', 'FAIL', 'high', 'Fail B')], [{ name: 'b', reason: 'risk' }]);
    expect(csv.split('\r\n')[1]).toBe('b,Fail B,FAIL,high,true');
  });
  it('does not mark a waived PASS as score-excluded (self-healing)', () => {
    // Operator only excludes FAIL+waiver; a waived check that now PASSes
    // still counts toward the score, so the CSV must not claim waived=true.
    const csv = resultsCsv([r('b', 'PASS', 'high', 'Pass B')], [{ name: 'b', reason: 'stale' }]);
    expect(csv.split('\r\n')[1]).toBe('b,Pass B,PASS,high,false');
  });
  it('quotes and escapes cells containing comma, quote, or newline', () => {
    const csv = resultsCsv([r('x,y', 'FAIL', 'high', 'He said "hi"\nline2')]);
    const row = csv.split('\r\n')[1];
    expect(row).toBe('"x,y","He said ""hi""",FAIL,high,false');
  });
  it('neutralizes spreadsheet formula-looking cells from untrusted CR data', () => {
    const csv = resultsCsv([
      r('=cmd', '-1', '@import', '+SUM(1,1)'),
      r('\tTabbed', '\nNewline', 'low'),
      r('\rCarriage', 'PASS', 'low'),
      r(' =cmd', 'PASS', 'low'), // leading space then formula
      r('a\0b', 'PASS', 'low'), // NUL stripped (can truncate cells)
    ]);
    const lines = csv.split('\r\n');
    expect(lines[1]).toBe(`'=cmd,"'+SUM(1,1)",'-1,'@import,false`);
    expect(csv).toContain(`"'\tTabbed","'\tTabbed","'\nNewline",low,false`);
    expect(csv).toContain(`"'\rCarriage","'\rCarriage",PASS,low,false`);
    expect(csv).toContain(`' =cmd`);
    expect(csv).toContain('ab,ab,PASS,low,false');
    expect(csv).not.toContain('\0');
  });
  it('handles empty input (header only)', () => {
    expect(resultsCsv([])).toBe('name,title,status,severity,waived');
  });
  // Export must match the Results table: benign INCONSISTENT collapses so CSV
  // status is not a raw "INCONSISTENT" that fails filters and score math.
  it('collapses benign INCONSISTENT via effectiveStatus', () => {
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
    expect(csv.split('\r\n')[1]).toBe('inc,Benign split,PASS,medium,false');
  });
  // Operator folds SKIP into notApplicable; CSV must match Overview N/A export.
  it('folds SKIP into NOT-APPLICABLE via effectiveStatus', () => {
    const csv = resultsCsv([r('s1', 'SKIP', 'low', 'Skipped rule')]);
    expect(csv.split('\r\n')[1]).toBe('s1,Skipped rule,NOT-APPLICABLE,low,false');
  });
  it('fuzz: valid CSV (quotes balanced) for arbitrary CR text', () => {
    const rand = () =>
      Array.from({ length: Math.floor(fuzzRand() * 40) }, () =>
        String.fromCharCode(Math.floor(fuzzRand() * 128)),
      ).join('');
    for (let i = 0; i < 2000; i++) {
      const csv = resultsCsv([r(rand(), 'FAIL', 'high', rand())]);
      expect(typeof csv).toBe('string');
      // Total double-quotes are even (all escapes balanced).
      expect((csv.match(/"/g) ?? []).length % 2).toBe(0);
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
});

describe('isValidK8sName', () => {
  it('accepts valid RFC1123 subdomain names', () => {
    expect(isValidK8sName('cis-custom')).toBe(true);
    expect(isValidK8sName('a')).toBe(true);
    expect(isValidK8sName('ocp4.cis.1')).toBe(true);
  });
  it('rejects invalid names', () => {
    expect(isValidK8sName('')).toBe(false);
    expect(isValidK8sName('My Profile')).toBe(false);
    expect(isValidK8sName('UPPER')).toBe(false);
    expect(isValidK8sName('-lead')).toBe(false);
    expect(isValidK8sName('trail-')).toBe(false);
    expect(isValidK8sName('a..b')).toBe(false);
    expect(isValidK8sName('a.-b')).toBe(false);
    expect(isValidK8sName('a-.b')).toBe(false);
    expect(isValidK8sName('a'.repeat(254))).toBe(false);
  });
  // Free-form TailoredProfile / resource names typed in the console; never throw,
  // and a true result must satisfy length + RFC1123 shape (no uppercase, no ends).
  it('fuzz: never throws; true implies length and shape invariants', () => {
    for (let i = 0; i < 2000; i++) {
      const name =
        i % 5 === 0
          ? randomString(i % 80)
          : i % 5 === 1
            ? 'a'.repeat(i % 260)
            : i % 5 === 2
              ? `x${'-'.repeat(i % 10)}y`
              : i % 5 === 3
                ? ''
                : `name-${i}`;
      let ok: boolean;
      expect(() => {
        ok = isValidK8sName(name);
      }).not.toThrow();
      expect(typeof ok!).toBe('boolean');
      if (ok!) {
        expect(name.length).toBeGreaterThan(0);
        expect(name.length).toBeLessThanOrEqual(253);
        expect(name).toMatch(/^[a-z0-9]/);
        expect(name).toMatch(/[a-z0-9]$/);
        expect(name).not.toMatch(/[A-Z_\s]/);
      }
    }
  });
});

describe('isValidTailoredProfileName', () => {
  it('accepts names that fit baseline-tp-<name> label budget (51 chars)', () => {
    expect(isValidTailoredProfileName('cis-custom')).toBe(true);
    expect(isValidTailoredProfileName('a'.repeat(51))).toBe(true);
  });
  it('rejects names longer than the ClusterBaseline tailoredProfiles MaxLength', () => {
    // isValidK8sName would accept 52 alphanumerics; suite label would exceed 63.
    expect(isValidK8sName('a'.repeat(52))).toBe(true);
    expect(isValidTailoredProfileName('a'.repeat(52))).toBe(false);
  });
  it('rejects the same shape invalids as isValidK8sName', () => {
    expect(isValidTailoredProfileName('')).toBe(false);
    expect(isValidTailoredProfileName('UPPER')).toBe(false);
    expect(isValidTailoredProfileName('-x')).toBe(false);
    expect(isValidTailoredProfileName('a..b')).toBe(false);
  });
  // suite label is "baseline-tp-<name>" (63 char k8s label budget => name <= 51).
  it('fuzz: never throws; true implies k8s name and len <= 51', () => {
    for (let i = 0; i < 2000; i++) {
      const name =
        i % 4 === 0
          ? randomString(i % 60)
          : i % 4 === 1
            ? 'a'.repeat(i % 60)
            : i % 4 === 2
              ? `tp-${i}`
              : '';
      let ok: boolean;
      expect(() => {
        ok = isValidTailoredProfileName(name);
      }).not.toThrow();
      if (ok!) {
        expect(name.length).toBeLessThanOrEqual(51);
        expect(isValidK8sName(name)).toBe(true);
      }
    }
  });
});

describe('isAlreadyExists', () => {
  it('detects a 409 / AlreadyExists apiserver rejection', () => {
    expect(isAlreadyExists({ code: 409 })).toBe(true);
    expect(isAlreadyExists({ reason: 'AlreadyExists' })).toBe(true);
    expect(isAlreadyExists({ message: 'tailoredprofiles "x" already exists' })).toBe(true);
  });
  it('is false for other errors', () => {
    expect(isAlreadyExists({ code: 403 })).toBe(false);
    expect(isAlreadyExists(new Error('boom'))).toBe(false);
    expect(isAlreadyExists(null)).toBe(false);
  });
});

describe('effectiveStatus', () => {
  const inc = (ann: Record<string, string>) =>
    ({ status: 'INCONSISTENT', metadata: { annotations: ann } }) as unknown as ComplianceCheckResult;

  it('passes through a non-inconsistent status unchanged', () => {
    expect(effectiveStatus({ status: 'FAIL', metadata: {} })).toBe('FAIL');
    expect(effectiveStatus({ status: 'PASS', metadata: {} })).toBe('PASS');
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
    expect(inc({}).status).toBe('INCONSISTENT');
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
  const rem = (kind?: string, obj?: Record<string, unknown>): ComplianceRemediation =>
    ({
      metadata: { name: 'r', namespace: 'openshift-compliance' },
      spec: { apply: false, current: obj ? { object: obj } : kind ? { object: { kind } } : undefined },
    }) as ComplianceRemediation;

  it('isNodeRemediation detects MachineConfig', () => {
    expect(isNodeRemediation(rem('MachineConfig'))).toBe(true);
    expect(isNodeRemediation(rem('APIServer'))).toBe(false);
    expect(isNodeRemediation(rem())).toBe(false);
  });
  it('remediationObjectText pretty-prints the object, empty when absent', () => {
    expect(remediationObjectText(rem('MachineConfig', { kind: 'MachineConfig', x: 1 }))).toContain(
      '"kind": "MachineConfig"',
    );
    expect(remediationObjectText(rem())).toBe('');
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
    }
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
    expect(
      resultFilterStatus(
        { metadata: { name: 'f1' }, status: 'FAIL' },
        [{ name: 'f1', expiresAt: '2020-01-01T00:00:00Z' }],
      ),
    ).toBe('FAIL');
    // Far-future expiry stays WAIVED under wall-clock.
    expect(
      resultFilterStatus(
        { metadata: { name: 'f1' }, status: 'FAIL' },
        [{ name: 'f1', expiresAt: '2099-01-01T00:00:00Z' }],
      ),
    ).toBe('WAIVED');
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
  it('waiverExpired / isWaived respect expiry', () => {
    const now = new Date('2026-07-11T00:00:00Z');
    const past = { name: 'a', expiresAt: '2026-07-10T00:00:00Z' };
    const future = { name: 'b', expiresAt: '2026-07-12T00:00:00Z' };
    const none = { name: 'c' };
    const bad = { name: 'd', expiresAt: 'not-a-date' };
    expect(waiverExpired(past, now)).toBe(true);
    expect(waiverExpired(future, now)).toBe(false);
    expect(waiverExpired(none, now)).toBe(false);
    // Corrupt expiresAt must not grant a permanent waiver.
    expect(waiverExpired(bad, now)).toBe(true);
    // isWaived (excluded from score) is false for an expired waiver.
    expect(isWaived('a', [past], now)).toBe(false);
    expect(isWaived('b', [future], now)).toBe(true);
    expect(isWaived('c', [none], now)).toBe(true);
    expect(isWaived('d', [bad], now)).toBe(false);
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
    const out = expiringWaivers([soon, later, gone, perm], 7 * day, now);
    expect(out.map((w) => w.name)).toEqual(['soon']);
  });
  it('removeWaiverPatch test-guards the name before removing', () => {
    expect(removeWaiverPatch(2, 'chk')).toEqual([
      { op: 'test', path: '/spec/waivers/2/name', value: 'chk' },
      { op: 'remove', path: '/spec/waivers/2' },
    ]);
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

  it('schedulePatch replaces when present, adds when absent', () => {
    expect(schedulePatch(true, '0 2 * * *')).toEqual([
      { op: 'replace', path: '/spec/schedule', value: '0 2 * * *' },
    ]);
    expect(schedulePatch(false, '0 2 * * *')).toEqual([
      { op: 'add', path: '/spec/schedule', value: '0 2 * * *' },
    ]);
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
});

describe('batchApplyPatch', () => {
  it('adds the annotation, creating the map when absent', () => {
    expect(batchApplyPatch(true, ['a', 'b'])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.io~1batch-apply', value: 'a,b' },
    ]);
    expect(batchApplyPatch(false, ['a'])).toEqual([
      { op: 'add', path: '/metadata/annotations', value: { 'baselinesecurity.io/batch-apply': 'a' } },
    ]);
  });
  it('is a no-op for empty, blank, or invalid names', () => {
    expect(batchApplyPatch(true, [])).toEqual([]);
    expect(batchApplyPatch(true, ['', '  ', ','])).toEqual([]);
    expect(batchApplyPatch(false, ['Bad_Name', 'UPPER'])).toEqual([]);
  });
  it('dedupes, trims, and caps at the operator batch limit', () => {
    expect(batchApplyPatch(true, [' a ', 'b', 'a', 'b '])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.io~1batch-apply', value: 'a,b' },
    ]);
    const many = Array.from({ length: 300 }, (_, i) => `rem-${i}`);
    const patch = batchApplyPatch(true, many);
    expect(patch).toHaveLength(1);
    const value = (patch[0] as { value: string }).value;
    expect(value.split(',')).toHaveLength(256);
    expect(value.startsWith('rem-0,')).toBe(true);
    expect(value.endsWith(',rem-255')).toBe(true);
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
});
