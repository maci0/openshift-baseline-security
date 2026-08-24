import { ComplianceCheckResult } from './models';
import { effectiveStatus, inconsistentSources } from './status';

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

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
      const { sources, mostCommon } = inconsistentSources(
        withAnn({ 'compliance.openshift.io/inconsistent-source': randomString(i % 40) }),
      );
      expect(Array.isArray(sources)).toBeTruthy();
      // Whatever parses must keep the row shape the Results tooltip renders.
      for (const s of sources) {
        expect(typeof s.node).toBe('string');
        expect(typeof s.status).toBe('string');
      }
      expect(mostCommon === null || typeof mostCommon === 'string').toBeTruthy();
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
