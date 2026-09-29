// K8s and TailoredProfile name validation (DNS-1123, CRD MaxLength bounds).
const dns1123Subdomain =
  /^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$/;

// Longest name the apiserver accepts, the unit a CRD maxLength is expressed in.
// Named so the waiver field-length message quotes the same number the check
// enforces. The bounds below count UTF-16 code units, which is only the same
// number for an accepted name because dns1123Subdomain admits ASCII alone.
export const K8S_NAME_MAX_LEN = 253;

// Valid Kubernetes resource name (RFC1123 subdomain): each dot-separated label
// starts and ends alphanumeric, with lowercase alphanumeric or '-' inside.
export const isValidK8sName = (name: string): boolean =>
  name.length > 0 && name.length <= K8S_NAME_MAX_LEN && dns1123Subdomain.test(name);

// TailoredProfile names bound into the baseline are capped at 51 so the suite
// label "baseline-tp-<name>" stays a valid Kubernetes label value (63 chars).
// Matches ClusterBaselineSpec.tailoredProfiles items MaxLength. That is a
// code-point bound and this is a code-unit one; they agree because the name
// has to be a valid Kubernetes name, and those are ASCII.
export const isValidTailoredProfileName = (name: string): boolean =>
  name.length <= 51 && isValidK8sName(name);
