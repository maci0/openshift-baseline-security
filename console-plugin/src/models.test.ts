import {
  checkProfileLabel,
  filterOwnedByBaseline,
  isOwnedByBaseline,
  nodePoolFromScanName,
  ownedSuiteLabels,
  ownedSuiteSelector,
  suiteFilterKey,
  suiteFilterKeyTitle,
  suiteProfileKey,
  suiteTailoredName,
} from './models';

describe('isOwnedByBaseline', () => {
  it('matches suite label to selected profiles', () => {
    expect(
      isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-cis' }, ['cis']),
    ).toBe(true);
    expect(
      isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-cis' }, ['stig']),
    ).toBe(false);
    expect(isOwnedByBaseline({ 'compliance.openshift.io/suite': 'other' }, ['cis'])).toBe(false);
    expect(isOwnedByBaseline(undefined, ['cis'])).toBe(false);
    expect(isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-cis' }, [])).toBe(false);
    expect(isOwnedByBaseline({}, ['cis'])).toBe(false);
    expect(
      isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-pci-dss' }, [
        'cis',
        'pci-dss',
      ]),
    ).toBe(true);
  });

  // Results/Remediations hot path passes Sets so membership is O(1); Set.has
  // must match array includes for the same members.
  it('accepts Set membership for profiles and tailored (hot-path form)', () => {
    const labels = { 'compliance.openshift.io/suite': 'baseline-cis' };
    expect(isOwnedByBaseline(labels, new Set(['cis', 'stig']))).toBe(true);
    expect(isOwnedByBaseline(labels, new Set(['stig']))).toBe(false);
    const tp = { 'compliance.openshift.io/suite': 'baseline-tp-custom' };
    expect(isOwnedByBaseline(tp, new Set(['cis']), new Set(['custom']))).toBe(true);
    expect(isOwnedByBaseline(tp, new Set(['cis']), new Set(['other']))).toBe(false);
  });

  it('fuzz: only true when suite is baseline-<selected profile>', () => {
    const profiles = ['cis', 'stig', 'e8', 'bsi'];
    for (let i = 0; i < 2000; i++) {
      const p = profiles[i % profiles.length];
      const suite =
        i % 5 === 0
          ? `baseline-${p}`
          : i % 5 === 1
            ? `baseline-${p}-extra`
            : i % 5 === 2
              ? p
              : i % 5 === 3
                ? undefined
                : `other-${p}`;
      const labels = suite === undefined ? undefined : { 'compliance.openshift.io/suite': suite };
      const selected = i % 3 === 0 ? profiles : [p];
      const got = isOwnedByBaseline(labels, selected);
      const want = !!suite && selected.some((s) => suite === `baseline-${s}`);
      expect(got).toBe(want);
    }
  });
});

describe('filterOwnedByBaseline', () => {
  const item = (suite?: string) => ({
    metadata: {
      name: suite ?? 'none',
      labels: suite ? { 'compliance.openshift.io/suite': suite } : undefined,
    },
  });

  it('keeps only built-in and tailored suites owned by this baseline', () => {
    const list = [
      item('baseline-cis'),
      item('baseline-stig'),
      item('baseline-tp-custom'),
      item('other'),
      item(undefined),
    ];
    const got = filterOwnedByBaseline(list, ['cis'], ['custom']);
    expect(got.map((r) => r.metadata.name)).toEqual(['baseline-cis', 'baseline-tp-custom']);
  });

  it('returns empty for undefined/empty input and drops foreign suites', () => {
    expect(filterOwnedByBaseline(undefined, ['cis'], [])).toEqual([]);
    expect(filterOwnedByBaseline([], ['cis'], [])).toEqual([]);
    expect(filterOwnedByBaseline([item('baseline-stig')], ['cis'], [])).toEqual([]);
  });
});

