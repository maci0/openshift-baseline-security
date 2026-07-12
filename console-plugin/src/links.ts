// Drop unpaired surrogates so encodeURIComponent / URLSearchParams never throw
// on malformed UTF-16 from untrusted names.
const stripSurrogates = (s: string): string => s.replace(/[\uD800-\uDFFF]/g, '');

// Console URL for a namespaced ComplianceCheckResult, so the detail modal can
// deep-link to the raw Compliance Operator resource.
export const checkResultHref = (name: string): string =>
  `/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/${encodeURIComponent(
    stripSurrogates(name),
  )}`;

// Console URL for a MachineConfigPool, so the drill-down can deep-link to it.
export const machineConfigPoolHref = (name: string): string =>
  `/k8s/cluster/machineconfiguration.openshift.io~v1~MachineConfigPool/${encodeURIComponent(
    stripSurrogates(name),
  )}`;

// Deep-link into Results with a status (and optional profile) row filter.
// Use "WAIVED" (not FAIL) for score-excluded checks so the link matches
// Overview fail/waived counts; see resultFilterStatus.
export const resultsHref = (status: string, profile?: string): string => {
  const params = new URLSearchParams();
  params.set('rowFilter-result-status', stripSurrogates(status));
  if (profile) {
    params.set('rowFilter-result-profile', stripSurrogates(profile));
  }
  return `/baseline-security/results?${params.toString()}`;
};
