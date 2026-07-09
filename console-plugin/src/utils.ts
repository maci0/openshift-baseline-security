import { ComplianceCheckResult } from './models';

// Normalize k8s watch / fetch errors (string | Error | { message }) for Alerts.
export const errorMessage = (err: unknown): string | null => {
  if (err == null || err === '') {
    return null;
  }
  if (typeof err === 'string') {
    return err;
  }
  if (err instanceof Error) {
    return err.message || err.name;
  }
  if (typeof err === 'object' && 'message' in err) {
    const m = (err as { message: unknown }).message;
    if (typeof m === 'string' && m) {
      return m;
    }
  }
  return String(err);
};

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
export const checkTitle = (r: ComplianceCheckResult): string =>
  r.description?.split('\n')[0]?.trim() || r.metadata.name;

export const checkBody = (r: ComplianceCheckResult): string =>
  r.description?.split('\n').slice(1).join('\n').trim() ?? '';

// RFC 4180 CSV cell: quote when it contains a comma, quote, CR or LF, and
// double embedded quotes. Values come from CR data, i.e. untrusted input.
const csvCell = (v: string): string =>
  /[",\r\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;

// resultsCsv serializes check results to a CSV report (name,title,status,
// severity). Deterministic column order; one header row.
// Console URL for a namespaced ComplianceCheckResult, so the detail modal can
// deep-link to the raw Compliance Operator resource.
export const checkResultHref = (name: string): string =>
  `/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/${encodeURIComponent(
    name,
  )}`;

export const resultsCsv = (results: ComplianceCheckResult[]): string => {
  const header = ['name', 'title', 'status', 'severity'];
  const rows = results.map((r) =>
    [r.metadata.name, checkTitle(r), r.status, r.severity].map((c) => csvCell(String(c ?? ''))).join(','),
  );
  return [header.join(','), ...rows].join('\r\n');
};

// New profile list after toggling one key; null when the change is invalid
// (the CRD requires at least one profile).
export const toggledProfiles = (
  current: string[],
  key: string,
  checked: boolean,
): string[] | null => {
  const next = checked ? [...new Set([...current, key])] : current.filter((p) => p !== key);
  return next.length ? next : null;
};

// JSON patch for spec.remediation.apply (Automatic|Manual).
export const remediationApplyPatch = (hasRemediation: boolean, automatic: boolean) => {
  const apply = automatic ? 'Automatic' : 'Manual';
  return hasRemediation
    ? [{ op: 'replace' as const, path: '/spec/remediation/apply', value: apply }]
    : [{ op: 'add' as const, path: '/spec/remediation', value: { apply } }];
};

// JSON patch to trigger a Compliance Operator rescan. value must change each
// click so a re-rescan is observed when the annotation already exists.
// When metadata.annotations is missing, add the whole map (nested add fails).
export const rescanPatch = (hasAnnotations: boolean, value: string) =>
  hasAnnotations
    ? [
        {
          op: 'add' as const,
          path: '/metadata/annotations/compliance.openshift.io~1rescan',
          value,
        },
      ]
    : [
        {
          op: 'add' as const,
          path: '/metadata/annotations',
          value: { 'compliance.openshift.io/rescan': value },
        },
      ];

// PatternFly semantic status color token for a 0-100 score.
export const scoreColor = (score?: number): string =>
  score == null || score < 60
    ? 'var(--pf-t--global--icon--color--status--danger--default)'
    : score < 90
      ? 'var(--pf-t--global--icon--color--status--warning--default)'
      : 'var(--pf-t--global--icon--color--status--success--default)';

// Deep-link into Results with a status (and optional profile) row filter.
// Strip unpaired surrogates so encodeURIComponent never throws on garbage.
export const resultsHref = (status: string, profile?: string): string => {
  const clean = (s: string) => s.replace(/[\uD800-\uDFFF]/g, '');
  const params = new URLSearchParams();
  params.set('rowFilter-result-status', clean(status));
  if (profile) {
    params.set('rowFilter-result-profile', clean(profile));
  }
  return `/baseline-security/results?${params.toString()}`;
};