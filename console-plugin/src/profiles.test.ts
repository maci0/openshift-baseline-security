import { isValidK8sName, isValidTailoredProfileName } from './names';
import {
  cleanRuleSelection,
  consoleRule,
  TailoredProfileManifest,
  tailoredProfileManifest,
  tailoredProfileSpecMatches,
  toggledProfiles,
} from './profiles';
import { fuzzRand, randomString } from './testing/fuzz';

describe('cleanRuleSelection', () => {
  it('trims, drops invalid names, dedupes, and lets disable win', () => {
    const { disable, enable } = cleanRuleSelection(
      [' r1 ', 'bad name', 'r2', 'r1'],
      ['r3', 'r1', ' bad name ', 'r3'],
    );
    expect(disable).toEqual(['r1', 'r2']);
    // Disable wins over enable (fail closed), duplicates dropped.
    expect(enable).toEqual(['r3']);
  });
  it('returns the same sets the manifest ships, so update and create cannot drift', () => {
    const selection = cleanRuleSelection(['dup', 'off-only'], ['dup', 'on-only']);
    const spec = tailoredProfileManifest('x', 'ocp4-cis', ['dup', 'off-only'], [
      'dup',
      'on-only',
    ]).spec as { enableRules?: { name: string }[]; disableRules?: { name: string }[] };
    expect((spec.disableRules ?? []).map((r) => r.name)).toEqual(selection.disable);
    expect((spec.enableRules ?? []).map((r) => r.name)).toEqual(selection.enable);
  });
});

