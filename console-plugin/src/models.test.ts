import {
  checkProfileLabel,
  ClusterBaseline,
  filterOwnedByBaseline,
  isOwnedByBaseline,
  isProfileKey,
  nodePoolFromScanName,
  ownedSuiteLabels,
  ownedSuiteSelector,
  scanningDisabled,
  PROFILE_INFO,
  PROFILE_KEYS,
  PROFILE_MAX_ITEMS,
  suiteFilterKey,
  suiteFilterKeyTitle,
  suiteProfileKey,
  suiteTailoredName,
  profileTitle,
} from './models';

// models.ts requires PROFILE_KEYS / ProfileKey / PROFILE_INFO / PROFILE_MAX_ITEMS
// to stay in lockstep with the operator CRD enum and Profiles MaxItems=8. The
// Record<> typing catches missing INFO entries, but not a key added to the union
// while PROFILE_KEYS (or the CRD) lags; this pins the runtime contract.
describe('profile key lockstep', () => {
  it('PROFILE_KEYS has no duplicates and matches Profiles MaxItems', () => {
    expect(PROFILE_KEYS.length).toBe(PROFILE_MAX_ITEMS);
    expect(new Set(PROFILE_KEYS).size).toBe(PROFILE_KEYS.length);
  });
  it('every key is a known enum value with display metadata', () => {
    for (const key of PROFILE_KEYS) {
      expect(isProfileKey(key)).toBeTruthy();
      const info = PROFILE_INFO[key];
      expect(info.title.length).toBeGreaterThan(0);
      expect(info.description.length).toBeGreaterThan(0);
    }
  });
});

describe('isOwnedByBaseline', () => {
  it('matches suite label to selected profiles', () => {
    expect(
      isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-cis' }, ['cis']),
    ).toBeTruthy();
    expect(
      isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-cis' }, ['stig']),
    ).toBeFalsy();
    expect(isOwnedByBaseline({ 'compliance.openshift.io/suite': 'other' }, ['cis'])).toBeFalsy();
    expect(isOwnedByBaseline(undefined, ['cis'])).toBeFalsy();
    expect(isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-cis' }, [])).toBeFalsy();
    expect(isOwnedByBaseline({}, ['cis'])).toBeFalsy();
    expect(
      isOwnedByBaseline({ 'compliance.openshift.io/suite': 'baseline-pci-dss' }, [
        'cis',
        'pci-dss',
      ]),
    ).toBeTruthy();
  });

  // Results/Remediations hot path passes Sets so membership is O(1); Set.has
  // must match array includes for the same members.
  it('accepts Set membership for profiles and tailored (hot-path form)', () => {
    const labels = { 'compliance.openshift.io/suite': 'baseline-cis' };
    expect(isOwnedByBaseline(labels, new Set(['cis', 'stig']))).toBeTruthy();
    expect(isOwnedByBaseline(labels, new Set(['stig']))).toBeFalsy();
    const tp = { 'compliance.openshift.io/suite': 'baseline-tp-custom' };
    expect(isOwnedByBaseline(tp, new Set(['cis']), new Set(['custom']))).toBeTruthy();
    expect(isOwnedByBaseline(tp, new Set(['cis']), new Set(['other']))).toBeFalsy();
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
      let wrong: string | undefined;
      // Empty remainder after prefix must be rejected.
      if (suite === 'baseline-' || suite === 'baseline-tp-') {
        if (key !== undefined || tailored !== undefined || filter !== undefined) {
          wrong = `empty remainder accepted for ${suite}: key=${key} tailored=${tailored} filter=${filter}`;
        }
      } else if (key !== undefined && tailored !== undefined) {
        // Built-in and tailored are exclusive.
        wrong = `both key=${key} and tailored=${tailored} for ${suite}`;
      } else if (tailored !== undefined) {
        if (filter !== `tp-${tailored}`) {
          wrong = `filter=${filter}, want tp-${tailored}`;
        } else if (!suite?.startsWith('baseline-tp-')) {
          wrong = `tailored=${tailored} without baseline-tp- prefix (suite=${suite})`;
        }
      } else if (key !== undefined) {
        if (filter !== key) {
          wrong = `filter=${filter}, want ${key}`;
        } else if (suite !== `baseline-${key}`) {
          wrong = `key=${key} from non-baseline suite ${suite}`;
        } else if (suite?.startsWith('baseline-tp-')) {
          wrong = `built-in key=${key} parsed from tailored suite ${suite}`;
        }
      } else if (filter !== undefined) {
        wrong = `filter=${filter} without a parsed key or tailored name`;
      }
      expect(wrong).toBeUndefined();
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
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['cis'], ['custom'])).toBeTruthy();
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['cis'], [])).toBeFalsy();
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['cis'], undefined)).toBeFalsy();
    // built-in still works, and a tailored suite is not matched as a profile
    expect(isOwnedByBaseline(lbl('baseline-cis'), ['cis'], ['custom'])).toBeTruthy();
    // tailored suite must not match via profiles even if profiles contains "tp-custom"
    expect(isOwnedByBaseline(lbl('baseline-tp-custom'), ['tp-custom'], undefined)).toBeFalsy();
    // empty tailored suite label is not owned
    expect(isOwnedByBaseline(lbl('baseline-tp-'), ['cis'], [''])).toBeFalsy();
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
  // CR status.profiles[].key is not runtime type-checked; the coercion exists so
  // a tampered non-string key cannot throw on .toUpperCase.
  it('coerces tampered non-string keys instead of throwing', () => {
    // SAFETY: deliberately tampered key (null); profileTitle must coerce, not throw.
    expect(profileTitle(null as string)).toBe('');
    // SAFETY: deliberately tampered key (undefined); profileTitle must coerce, not throw.
    expect(profileTitle(undefined as string)).toBe('');
    // SAFETY: deliberately tampered key (number); profileTitle must stringify, not throw.
    expect(profileTitle(42 as string)).toBe('42');
  });
});

// Single source for the "Scanning is disabled" dead-end shown by all three tabs;
// a drift here would make one tab render controls while another claims disabled.
describe('scanningDisabled', () => {
  const cb = (spec: Partial<ClusterBaseline['spec']>): ClusterBaseline => ({
    metadata: { name: 'cb' },
    spec: { profiles: [], ...spec },
  });

  it('is true when neither built-in nor tailored profiles are selected', () => {
    expect(scanningDisabled(undefined)).toBeTruthy();
    expect(scanningDisabled(cb({}))).toBeTruthy();
    expect(scanningDisabled(cb({ profiles: [], tailoredProfiles: [] }))).toBeTruthy();
  });
  it('is false when any profile or tailored profile is selected', () => {
    expect(scanningDisabled(cb({ profiles: ['cis'] }))).toBeFalsy();
    expect(scanningDisabled(cb({ profiles: [], tailoredProfiles: ['tp-custom'] }))).toBeFalsy();
    expect(scanningDisabled(cb({ profiles: ['cis'], tailoredProfiles: ['tp-custom'] }))).toBeFalsy();
  });
});
