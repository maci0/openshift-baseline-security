const dns1123Subdomain =
  /^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$/;

// Valid Kubernetes resource name (RFC1123 subdomain): each dot-separated label
// starts and ends alphanumeric, with lowercase alphanumeric or '-' inside.
export const isValidK8sName = (name: string): boolean =>
  name.length > 0 && name.length <= 253 && dns1123Subdomain.test(name);

// TailoredProfile names bound into the baseline are capped at 51 so the suite
// label "baseline-tp-<name>" stays a valid Kubernetes label value (63 chars).
// Matches ClusterBaselineSpec.tailoredProfiles items MaxLength.
export const isValidTailoredProfileName = (name: string): boolean =>
  name.length > 0 && name.length <= 51 && dns1123Subdomain.test(name);
