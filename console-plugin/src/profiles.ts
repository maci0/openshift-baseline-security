import { PROFILE_MAX_ITEMS } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';

// New profile list after toggling one key. An empty result is valid: clearing
// every profile disables scanning (the operator prunes the bindings).
// Refuse adds past CRD MaxItems=8 so admission is not the first failure mode.
export const toggledProfiles = (current: string[], key: string, checked: boolean): string[] => {
  if (!checked) {
    return current.filter((p) => p !== key);
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
// Invalid extends falls back to ocp4-cis; invalid rule names are dropped.
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
  const base = extendsProfile.trim();
  const extendsName = isValidK8sName(base) ? base : 'ocp4-cis';
  const spec: Record<string, unknown> = {
    title: profileName,
    extends: extendsName,
  };
  const rule = (n: string) => ({ name: n, rationale: 'set via console' });
  const enable = cleanRuleNames(enableRules);
  const disable = cleanRuleNames(disableRules);
  if (enable.length) spec.enableRules = enable.map(rule);
  if (disable.length) spec.disableRules = disable.map(rule);
  return {
    apiVersion: 'compliance.openshift.io/v1alpha1',
    kind: 'TailoredProfile',
    metadata: { name: profileName, namespace: 'openshift-compliance' },
    spec,
  };
};
