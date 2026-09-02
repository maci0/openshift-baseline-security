import { ComplianceCheckResult } from './models';
import { isString } from './parse';
import {
  effectiveStatus,
  inconsistentSources,
  RESULT_FILTER_STATUSES,
  statusDisplayTitle,
} from './status';
import { randomString } from './testing/fuzz';

describe('inconsistentSources', () => {
  const withAnn = (ann?: Record<string, string>): ComplianceCheckResult => ({
    metadata: { name: 'r', namespace: 'ns', annotations: ann },
    status: 'INCONSISTENT',
  });

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
      const { sources, mostCommon } = inconsistentSources(
        withAnn({ 'compliance.openshift.io/inconsistent-source': randomString(i % 40) }),
      );
      expect(Array.isArray(sources)).toBeTruthy();
      // Whatever parses must keep the row shape the Results tooltip renders.
      for (const s of sources) {
        expect(isString(s.node)).toBeTruthy();
        expect(isString(s.status)).toBeTruthy();
      }
      expect(mostCommon === null || isString(mostCommon)).toBeTruthy();
    }
  });
});

describe('effectiveStatus', () => {
  // SAFETY: Overview renders CRs whose metadata can lack name/namespace; the
  // collapse must work from annotations alone.
  const inc = (ann: Record<string, string>): ComplianceCheckResult =>
    ({ status: 'INCONSISTENT', metadata: { annotations: ann } }) as ComplianceCheckResult;

  it('passes through a non-inconsistent status unchanged', () => {
    expect(effectiveStatus({ status: 'FAIL', metadata: {} })).toBe('FAIL');
    expect(effectiveStatus({ status: 'PASS', metadata: {} })).toBe('PASS');
  });
  // Operator tally maps unknown/empty status to ERROR; UI/CSV must match so a
  // missing field is not a blank filter chip or a thrown CSV export.
  it('maps empty or non-string status to ERROR (operator tally parity)', () => {
    expect(effectiveStatus({ status: '', metadata: {} })).toBe('ERROR');
    // The typed client promises `string`, but the apiserver stores JSON: a
    // tampered CR ships null, numbers, or no field at all. Every junk shape
    // must fold to ERROR exactly like the operator tally, never throw.
    // SAFETY: deliberately corrupted CR payload; the collapse coerces junk.
    const cr = (body: string) =>
      JSON.parse(body) as {
        status: string;
        metadata?: { annotations?: Record<string, string> };
      };
    expect(effectiveStatus(cr('{"metadata": {}}'))).toBe('ERROR');
    expect(effectiveStatus(cr('{"status": null, "metadata": {}}'))).toBe('ERROR');
    expect(effectiveStatus(cr('{"status": 42, "metadata": {}}'))).toBe('ERROR');
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
    let wrong: string | undefined;
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
        const expected = rawStatus === 'SKIP' ? 'NOT-APPLICABLE' : rawStatus;
        if (got! !== expected) {
          wrong ??= `iteration ${i}: ${rawStatus} rendered ${JSON.stringify(got!)}`;
        }
      } else if (!['PASS', 'NOT-APPLICABLE', 'INCONSISTENT'].includes(got!)) {
        wrong ??= `iteration ${i}: INCONSISTENT collapsed to ${JSON.stringify(got!)}`;
      } else {
        const src = r.metadata.annotations['compliance.openshift.io/inconsistent-source'];
        const mc = r.metadata.annotations['compliance.openshift.io/most-common-status'];
        // Fail-closed: FAIL/ERROR as a per-node or most-common status must not collapse.
        const conflict =
          /:(?:\s*)(FAIL|ERROR)(?:\s*(?:,|$))/i.test(src) || /^(FAIL|ERROR)$/i.test(mc.trim());
        if (conflict && got! !== 'INCONSISTENT') {
          wrong ??= `iteration ${i}: FAIL/ERROR annotation collapsed to ${JSON.stringify(got!)}`;
        }
      }
    }
    expect(wrong).toBeUndefined();
  });
});

describe('RESULT_FILTER_STATUSES / statusDisplayTitle', () => {
  it('is the Results chip set: WAIVED in, SKIP out', () => {
    expect([...RESULT_FILTER_STATUSES]).toEqual([
      'PASS',
      'FAIL',
      'WAIVED',
      'MANUAL',
      'ERROR',
      'INFO',
      'INCONSISTENT',
      'NOT-APPLICABLE',
    ]);
    expect(RESULT_FILTER_STATUSES).not.toContain('SKIP');
    expect(new Set(RESULT_FILTER_STATUSES).size).toBe(RESULT_FILTER_STATUSES.length);
  });

  it('effectiveStatus never returns SKIP or WAIVED', () => {
    expect(effectiveStatus({ status: 'SKIP' })).toBe('NOT-APPLICABLE');
    expect(effectiveStatus({ status: 'WAIVED' })).toBe('ERROR');
    expect(effectiveStatus({ status: 'PASS' })).toBe('PASS');
  });

  it('statusDisplayTitle localizes known tokens; SKIP shares N/A', () => {
    const t = (k: string): string => k;
    expect(statusDisplayTitle('PASS', t)).toBe('Pass');
    expect(statusDisplayTitle('WAIVED', t)).toBe('Waived');
    expect(statusDisplayTitle('SKIP', t)).toBe('Not applicable');
    expect(statusDisplayTitle('NOT-APPLICABLE', t)).toBe('Not applicable');
    expect(statusDisplayTitle('FUTURE-STATE', t)).toBe('FUTURE-STATE');
  });
});