describe('tailored suite ownership', () => {
  const lbl = (suite: string) => ({ 'compliance.openshift.io/suite': suite });
  it('suiteTailoredName extracts the tailored name', () => {
    expect(suiteTailoredName(lbl('baseline-tp-custom'))).toBe('custom');
    expect(suiteTailoredName(lbl('baseline-cis'))).toBeUndefined();
    expect(suiteTailoredName(undefined)).toBeUndefined();
    // empty name after prefix is rejected (matches operator tailoredNameFromSuite)
    expect(suiteTailoredName(lbl('baseline-tp-'))).toBeUndefined();
  });
  it('suiteProfileKey ignores tailored suites', () => {
    expect(suiteProfileKey(lbl('baseline-cis'))).toBe('cis');
    expect(suiteProfileKey(lbl('baseline-tp-custom'))).toBeUndefined();
    expect(suiteProfileKey(lbl('baseline-'))).toBeUndefined();
    expect(suiteProfileKey(undefined)).toBeUndefined();
  });
  it('suiteFilterKey maps built-in and tailored suites for Results filters', () => {
    expect(suiteFilterKey(lbl('baseline-cis'))).toBe('cis');
    expect(suiteFilterKey(lbl('baseline-tp-custom'))).toBe('tp-custom');
    expect(suiteFilterKey(lbl('baseline-tp-'))).toBeUndefined();
    expect(suiteFilterKey(lbl('other'))).toBeUndefined();
    expect(suiteFilterKey(undefined)).toBeUndefined();
  });

  it('ownedSuiteLabels builds baseline-* and baseline-tp-* values for watches', () => {
    expect(ownedSuiteLabels(['cis', 'stig'], ['custom'])).toEqual([
      'baseline-cis',
      'baseline-stig',
      'baseline-tp-custom',
    ]);
    expect(ownedSuiteLabels(undefined, undefined)).toEqual([]);
    expect(ownedSuiteLabels([''], [''])).toEqual([]);
  });

  it('nodePoolFromScanName uses the last -node- segment', () => {
    expect(nodePoolFromScanName('ocp4-cis-node-worker')).toBe('worker');
    expect(nodePoolFromScanName('custom-node-profile-node-master')).toBe('master');
    expect(nodePoolFromScanName('ocp4-cis')).toBeNull();
    expect(nodePoolFromScanName('ocp4-cis-node-')).toBeNull();
    expect(nodePoolFromScanName('')).toBeNull();
  });

  it('ownedSuiteSelector wraps labels for CO list watches (or undefined when empty)', () => {
    expect(ownedSuiteSelector(['cis'], ['custom'])).toEqual({
      matchExpressions: [
        {
          key: 'compliance.openshift.io/suite',
          operator: 'In',
          values: ['baseline-cis', 'baseline-tp-custom'],
        },
      ],
    });
    expect(ownedSuiteSelector(undefined, undefined)).toBeUndefined();
    expect(ownedSuiteSelector([''], [''])).toBeUndefined();
  });

  // Suite labels come from untrusted cluster objects. Parsers must never throw,
  // reject empty remainders, and keep tailored vs built-in mutually exclusive.
  it('fuzz: suite parsers never throw; empty remainder rejected; tailored exclusive', () => {
    // Deterministic PRNG so CI failures are reproducible.
    let seed = 0xcafebabe;
    const fuzzRand = (): number => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      return seed / 0x100000000;
    };
    const rand = (n: number) =>
      Array.from({ length: n }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join(
        '',
      );
    for (let i = 0; i < 2000; i++) {
      const suite =
        i % 6 === 0
          ? `baseline-${rand(i % 20)}`
          : i % 6 === 1
            ? `baseline-tp-${rand(i % 20)}`
            : i % 6 === 2
              ? 'baseline-'
              : i % 6 === 3
                ? 'baseline-tp-'
                : i % 6 === 4
                  ? rand(i % 40)
                  : undefined;
      const labels = suite === undefined ? undefined : lbl(suite);
      let key: string | undefined;
      let tailored: string | undefined;
      let filter: string | undefined;
      expect(() => {
        key = suiteProfileKey(labels);
        tailored = suiteTailoredName(labels);
        filter = suiteFilterKey(labels);
      }).not.toThrow();
      // Empty remainder after prefix must be rejected.
      if (suite === 'baseline-' || suite === 'baseline-tp-') {
        expect(key).toBeUndefined();
        expect(tailored).toBeUndefined();
        expect(filter).toBeUndefined();
        continue;
      }
      // Built-in and tailored are exclusive.
      if (key !== undefined && tailored !== undefined) {
        throw new Error(`both key=${key} and tailored=${tailored} for ${suite}`);
      }
      if (tailored !== undefined) {
        expect(filter).toBe(`tp-${tailored}`);
        expect(suite?.startsWith('baseline-tp-')).toBe(true);
      } else if (key !== undefined) {
        expect(filter).toBe(key);
        expect(suite).toBe(`baseline-${key}`);
        expect(suite?.startsWith('baseline-tp-')).toBe(false);
      } else {
        expect(filter).toBeUndefined();
      }
    }
  });
  it('checkProfileLabel uses display titles for built-ins, keeps tailored names, dashes unknown', () => {
    expect(checkProfileLabel(lbl('baseline-cis'))).toBe('CIS');
    expect(checkProfileLabel(lbl('baseline-pci-dss'))).toBe('PCI-DSS');
    expect(checkProfileLabel(lbl('baseline-nist-moderate'))).toBe('NIST 800-53 Moderate');
    expect(checkProfileLabel(lbl('baseline-tp-cis-custom'))).toBe('cis-custom');
    expect(checkProfileLabel(lbl('other'))).toBe('—');
    expect(checkProfileLabel(undefined)).toBe('—');
  });
  it('suiteFilterKeyTitle matches checkProfileLabel for known filter keys', () => {
    expect(suiteFilterKeyTitle('cis')).toBe('CIS');
    expect(suiteFilterKeyTitle('tp-cis-custom')).toBe('cis-custom');
    expect(suiteFilterKeyTitle(suiteFilterKey(lbl('baseline-stig'))!)).toBe(
      checkProfileLabel(lbl('baseline-stig')),
    );
  });
  it('isOwnedByBaseline recognizes bound tailored profiles', () => {
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['cis'], ['custom'])).toBe(true);
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['cis'], [])).toBe(false);
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['cis'], undefined)).toBe(false);
    // built-in still works, and a tailored suite is not matched as a profile
    expect(isOwnedByBaseline(lbl('baseline-cis'), ['cis'], ['custom'])).toBe(true);
    // tailored suite must not match via profiles even if profiles contains "tp-custom"
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['tp-custom'], undefined)).toBe(false);
    // empty tailored suite label is not owned
    expect(isOwnedByBaseline(lbl('baseline-tp-'), ['cis'], [''])).toBe(false);
  });
});
