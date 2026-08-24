import { checkResultHref, resultsHref } from './links';
import { randomString } from './testing/fuzz';

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
      expect(href.startsWith('/baseline-security/results?')).toBeTruthy();
      expect(href).toContain('rowFilter-result-status=');
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
      expect(href.startsWith(prefix)).toBeTruthy();
      // The name segment carries no unescaped path separator or whitespace.
      expect(href.slice(prefix.length)).not.toMatch(/[/\s#?]/);
    }
  });
});
