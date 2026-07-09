import { isOwnedByBaseline } from './models';

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
