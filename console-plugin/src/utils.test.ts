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
  toggledProfiles,
  isValidCron,
  schedulePatch,
  batchApplyPatch,
  buildReportHtml,
  tailoredProfileManifest,
  isValidK8sName,
  isValidTailoredProfileName,
  isAlreadyExists,
} from './utils';
import { ClusterBaseline, ComplianceCheckResult, ComplianceRemediation, ResultCounts } from './models';

const result = (name: string, description?: string): ComplianceCheckResult =>
  ({ metadata: { name, namespace: 'ns' }, description }) as ComplianceCheckResult;

const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(Math.random() * 0xffff))).join(
    '',
  );

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
  it('rejects removing the last profile', () => {
    expect(toggledProfiles(['cis'], 'cis', false)).toBeNull();
  });
  it('fuzz: never empty array, never duplicates when adding', () => {
    const keys = ['cis', 'stig', 'e8', 'bsi', 'pci-dss'];
    for (let i = 0; i < 2000; i++) {
      const n = (i % 5) + 1;
      const current = keys.slice(0, n);
      const key = keys[i % keys.length];
      const checked = i % 2 === 0;
      const next = toggledProfiles(current, key, checked);
      if (next === null) {
        expect(checked).toBe(false);
        expect(current).toEqual([key]);
      } else {
        expect(next.length).toBeGreaterThan(0);
        expect(new Set(next).size).toBe(next.length);
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
  it('fuzz: always one op carrying the token', () => {
    for (let i = 0; i < 100; i++) {
      const token = String(i);
      const p = rescanPatch(i % 2 === 0, token);
      expect(p).toHaveLength(1);
      if (i % 2 === 0) {
        expect(p[0].value).toBe(token);
      } else {
        expect((p[0].value as { 'compliance.openshift.io/rescan': string })[
          'compliance.openshift.io/rescan'
        ]).toBe(token);
      }
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
      const s = i === 0 ? undefined : Math.floor(Math.random() * 200) - 50;
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
    ]);
    const lines = csv.split('\r\n');
    expect(lines[1]).toBe(`'=cmd,"'+SUM(1,1)",'-1,'@import,false`);
    expect(csv).toContain(`"'\tTabbed","'\tTabbed","'\nNewline",low,false`);
    expect(csv).toContain(`"'\rCarriage","'\rCarriage",PASS,low,false`);
    expect(csv).toContain(`' =cmd`);
  });
  it('handles empty input (header only)', () => {
    expect(resultsCsv([])).toBe('name,title,status,severity,waived');
  });
  it('fuzz: valid CSV (quotes balanced) for arbitrary CR text', () => {
    const rand = () =>
      Array.from({ length: Math.floor(Math.random() * 40) }, () =>
        String.fromCharCode(Math.floor(Math.random() * 128)),
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
    const { sources } = inconsistentSources(
      withAnn({ 'compliance.openshift.io/inconsistent-source': ' node0 , , n1:PASS ' }),
    );
    expect(sources).toEqual([
      { node: 'node0', status: '' },
      { node: 'n1', status: 'PASS' },
    ]);
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
    expect(isValidK8sName('a'.repeat(254))).toBe(false);
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
  it('keeps unknown/empty states as INCONSISTENT', () => {
    expect(inc({}).status).toBe('INCONSISTENT');
    expect(effectiveStatus(inc({}))).toBe('INCONSISTENT');
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
      }),
    ).toEqual([
      {
        op: 'add',
        path: '/spec/waivers',
        value: [
          { name: 'chk', reason: 'risk', requestedBy: 'alice', expiresAt: '2027-01-01T00:00:00Z' },
        ],
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
  it('addWaiverPatch is a no-op for empty names', () => {
    expect(addWaiverPatch(undefined, { name: '', reason: 'x' })).toEqual([]);
    expect(addWaiverPatch([], { name: '' })).toEqual([]);
  });
  it('waiverExpired / isWaived respect expiry', () => {
    const now = new Date('2026-07-11T00:00:00Z');
    const past = { name: 'a', expiresAt: '2026-07-10T00:00:00Z' };
    const future = { name: 'b', expiresAt: '2026-07-12T00:00:00Z' };
    const none = { name: 'c' };
    expect(waiverExpired(past, now)).toBe(true);
    expect(waiverExpired(future, now)).toBe(false);
    expect(waiverExpired(none, now)).toBe(false);
    // isWaived (excluded from score) is false for an expired waiver.
    expect(isWaived('a', [past], now)).toBe(false);
    expect(isWaived('b', [future], now)).toBe(true);
    expect(isWaived('c', [none], now)).toBe(true);
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
  it('fuzz: addWaiverPatch carries the name when non-empty', () => {
    for (let i = 0; i < 500; i++) {
      const name = randomString(i % 30) || 'n';
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
    expect(isValidCron('0 1 * *')).toBe(false); // 4 fields
    expect(isValidCron('0 1 * * * *')).toBe(false); // 6 fields
    expect(isValidCron('not a cron')).toBe(false);
    expect(isValidCron('')).toBe(false);
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

describe('batchApplyPatch', () => {
  it('adds the annotation, creating the map when absent', () => {
    expect(batchApplyPatch(true, ['a', 'b'])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.io~1batch-apply', value: 'a,b' },
    ]);
    expect(batchApplyPatch(false, ['a'])).toEqual([
      { op: 'add', path: '/metadata/annotations', value: { 'baselinesecurity.io/batch-apply': 'a' } },
    ]);
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
  const html = buildReportHtml(cb, now);
  it('includes score and per-profile counts', () => {
    expect(html).toContain('94 / 100');
    expect(html).toContain('CIS');
    expect(html).toContain('212');
  });
  it('escapes untrusted waiver text (no raw script tag)', () => {
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>x</script>');
  });
  it('lists only active (non-expired) waivers', () => {
    expect(html).toContain('chk');
    expect(html).not.toContain('>old<');
    expect(html).toContain('Active waivers (1)');
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
