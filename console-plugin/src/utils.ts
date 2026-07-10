import {
  ClusterBaseline,
  ComplianceCheckResult,
  ComplianceRemediation,
  ResultCounts,
  Waiver,
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
  // A message-bearing object, a null-prototype object, a throwing toString, or a
  // throwing `message` getter must all be tolerated: an error normalizer must
  // never throw. Guard the whole property access + String() fallback.
  try {
    if (typeof err === 'object' && 'message' in err) {
      const m = (err as { message: unknown }).message;
      if (typeof m === 'string' && m) {
        return m;
      }
    }
    return String(err);
  } catch {
    return 'Unknown error';
  }
};

// Sum result counts across profiles (built-in + tailored) for the composition
// donut, so its slices match the score, which includes all of them.
const count = (n: number | undefined): number => n ?? 0;

export const aggregateCounts = (...groups: ResultCounts[]): ResultCounts =>
  groups.reduce(
    (a, g) => ({
      pass: a.pass + count(g.pass),
      fail: a.fail + count(g.fail),
      manual: a.manual + count(g.manual),
      info: a.info + count(g.info),
      error: a.error + count(g.error),
      inconsistent: a.inconsistent + count(g.inconsistent),
      waived: a.waived + count(g.waived),
      notApplicable: a.notApplicable + count(g.notApplicable),
    }),
    { pass: 0, fail: 0, manual: 0, info: 0, error: 0, inconsistent: 0, waived: 0, notApplicable: 0 },
  );

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
export const checkTitle = (r: ComplianceCheckResult): string =>
  r.description?.split('\n')[0]?.trim() || r.metadata.name;

export const checkBody = (r: ComplianceCheckResult): string =>
  r.description?.split('\n').slice(1).join('\n').trim() ?? '';

// RFC 4180 CSV cell with spreadsheet-formula hardening. Values come from CR
// data, i.e. untrusted input. Prefix formula-looking cells with an apostrophe
// before quoting so spreadsheet apps import them as literal text. Also catch
// leading whitespace before a formula sigil (Excel often trims then evaluates).
const csvCell = (v: string): string => {
  const safe = /^\s*[=+\-@\t\r\n]/.test(v) ? `'${v}` : v;
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

// Drop unpaired surrogates so encodeURIComponent / URLSearchParams never throw
// on malformed UTF-16 from untrusted names.
const stripSurrogates = (s: string): string => s.replace(/[\uD800-\uDFFF]/g, '');

// Console URL for a namespaced ComplianceCheckResult, so the detail modal can
// deep-link to the raw Compliance Operator resource.
export const checkResultHref = (name: string): string =>
  `/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/${encodeURIComponent(
    stripSurrogates(name),
  )}`;

// The MachineConfigPool a node scan targeted, parsed from the scan-name label
// ("<profile>-node-<pool>"), or null for a platform (non-node) check. Node scans
// run per-MCP, so this is the pool the per-node results below belong to.
export const nodeScanPool = (result: ComplianceCheckResult): string | null => {
  const scan = result.metadata?.labels?.['compliance.openshift.io/scan-name'] ?? '';
  const i = scan.indexOf('-node-');
  return i < 0 ? null : scan.slice(i + '-node-'.length) || null;
};

// Console URL for a MachineConfigPool, so the drill-down can deep-link to it.
export const machineConfigPoolHref = (name: string): string =>
  `/k8s/cluster/machineconfiguration.openshift.io~v1~MachineConfigPool/${encodeURIComponent(
    stripSurrogates(name),
  )}`;

export type NodeStatus = { node: string; status: string };

// Per-node breakdown of an INCONSISTENT check. The Compliance Operator records
// the nodes that diverge from the majority in the inconsistent-source annotation
// ("node:STATUS,node:STATUS"), and the status the rest share in
// most-common-status. Untrusted cluster data: never throws on a malformed value.
export const inconsistentSources = (
  result: ComplianceCheckResult,
): { sources: NodeStatus[]; mostCommon: string | null } => {
  const ann = result.metadata?.annotations ?? {};
  const raw = ann['compliance.openshift.io/inconsistent-source'] ?? '';
  const sources = raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const i = s.indexOf(':');
      return i < 0
        ? { node: s, status: '' }
        : { node: s.slice(0, i), status: s.slice(i + 1) };
    });
  return { sources, mostCommon: ann['compliance.openshift.io/most-common-status'] || null };
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
    ? [{ op: 'add' as const, path: '/spec/remediation/apply', value: apply }]
    : [{ op: 'add' as const, path: '/spec/remediation', value: { apply } }];
};

// True when a check result is waived (accepted risk) by spec.waivers.
export const isWaived = (name: string, waivers?: Waiver[]): boolean =>
  !!waivers?.some((w) => w.name === name);

// JSON patch adding a waiver for a check. Adds the whole spec.waivers array when
// absent (nested add would 404), else appends one entry.
export const addWaiverPatch = (hasWaivers: boolean, name: string, reason: string) => {
  const entry = reason ? { name, reason } : { name };
  return hasWaivers
    ? [{ op: 'add' as const, path: '/spec/waivers/-', value: entry }]
    : [{ op: 'add' as const, path: '/spec/waivers', value: [entry] }];
};

// JSON patch removing the waiver at index i (test-guards the name so a
// concurrent reorder cannot delete the wrong entry).
export const removeWaiverPatch = (index: number, name: string) => [
  { op: 'test' as const, path: `/spec/waivers/${index}/name`, value: name },
  { op: 'remove' as const, path: `/spec/waivers/${index}` },
];

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
export const resultsHref = (status: string, profile?: string): string => {
  const params = new URLSearchParams();
  params.set('rowFilter-result-status', stripSurrogates(status));
  if (profile) {
    params.set('rowFilter-result-profile', stripSurrogates(profile));
  }
  return `/baseline-security/results?${params.toString()}`;
};
