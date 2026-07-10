import {
  ClusterBaseline,
  ComplianceCheckResult,
  ComplianceRemediation,
  ResultCounts,
} from './models';

// Pick the singleton ClusterBaseline (named "cluster", else the first) and
// return its score, or null when there is none / it has not scored yet. Shared
// by the cluster Overview detail item.
export const clusterScore = (baselines?: ClusterBaseline[]): number | null => {
  const b = baselines?.find((x) => x.metadata.name === 'cluster') ?? baselines?.[0];
  return b?.status?.score ?? null;
};

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

// Sum result counts across profiles (built-in + tailored) for the composition
// donut, so its slices match the score, which includes all of them.
export const aggregateCounts = (...groups: ResultCounts[]): ResultCounts =>
  groups.reduce(
    (a, g) => ({
      pass: a.pass + g.pass,
      fail: a.fail + g.fail,
      manual: a.manual + g.manual,
      error: a.error + g.error,
      notApplicable: a.notApplicable + g.notApplicable,
    }),
    { pass: 0, fail: 0, manual: 0, error: 0, notApplicable: 0 },
  );

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
export const checkTitle = (r: ComplianceCheckResult): string =>
  r.description?.split('\n')[0]?.trim() || r.metadata.name;

export const checkBody = (r: ComplianceCheckResult): string =>
  r.description?.split('\n').slice(1).join('\n').trim() ?? '';

// RFC 4180 CSV cell with spreadsheet-formula hardening. Values come from CR
// data, i.e. untrusted input. Prefix formula-looking cells with an apostrophe
// before quoting so spreadsheet apps import them as literal text.
const csvCell = (v: string): string => {
  const safe = /^[=+\-@\t\r\n]/.test(v) ? `'${v}` : v;
  return /[",\t\r\n]/.test(safe) ? `"${safe.replace(/"/g, '""')}"` : safe;
};

// resultsCsv serializes check results to a CSV report (name,title,status,
// severity). Deterministic column order; one header row.
export const resultsCsv = (results: ComplianceCheckResult[]): string => {
  const header = ['name', 'title', 'status', 'severity'];
  const rows = results.map((r) =>
    [r.metadata.name, checkTitle(r), r.status, r.severity].map((c) => csvCell(String(c ?? ''))).join(','),
  );
  return [header.join(','), ...rows].join('\r\n');
};

// A node remediation renders into a MachineConfig; applying it reboots nodes.
export const isNodeRemediation = (rem: ComplianceRemediation): boolean =>
  rem.spec.current?.object?.kind === 'MachineConfig';

// Pretty-printed rendered object for the remediation detail view.
export const remediationObjectText = (rem: ComplianceRemediation): string => {
  const obj = rem.spec.current?.object;
  return obj ? JSON.stringify(obj, null, 2) : '';
};

// Console URL for a namespaced ComplianceCheckResult, so the detail modal can
// deep-link to the raw Compliance Operator resource.
export const checkResultHref = (name: string): string =>
  `/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/${encodeURIComponent(
    name,
  )}`;

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
    ? [{ op: 'add' as const, path: '/spec/remediation/apply', value: apply }]
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
