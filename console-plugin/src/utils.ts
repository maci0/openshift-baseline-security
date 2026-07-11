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

// resultsCsv serializes check results to a CSV report. Deterministic column
// order; one header row. When waivers are provided, a waived column marks
// checks excluded from the score (FAIL + waiver only; a waived PASS still
// counts toward the score) so exports match Overview score math.
export const resultsCsv = (
  results: ComplianceCheckResult[],
  waivers?: Waiver[],
): string => {
  const header = ['name', 'title', 'status', 'severity', 'waived'];
  const rows = results.map((r) =>
    [
      r.metadata.name,
      checkTitle(r),
      r.status,
      r.severity,
      r.status === 'FAIL' && isWaived(r.metadata.name, waivers) ? 'true' : 'false',
    ]
      .map((c) => csvCell(String(c ?? '')))
      .join(','),
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

// HTML-escape untrusted text (waiver reasons, rule titles) for the report.
const esc = (s: string): string =>
  s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string);

// Build a printable, self-contained HTML compliance report from already-watched
// data: overall score, per-profile breakdown, and active waivers with
// attribution. All untrusted text is HTML-escaped (never interpreted as markup).
export const buildReportHtml = (baseline: ClusterBaseline, now: Date = new Date()): string => {
  const st = baseline.status ?? {};
  const score = st.score != null ? `${st.score} / 100` : 'Not scanned';
  const profileRows = [
    ...(st.profiles ?? []).map((p) => ({ name: (p.key ?? '').toUpperCase(), c: p })),
    ...(st.tailoredProfiles ?? []).map((p) => ({ name: `${p.name} (tailored)`, c: p })),
  ]
    .map(
      ({ name, c }) =>
        `<tr><td>${esc(name)}</td><td>${c.pass ?? 0}</td><td>${c.fail ?? 0}</td>` +
        `<td>${c.manual ?? 0}</td><td>${c.inconsistent ?? 0}</td><td>${c.waived ?? 0}</td></tr>`,
    )
    .join('');
  const activeWaivers = (baseline.spec.waivers ?? []).filter((w) => !waiverExpired(w, now));
  const waiverRows = activeWaivers
    .map(
      (w) =>
        `<tr><td>${esc(w.name)}</td><td>${esc(w.reason ?? '')}</td>` +
        `<td>${esc(w.requestedBy ?? '')}</td><td>${esc(w.approvedBy ?? '')}</td>` +
        `<td>${w.expiresAt ? esc(new Date(w.expiresAt).toLocaleDateString()) : ''}</td></tr>`,
    )
    .join('');
  return `<!doctype html><html><head><meta charset="utf-8"><title>Compliance report</title>
<style>body{font-family:sans-serif;margin:2rem;color:#151515}h1{margin-bottom:0}
table{border-collapse:collapse;margin:1rem 0;width:100%}th,td{border:1px solid #ccc;padding:4px 8px;text-align:left}
.muted{color:#666}</style></head><body>
<h1>Compliance report</h1>
<p class="muted">Generated ${esc(now.toISOString())} • last scan ${esc(st.lastScanTime ?? 'n/a')}</p>
<h2>Score: ${esc(score)}</h2>
<h3>Profiles</h3>
<table><thead><tr><th>Profile</th><th>Pass</th><th>Fail</th><th>Manual</th><th>Inconsistent</th><th>Waived</th></tr></thead>
<tbody>${profileRows || '<tr><td colspan="6" class="muted">No profiles</td></tr>'}</tbody></table>
<h3>Active waivers (${activeWaivers.length})</h3>
<table><thead><tr><th>Check</th><th>Reason</th><th>Requested by</th><th>Approved by</th><th>Expires</th></tr></thead>
<tbody>${waiverRows || '<tr><td colspan="5" class="muted">None</td></tr>'}</tbody></table>
</body></html>`;
};

// Loose 5-field cron validation for the schedule editor: five whitespace-
// separated fields of the allowed character set. The operator does the real
// parse; this just blocks obvious garbage before patching.
export const isValidCron = (s: string): boolean => {
  const fields = s.trim().split(/\s+/);
  return fields.length === 5 && fields.every((f) => /^[0-9*/,\-]+$/.test(f));
};

// JSON patch for spec.schedule (add when absent, replace when present).
export const schedulePatch = (hasSchedule: boolean, cron: string) =>
  hasSchedule
    ? [{ op: 'replace' as const, path: '/spec/schedule', value: cron }]
    : [{ op: 'add' as const, path: '/spec/schedule', value: cron }];

// JSON patch setting the batch-apply annotation on the ClusterBaseline, which
// the operator consumes to pause MachineConfigPools, apply the listed
// remediations, and resume so nodes reboot once. Adds the annotations map when
// absent (a nested add would 404).
export const batchApplyPatch = (hasAnnotations: boolean, names: string[]) => {
  const value = names.join(',');
  return hasAnnotations
    ? [
        {
          op: 'add' as const,
          path: '/metadata/annotations/baselinesecurity.io~1batch-apply',
          value,
        },
      ]
    : [
        {
          op: 'add' as const,
          path: '/metadata/annotations',
          value: { 'baselinesecurity.io/batch-apply': value },
        },
      ];
};

// JSON patch for spec.remediation.apply (Automatic|Manual).
export const remediationApplyPatch = (hasRemediation: boolean, automatic: boolean) => {
  const apply = automatic ? 'Automatic' : 'Manual';
  return hasRemediation
    ? [{ op: 'add' as const, path: '/spec/remediation/apply', value: apply }]
    : [{ op: 'add' as const, path: '/spec/remediation', value: { apply } }];
};

// A waiver is expired once its expiresAt is in the past; an expired waiver no
// longer excludes its check (matching the operator).
export const waiverExpired = (w: Waiver, now: Date = new Date()): boolean =>
  !!w.expiresAt && new Date(w.expiresAt).getTime() <= now.getTime();

// The waiver entry for a check name (regardless of expiry), or undefined.
export const findWaiver = (name: string, waivers?: Waiver[]): Waiver | undefined =>
  name ? waivers?.find((w) => w.name === name && !!w.name) : undefined;

// True when a check is actively waived (has a non-expired waiver), i.e. excluded
// from the score. Empty names never match. Expired waivers do not count.
export const isWaived = (name: string, waivers?: Waiver[], now: Date = new Date()): boolean => {
  const w = findWaiver(name, waivers);
  return !!w && !waiverExpired(w, now);
};

// Active waivers expiring within `withinMs` (not yet expired), for surfacing.
export const expiringWaivers = (
  waivers: Waiver[] | undefined,
  withinMs: number,
  now: Date = new Date(),
): Waiver[] =>
  (waivers ?? []).filter((w) => {
    if (!w.expiresAt) {
      return false;
    }
    const t = new Date(w.expiresAt).getTime();
    return t > now.getTime() && t <= now.getTime() + withinMs;
  });

// Filter-chip / deep-link status for a result. FAIL+waiver is "WAIVED" so the
// Results FAIL filter matches Overview fail counts (operator score math excludes
// waived fails from the Fail bucket). A waived PASS stays PASS (still scored).
export const resultFilterStatus = (
  r: Pick<ComplianceCheckResult, 'status'> & { metadata: { name: string } },
  waivers?: Waiver[],
): string =>
  r.status === 'FAIL' && isWaived(r.metadata.name, waivers) ? 'WAIVED' : r.status;

// JSON patch adding a waiver for a check. When the array is absent, create it;
// when it exists (including empty after the last remove), append with "/-".
// If the name is already waived, replace that entry (updates reason, avoids
// duplicate list-map keys from a double-click race). Empty names yield no ops.
export const addWaiverPatch = (waivers: Waiver[] | undefined | null, entry: Waiver) => {
  const name = entry.name;
  if (!name) {
    return [] as { op: 'add' | 'replace' | 'test'; path: string; value: unknown }[];
  }
  // Drop empty optional fields so the stored entry stays minimal.
  const clean: Waiver = { name };
  if (entry.reason) clean.reason = entry.reason;
  if (entry.requestedBy) clean.requestedBy = entry.requestedBy;
  if (entry.approvedBy) clean.approvedBy = entry.approvedBy;
  if (entry.expiresAt) clean.expiresAt = entry.expiresAt;
  if (entry.reviewBy) clean.reviewBy = entry.reviewBy;
  if (waivers != null) {
    const idx = waivers.findIndex((w) => w.name === name);
    if (idx >= 0) {
      return [
        { op: 'test' as const, path: `/spec/waivers/${idx}/name`, value: name },
        { op: 'replace' as const, path: `/spec/waivers/${idx}`, value: clean },
      ];
    }
    return [{ op: 'add' as const, path: '/spec/waivers/-', value: clean }];
  }
  return [{ op: 'add' as const, path: '/spec/waivers', value: [clean] }];
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
