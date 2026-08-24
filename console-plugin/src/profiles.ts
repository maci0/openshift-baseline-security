// Profile toggle helpers and TailoredProfile create manifests.
import { COMPLIANCE_NAMESPACE, DEFAULT_BASE_PROFILE, isProfileKey, PROFILE_MAX_ITEMS } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';
import { isString } from './parse';

// Rule object written into TailoredProfile disableRules/enableRules. Shared by
// tailoredProfileManifest and the ProfilesTab update path so both write the
// identical shape.
export interface ConsoleRule {
  name: string;
  rationale: string;
}

// Trimmed, validated, deduped rule-name selection; disable wins over enable.
export interface RuleSelection {
  disable: string[];
  enable: string[];
}

// TailoredProfile manifest written on create; mirrors apiVersion/kind of
// compliance.openshift.io/v1alpha1 and the fields admission accepts.
export interface TailoredProfileManifest {
  apiVersion: 'compliance.openshift.io/v1alpha1';
  kind: 'TailoredProfile';
  metadata: { name: string; namespace: string };
  spec: TailoredProfileSpec;
}

export interface TailoredProfileSpec {
  title: string;
  extends: string;
  enableRules?: ConsoleRule[];
  disableRules?: ConsoleRule[];
}

// Fields read back off a cluster TailoredProfile during retry adoption; values
// are untrusted and narrowed through guards before comparison.
export interface ExistingTailoredProfile {
  spec?: {
    extends?: unknown;
    enableRules?: unknown;
    disableRules?: unknown;
  };
}

// New profile list after toggling one key. An empty result is valid: clearing
// every profile disables scanning (the operator prunes the bindings).
// Refuse unknown keys and adds past CRD MaxItems=8 so admission is not the
// first failure mode.
export const toggledProfiles = (current: string[], key: string, checked: boolean): string[] => {
  if (!checked) {
    return current.filter((p) => p !== key);
  }
  // CRD Enum: only known ProfileKey values are admitted.
  if (!isProfileKey(key)) {
    return current;
  }
  if (current.includes(key)) {
    return current;
  }
  if (current.length >= PROFILE_MAX_ITEMS) {
    return current;
  }
  return [...current, key];
};

// DNS-1123 rule/profile names only; drop free-form junk at the console boundary
// so the create payload cannot carry injection-shaped strings into the CR.
const cleanRuleNames = (rules: string[]): string[] => {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of rules) {
    const n = raw.trim();
    if (!n || !isValidK8sName(n) || seen.has(n)) continue;
    seen.add(n);
    out.push(n);
  }
  return out;
};

// Rule object written into TailoredProfile disableRules/enableRules. Shared by
// tailoredProfileManifest and the ProfilesTab update path so both write the
// identical shape.
export const consoleRule = (n: string): ConsoleRule => ({
  name: n,
  rationale: 'set via console',
});

// Normalize a rule selection exactly as tailoredProfileManifest does so the
// create payload and the update payload cannot drift: trim, drop invalid names,
// dedupe, and let disable win over enable (a rule in both lists is
// contradictory; fail closed).
export const cleanRuleSelection = (
  disableRules: string[],
  enableRules: string[],
): RuleSelection => {
  const disable = cleanRuleNames(disableRules);
  const disableSet = new Set(disable);
  return {
    disable,
    enable: cleanRuleNames(enableRules).filter((n) => !disableSet.has(n)),
  };
};

// Build a TailoredProfile CR body from an editor: a base profile to extend and
// optional rule names to enable/disable. Empty rule lists are omitted.
// Empty/whitespace extends defaults to ocp4-cis (same as the Profiles form).
// Invalid non-empty extends throws (fail closed); invalid rule names are dropped.
// metadata.name must be a valid TailoredProfile name (DNS-1123, max 51); callers
// validate first, and this helper fails closed so a future call path cannot
// ship free-form / path-shaped strings into the create payload.
export const tailoredProfileManifest = (
  name: string,
  extendsProfile: string,
  disableRules: string[],
  enableRules: string[] = [],
): TailoredProfileManifest => {
  const profileName = name.trim();
  if (!isValidTailoredProfileName(profileName)) {
    throw new Error('invalid TailoredProfile name');
  }
  // Empty means "use the form default"; non-empty junk must not silently become CIS.
  const extendsName = extendsProfile.trim() || DEFAULT_BASE_PROFILE;
  if (!isValidK8sName(extendsName)) {
    throw new Error('invalid base profile name');
  }
  const spec: TailoredProfileSpec = {
    title: profileName,
    extends: extendsName,
  };
  const rule = consoleRule;
  const { disable, enable } = cleanRuleSelection(disableRules, enableRules);
  if (enable.length) spec.enableRules = enable.map(rule);
  if (disable.length) spec.disableRules = disable.map(rule);
  return {
    apiVersion: 'compliance.openshift.io/v1alpha1',
    kind: 'TailoredProfile',
    metadata: { name: profileName, namespace: COMPLIANCE_NAMESPACE },
    spec,
  };
};

// tailoredProfileSpecMatches reports whether an existing TailoredProfile's spec
// equals what tailoredProfileManifest would build for the same inputs. A create
// that returns AlreadyExists uses this to tell a genuine retry (same settings,
// safe to adopt and bind) from a name collision with an unrelated profile
// (different settings, which must not be silently bound as if it were ours).
export const tailoredProfileSpecMatches = (
  existing: ExistingTailoredProfile | undefined,
  extendsProfile: string,
  disableRules: string[],
  enableRules: string[] = [],
): boolean => {
  const spec = existing?.spec ?? {};
  const isRuleRef = (v: unknown): v is { name?: unknown } =>
    v !== null && typeof v === 'object';
  const isRuleList = (v: unknown): v is readonly unknown[] => Array.isArray(v);
  const names = (rules: readonly unknown[] | undefined): string[] =>
    (rules ?? [])
      .filter(isRuleRef)
      .map((r) => (isString(r.name) ? r.name : ''))
      .filter(Boolean)
      .sort();
  const eq = (a: string[], b: string[]) =>
    a.length === b.length && a.every((x, i) => x === b[i]);
  // Mirror the manifest's normalization: default extends and the same
  // cleanRuleSelection (drop invalid rule names, disable wins over enable) so
  // the comparison sees the same set.
  const extendsName = extendsProfile.trim() || DEFAULT_BASE_PROFILE;
  const { disable, enable } = cleanRuleSelection(disableRules, enableRules);
  const disableSorted = [...disable].sort();
  const enableSorted = [...enable].sort();
  const existingExtends = isString(spec.extends) ? spec.extends : '';
  return (
    existingExtends === extendsName &&
    eq(names(isRuleList(spec.disableRules) ? spec.disableRules : []), disableSorted) &&
    eq(names(isRuleList(spec.enableRules) ? spec.enableRules : []), enableSorted)
  );
};
