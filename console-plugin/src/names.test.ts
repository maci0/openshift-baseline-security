import { isValidK8sName, isValidTailoredProfileName } from './names';

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

describe('isValidK8sName', () => {
  it('accepts valid RFC1123 subdomain names', () => {
    expect(isValidK8sName('cis-custom')).toBe(true);
    expect(isValidK8sName('a')).toBe(true);
    expect(isValidK8sName('ocp4.cis.1')).toBe(true);
  });
  it('rejects invalid names', () => {
    expect(isValidK8sName('')).toBe(false);
    expect(isValidK8sName('My Profile')).toBe(false);
    expect(isValidK8sName('UPPER')).toBe(false);
    expect(isValidK8sName('-lead')).toBe(false);
    expect(isValidK8sName('trail-')).toBe(false);
    expect(isValidK8sName('a..b')).toBe(false);
    expect(isValidK8sName('a.-b')).toBe(false);
    expect(isValidK8sName('a-.b')).toBe(false);
    expect(isValidK8sName('a'.repeat(254))).toBe(false);
  });
  // Free-form TailoredProfile / resource names typed in the console; never throw,
  // and a true result must satisfy length + RFC1123 shape (no uppercase, no ends).
  it('fuzz: never throws; true implies length and shape invariants', () => {
    for (let i = 0; i < 2000; i++) {
      const name =
        i % 5 === 0
          ? randomString(i % 80)
          : i % 5 === 1
            ? 'a'.repeat(i % 260)
            : i % 5 === 2
              ? `x${'-'.repeat(i % 10)}y`
              : i % 5 === 3
                ? ''
                : `name-${i}`;
      let ok: boolean;
      expect(() => {
        ok = isValidK8sName(name);
      }).not.toThrow();
      expect(typeof ok!).toBe('boolean');
      if (ok!) {
        expect(name.length).toBeGreaterThan(0);
        expect(name.length).toBeLessThanOrEqual(253);
        expect(name).toMatch(/^[a-z0-9]/);
        expect(name).toMatch(/[a-z0-9]$/);
        expect(name).not.toMatch(/[A-Z_\s]/);
      }
    }
  });
});

describe('isValidTailoredProfileName', () => {
  it('accepts names that fit baseline-tp-<name> label budget (51 chars)', () => {
    expect(isValidTailoredProfileName('cis-custom')).toBe(true);
    expect(isValidTailoredProfileName('a'.repeat(51))).toBe(true);
  });
  it('rejects names longer than the ClusterBaseline tailoredProfiles MaxLength', () => {
    // isValidK8sName would accept 52 alphanumerics; suite label would exceed 63.
    expect(isValidK8sName('a'.repeat(52))).toBe(true);
    expect(isValidTailoredProfileName('a'.repeat(52))).toBe(false);
  });
  it('rejects the same shape invalids as isValidK8sName', () => {
    expect(isValidTailoredProfileName('')).toBe(false);
    expect(isValidTailoredProfileName('UPPER')).toBe(false);
    expect(isValidTailoredProfileName('-x')).toBe(false);
    expect(isValidTailoredProfileName('a..b')).toBe(false);
  });
  // suite label is "baseline-tp-<name>" (63 char k8s label budget => name <= 51).
  it('fuzz: never throws; true implies k8s name and len <= 51', () => {
    for (let i = 0; i < 2000; i++) {
      const name =
        i % 4 === 0
          ? randomString(i % 60)
          : i % 4 === 1
            ? 'a'.repeat(i % 60)
            : i % 4 === 2
              ? `tp-${i}`
              : '';
      let ok: boolean;
      expect(() => {
        ok = isValidTailoredProfileName(name);
      }).not.toThrow();
      if (ok!) {
        expect(name.length).toBeLessThanOrEqual(51);
        expect(isValidK8sName(name)).toBe(true);
      }
    }
  });
});
