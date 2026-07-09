import {
  autoApplyPatch,
  checkBody,
  checkTitle,
  rescanPatch,
  resultsHref,
  scoreColor,
  toggledProfiles,
} from './utils';
import { ComplianceCheckResult } from './models';

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

describe('autoApplyPatch', () => {
  it('replaces the leaf when spec.remediation exists', () => {
    expect(autoApplyPatch(true, true)).toEqual([
      { op: 'replace', path: '/spec/remediation/apply', value: 'Automatic' },
    ]);
    expect(autoApplyPatch(true, false)).toEqual([
      { op: 'replace', path: '/spec/remediation/apply', value: 'Manual' },
    ]);
  });
  it('adds the parent object when spec.remediation is absent', () => {
    expect(autoApplyPatch(false, true)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { apply: 'Automatic' } },
    ]);
    expect(autoApplyPatch(false, false)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { apply: 'Manual' } },
    ]);
  });
  it('fuzz: always a single op carrying a valid enum value', () => {
    for (const has of [true, false]) {
      for (const checked of [true, false]) {
        const patch = autoApplyPatch(has, checked);
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

describe('resultsHref', () => {
  it('builds a filtered results path', () => {
    expect(resultsHref('FAIL')).toBe(
      '/baseline-security/results?rowFilter-result-status=FAIL',
    );
  });
  it('encodes special characters', () => {
    expect(resultsHref('NOT-APPLICABLE')).toContain('NOT-APPLICABLE');
    expect(resultsHref('a b')).toContain('a%20b');
    expect(resultsHref('x&y')).toContain(encodeURIComponent('x&y'));
  });
  it('fuzz: always under /baseline-security/results and never throws', () => {
    for (let i = 0; i < 1000; i++) {
      const href = resultsHref(randomString(i % 32));
      expect(href.startsWith('/baseline-security/results?rowFilter-result-status=')).toBe(true);
    }
  });
});
