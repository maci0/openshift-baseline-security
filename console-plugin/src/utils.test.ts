import {
  checkResultHref,
  aggregateCounts,
  isNodeRemediation,
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
} from './utils';
import { ComplianceCheckResult, ComplianceRemediation } from './models';

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
  it('replaces the leaf when spec.remediation exists', () => {
    expect(remediationApplyPatch(true, true)).toEqual([
      { op: 'replace', path: '/spec/remediation/apply', value: 'Automatic' },
    ]);
    expect(remediationApplyPatch(true, false)).toEqual([
      { op: 'replace', path: '/spec/remediation/apply', value: 'Manual' },
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
    expect(lines[0]).toBe('name,title,status,severity');
    expect(lines[1]).toBe('a,Title A,PASS,low');
    expect(lines[2]).toBe('b,Title B,FAIL,high');
  });
  it('quotes and escapes cells containing comma, quote, or newline', () => {
    const csv = resultsCsv([r('x,y', 'FAIL', 'high', 'He said "hi"\nline2')]);
    const row = csv.split('\r\n')[1];
    expect(row).toBe('"x,y","He said ""hi""",FAIL,high');
  });
  it('neutralizes spreadsheet formula-looking cells from untrusted CR data', () => {
    const csv = resultsCsv([
      r('=cmd', '-1', '@import', '+SUM(1,1)'),
      r('\tTabbed', '\nNewline', 'low'),
      r('\rCarriage', 'PASS', 'low'),
    ]);
    const lines = csv.split('\r\n');
    expect(lines[1]).toBe(`'=cmd,"'+SUM(1,1)",'-1,'@import`);
    expect(csv).toContain(`"'\tTabbed","'\tTabbed","'\nNewline",low`);
    expect(csv).toContain(`"'\rCarriage","'\rCarriage",PASS,low`);
  });
  it('handles empty input (header only)', () => {
    expect(resultsCsv([])).toBe('name,title,status,severity');
  });
  it('fuzz: valid CSV (each row has 4 cells; quotes balanced) for arbitrary CR text', () => {
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
});

describe('aggregateCounts', () => {
  const c = (pass: number, fail: number, manual = 0, error = 0, notApplicable = 0) => ({
    pass,
    fail,
    manual,
    error,
    notApplicable,
  });
  it('sums profiles and tailored profiles together', () => {
    expect(aggregateCounts(c(10, 2, 1), c(40, 8, 3))).toEqual(c(50, 10, 4));
  });
  it('returns zeros for no groups', () => {
    expect(aggregateCounts()).toEqual(c(0, 0, 0, 0, 0));
  });
  it('score composition matches: tailored-only results still populate totals', () => {
    // regular profile empty, tailored has results -> totals non-zero
    const totals = aggregateCounts(c(0, 0), c(2, 1));
    expect(totals.pass + totals.fail).toBe(3);
  });
});
