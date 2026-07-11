import {
  checkProfileLabel,
  isOwnedByBaseline,
  suiteFilterKey,
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
  it('checkProfileLabel uppercases built-ins, keeps tailored names, dashes unknown', () => {
    expect(checkProfileLabel(lbl('baseline-cis'))).toBe('CIS');
    expect(checkProfileLabel(lbl('baseline-pci-dss'))).toBe('PCI-DSS');
    expect(checkProfileLabel(lbl('baseline-tp-cis-custom'))).toBe('cis-custom');
    expect(checkProfileLabel(lbl('other'))).toBe('—');
    expect(checkProfileLabel(undefined)).toBe('—');
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