describe('consoleRule', () => {
  it('writes the shared rationale so create and update payloads match', () => {
    expect(consoleRule('r1')).toEqual({ name: 'r1', rationale: 'set via console' });
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
  it('allows clearing the last profile (disables scanning)', () => {
    expect(toggledProfiles(['cis'], 'cis', false)).toEqual([]);
  });
  it('refuses adds past CRD MaxItems=8', () => {
    const full = ['cis', 'pci-dss', 'nist-moderate', 'nist-high', 'stig', 'nerc-cip', 'e8', 'bsi'];
    // 'extra' is also not a ProfileKey enum value; either bound fails closed.
    expect(toggledProfiles(full, 'extra', true)).toEqual(full);
    expect(toggledProfiles(full, 'cis', false)).toEqual(full.filter((k) => k !== 'cis'));
  });
  it('refuses unknown ProfileKey values (CRD enum fail-closed)', () => {
    expect(toggledProfiles(['cis'], 'extra', true)).toEqual(['cis']);
    expect(toggledProfiles(['cis'], 'CIS', true)).toEqual(['cis']);
    expect(toggledProfiles(['cis'], '', true)).toEqual(['cis']);
    // Remove still filters by exact key match (unknown keys can be stripped).
    expect(toggledProfiles(['cis', 'bogus'], 'bogus', false)).toEqual(['cis']);
  });
  it('fuzz: never duplicates when adding; removing the missing key is a no-op', () => {
    const keys = ['cis', 'stig', 'e8', 'bsi', 'pci-dss'];
    for (let i = 0; i < 2000; i++) {
      const n = (i % 5) + 1;
      const current = keys.slice(0, n);
      const key = keys[i % keys.length];
      const checked = i % 2 === 0;
      const next = toggledProfiles(current, key, checked);
      expect(new Set(next).size).toBe(next.length);
      expect(next.length).toBeLessThanOrEqual(8);
      if (checked) {
        expect(next).toContain(key);
      }
    }
  });
  // Toggle involution: enable X then disable X returns the prior set when the
  // add was allowed (not at MaxItems, key not already present). Disable then
  // re-enable restores membership when the set was non-empty after remove.
  it('fuzz: enable then disable is involution when the add is accepted', () => {
    const keys = ['cis', 'stig', 'e8', 'bsi', 'pci-dss', 'nerc-cip', 'nist-high', 'nist-moderate'];
    for (let i = 0; i < 2000; i++) {
      const n = i % 8; // 0..7 so an add can succeed under MaxItems=8
      const current = keys.slice(0, n);
      const key = keys[i % keys.length];
      if (current.includes(key)) {
        // Already present: enable is a no-op; disable then re-enable restores.
        const without = toggledProfiles(current, key, false);
        expect(without).not.toContain(key);
        expect(toggledProfiles(without, key, true).sort()).toEqual([...current].sort());
        continue;
      }
      const added = toggledProfiles(current, key, true);
      if (added.length === current.length) {
        // MaxItems refused the add; leave current unchanged.
        expect(added).toEqual(current);
        continue;
      }
      expect(added).toContain(key);
      expect(toggledProfiles(added, key, false).sort()).toEqual([...current].sort());
    }
  });
});

describe('tailoredProfileManifest', () => {
  it('keeps a rule present in both enable and disable only in disable (fail closed)', () => {
    // (name, extends, disableRules, enableRules); a rule in both must not ship in
    // both enableRules and disableRules (self-conflicting manifest).
    const m = tailoredProfileManifest('cis-custom', 'ocp4-cis', ['dup', 'off-only'], [
      'dup',
      'on-only',
    ]);
    const spec = m.spec as {
      enableRules?: { name: string }[];
      disableRules?: { name: string }[];
    };
    const enabled = (spec.enableRules ?? []).map((r) => r.name);
    const disabled = (spec.disableRules ?? []).map((r) => r.name);
    expect(disabled).toContain('dup');
    expect(enabled).not.toContain('dup');
    expect(enabled).toContain('on-only');
  });
  it('builds a TailoredProfile CR, omitting empty rule lists', () => {
    const m = tailoredProfileManifest('cis-custom', 'ocp4-cis', []);
    expect(m.kind).toBe('TailoredProfile');
    expect((m.metadata as { name: string }).name).toBe('cis-custom');
    expect((m.spec as { extends: string }).extends).toBe('ocp4-cis');
    expect(m.spec.disableRules).toBeUndefined();
  });
  it('includes enable/disable rules when provided', () => {
    const m = tailoredProfileManifest('x', 'ocp4-cis', ['r1', 'r2'], ['r3']);
    const spec = m.spec as { enableRules: { name: string }[]; disableRules: { name: string }[] };
    expect(spec.disableRules.map((r) => r.name)).toEqual(['r1', 'r2']);
    expect(spec.enableRules.map((r) => r.name)).toEqual(['r3']);
  });
  it('drops non-DNS-1123 rule names; empty extends defaults to ocp4-cis', () => {
    const m = tailoredProfileManifest(
      'x',
      '',
      ['ok-rule', 'bad name', '../x', 'ok-rule', ''],
      ['also-ok', 'has spaces'],
    );
    const spec = m.spec as { extends: string; enableRules?: { name: string }[]; disableRules?: { name: string }[] };
    expect(spec.extends).toBe('ocp4-cis');
    expect(spec.disableRules?.map((r) => r.name)).toEqual(['ok-rule']);
    expect(spec.enableRules?.map((r) => r.name)).toEqual(['also-ok']);
  });
  it('refuses invalid base profile extends (no silent CIS substitution)', () => {
    expect(() => tailoredProfileManifest('x', 'not a profile!!!', [])).toThrow(
      /invalid base profile name/,
    );
    expect(() => tailoredProfileManifest('x', '../evil', [])).toThrow(/invalid base profile name/);
  });
  it('refuses invalid metadata.name (path-shaped / over-long / empty)', () => {
    expect(() => tailoredProfileManifest('../x', 'ocp4-cis', [])).toThrow(/invalid TailoredProfile name/);
    expect(() => tailoredProfileManifest('', 'ocp4-cis', [])).toThrow(/invalid TailoredProfile name/);
    expect(() => tailoredProfileManifest('a'.repeat(52), 'ocp4-cis', [])).toThrow(
      /invalid TailoredProfile name/,
    );
    expect(() => tailoredProfileManifest('has spaces', 'ocp4-cis', [])).toThrow(
      /invalid TailoredProfile name/,
    );
  });
  it('trims a valid name before writing metadata and title', () => {
    const m = tailoredProfileManifest('  cis-custom  ', 'ocp4-cis', []);
    expect((m.metadata as { name: string }).name).toBe('cis-custom');
    expect((m.spec as { title: string }).title).toBe('cis-custom');
  });
  // Form free-text (name, extends, rule lists) is untrusted. Invalid inputs must
  // throw a typed Error (fail closed) or produce a DNS-1123-only create payload.
  it('fuzz: invalid names throw; accepted payloads stay DNS-1123 and kind-correct', () => {
    const seeds = [
      '',
      '../x',
      'has spaces',
      'a'.repeat(52),
      'ok-name',
      'ocp4-cis',
      'rule-1',
      '!!!',
      'UPPER',
      'ends-',
      '-starts',
    ];
    for (let i = 0; i < 1000; i++) {
      const name =
        i < seeds.length ? seeds[i] : i % 4 === 0 ? `tp-${i}` : randomString(i % 60);
      const extendsBase =
        i % 5 === 0 ? 'ocp4-cis' : i % 7 === 0 ? '' : randomString(i % 40);
      const rules = [
        i % 3 === 0 ? `rule-${i}` : randomString(i % 20),
        '../x',
        'has spaces',
        '',
        seeds[i % seeds.length],
      ];
      let threw = false;
      let m: TailoredProfileManifest | undefined;
      try {
        m = tailoredProfileManifest(name, extendsBase, rules, rules);
      } catch (e) {
        threw = true;
        expect(e).toBeInstanceOf(Error);
        expect((e as Error).message).toMatch(/invalid (TailoredProfile|base profile) name/);
      }
      if (!threw) {
        expect(m).toBeDefined();
        expect(m!.kind).toBe('TailoredProfile');
        expect(m!.apiVersion).toBe('compliance.openshift.io/v1alpha1');
        const metaName = (m!.metadata as { name: string }).name;
        expect(isValidTailoredProfileName(metaName)).toBeTruthy();
        const spec = m!.spec as {
          extends: string;
          enableRules?: { name: string }[];
          disableRules?: { name: string }[];
        };
        expect(isValidK8sName(spec.extends)).toBeTruthy();
        for (const r of [...(spec.enableRules ?? []), ...(spec.disableRules ?? [])]) {
          expect(isValidK8sName(r.name)).toBeTruthy();
        }
      }
    }
  });
});

// On an AlreadyExists create, the authoring form adopts the existing CR only if
// its content matches what we would have created (a genuine retry). A collision
// with an unrelated profile must NOT match, or the user's edits are silently
// discarded and a different profile is scanned under a false "created and
// bound" success.

describe('tailoredProfileSpecMatches', () => {
  const specOf = (extendsBase: string, disable: string[], enable: string[] = []) =>
    tailoredProfileManifest('x', extendsBase, disable, enable);
  it('matches an identical spec regardless of rule order', () => {
    const existing = specOf('ocp4-cis', ['b-rule', 'a-rule']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['a-rule', 'b-rule'])).toBeTruthy();
  });
  it('matches when both sides default extends to ocp4-cis', () => {
    const existing = specOf('ocp4-cis', []);
    expect(tailoredProfileSpecMatches(existing, '', [])).toBeTruthy();
  });
  it('does not match a different base profile', () => {
    const existing = specOf('ocp4-cis', ['a-rule']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-pci-dss', ['a-rule'])).toBeFalsy();
  });
  it('does not match a different disable-rule set (the collision case)', () => {
    const existing = specOf('ocp4-cis', ['rule-x']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['rule-y'])).toBeFalsy();
  });
  it('ignores invalid rule names the manifest would have dropped', () => {
    const existing = specOf('ocp4-cis', ['good-rule']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['good-rule', 'bad name'])).toBeTruthy();
  });
  it('treats a rule in both enable and disable as disabled (mirrors the manifest)', () => {
    const existing = specOf('ocp4-cis', ['dup'], ['dup', 'on-only']);
    expect(tailoredProfileSpecMatches(existing, 'ocp4-cis', ['dup'], ['dup', 'on-only'])).toBeTruthy();
  });
  it('does not match undefined / empty existing against a real spec', () => {
    expect(tailoredProfileSpecMatches(undefined, 'ocp4-cis', ['a-rule'])).toBeFalsy();
    expect(tailoredProfileSpecMatches({}, 'ocp4-cis', ['a-rule'])).toBeFalsy();
  });
});
