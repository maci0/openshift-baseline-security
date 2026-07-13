// Profile toggle helpers and TailoredProfile create manifests.
import { COMPLIANCE_NAMESPACE, isProfileKey, PROFILE_MAX_ITEMS } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';

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
): Record<string, unknown> => {
  const profileName = name.trim();
  if (!isValidTailoredProfileName(profileName)) {
    throw new Error('invalid TailoredProfile name');
  }
  // Empty means "use the form default"; non-empty junk must not silently become CIS.
  const extendsName = extendsProfile.trim() || 'ocp4-cis';
  if (!isValidK8sName(extendsName)) {
    throw new Error('invalid base profile name');
  }
  const spec: Record<string, unknown> = {
    title: profileName,
    extends: extendsName,
  };
  const rule = (n: string) => ({ name: n, rationale: 'set via console' });
  const disable = cleanRuleNames(disableRules);
  // A rule in both lists is contradictory; disable wins (fail closed) so the
  // console never ships a self-conflicting enable+disable manifest.
  const disableSet = new Set(disable);
  const enable = cleanRuleNames(enableRules).filter((n) => !disableSet.has(n));
  if (enable.length) spec.enableRules = enable.map(rule);
  if (disable.length) spec.disableRules = disable.map(rule);
  return {
    apiVersion: 'compliance.openshift.io/v1alpha1',
    kind: 'TailoredProfile',
    metadata: { name: profileName, namespace: COMPLIANCE_NAMESPACE },
    spec,
  };
};
