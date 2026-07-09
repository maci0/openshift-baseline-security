import { autoApplyPatch, checkBody, checkTitle, scoreColor, toggledProfiles } from './utils';
import { ComplianceCheckResult } from './models';

const result = (name: string, description?: string): ComplianceCheckResult =>
  ({ metadata: { name, namespace: 'ns' }, description }) as ComplianceCheckResult;

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
  it('never throws and never returns empty on arbitrary input', () => {
    // description is CR data: exercise it with random garbage.
    for (let i = 0; i < 2000; i++) {
      const len = Math.floor(Math.random() * 64);
      const junk = Array.from({ length: len }, () =>
        String.fromCharCode(Math.floor(Math.random() * 0xffff)),
      ).join('');
      const title = checkTitle(result('name', junk));
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
  it('never throws on arbitrary input', () => {
    for (let i = 0; i < 2000; i++) {
      const junk = Array.from({ length: i % 64 }, () =>
        String.fromCharCode(Math.floor(Math.random() * 0xffff)),
      ).join('');
      expect(typeof checkBody(result('n', junk))).toBe('string');
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
});

describe('autoApplyPatch', () => {
  it('replaces the leaf when spec.remediation exists', () => {
    expect(autoApplyPatch(true, true)).toEqual([
      { op: 'replace', path: '/spec/remediation/autoApply', value: true },
    ]);
  });
  it('adds the parent object when spec.remediation is absent', () => {
    expect(autoApplyPatch(false, true)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { autoApply: true } },
    ]);
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
});
