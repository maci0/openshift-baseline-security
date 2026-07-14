// ClusterBaseline / TailoredProfile JSON patches (optimistic concurrency, fail-closed).
import { isValidCron } from './cron';
import { TAILORED_PROFILE_MAX_ITEMS, WAIVER_MAX_ITEMS, Waiver } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';

// Matches operator batchApplyAnnotation / batchMaxRemediations so the console
// and reconciler cannot drift on key or size.
export const BATCH_APPLY_ANNOTATION = 'baselinesecurity.openshift.io/batch-apply';
export const batchApplyMaxNames = 256;

// True when the batch-apply annotation names at least one remediation.
// Operator parity (batchRemediationNames / splitCSV): key presence alone is not
// enough; empty, whitespace, or comma-only values are not a batch request.
// Used so the Remediations UI does not stick on "in progress" for a stale empty key.
export const batchApplyRequested = (
  annotations?: Record<string, string> | null,
): boolean => {
  const raw = annotations?.[BATCH_APPLY_ANNOTATION];
  if (typeof raw !== 'string' || !raw) {
    return false;
  }
  for (const part of raw.split(',')) {
    if (part.trim()) {
      return true;
    }
  }
  return false;
};

// Optimistic concurrency test op prepended to ClusterBaseline JSON patches.
// Empty when resourceVersion is unknown (no guard rather than a false conflict).
export const resourceVersionTest = (resourceVersion?: string) =>
  resourceVersion
    ? [{ op: 'test' as const, path: '/metadata/resourceVersion', value: resourceVersion }]
    : [];

// Idempotent, optimistic JSON patch for binding a newly-created TailoredProfile.
// The resourceVersion guard prevents an absent-list add from replacing a list
// another admin created after this render. Invalid names (CRD MaxLength 51 /
// DNS-1123) yield no ops so admission is not the first failure mode.
export const tailoredProfileBindingPatch = (
  current: string[] | undefined,
  name: string,
  resourceVersion?: string,
) => {
  if (!isValidTailoredProfileName(name) || current?.includes(name)) return [];
  // CRD MaxItems=32: refuse growth past the bound (replace/duplicate already no-op above).
  if ((current?.length ?? 0) >= TAILORED_PROFILE_MAX_ITEMS) return [];
  const guard = resourceVersionTest(resourceVersion);
  return current != null
    ? [
        ...guard,
        { op: 'test' as const, path: '/spec/tailoredProfiles', value: current },
        { op: 'add' as const, path: '/spec/tailoredProfiles/-', value: name },
      ]
    : [...guard, { op: 'add' as const, path: '/spec/tailoredProfiles', value: [name] }];
};

// JSON patch for spec.schedule. Always uses `add` so missing, empty-string, and
// already-set values all succeed (RFC 6902 add creates or replaces an object
// member; matches remediationApplyPatch leaf handling for defaulted-absent
// fields). Invalid cron yields no ops so CRD/controller rejection is not the
// first failure mode. hasSchedule is retained for call-site compatibility.
export const schedulePatch = (_hasSchedule: boolean, cron: string) => {
  const value = cron.trim();
  if (!isValidCron(value)) {
    return [] as { op: 'add'; path: string; value: unknown }[];
  }
  return [{ op: 'add' as const, path: '/spec/schedule', value }];
};

// JSON patch setting the batch-apply annotation on the ClusterBaseline, which
// the operator consumes to pause MachineConfigPools, apply the listed
// remediations, and resume so nodes reboot once. Adds the annotations map when
// absent (a nested add would 404). Empty / invalid names yield no ops (matches
// operator skip of comma-only annotations). Cap matches batchMaxRemediations.
export const batchApplyPatch = (hasAnnotations: boolean, names: string[]) => {
  const seen = new Set<string>();
  const list: string[] = [];
  for (const raw of names) {
    const n = raw.trim();
    if (!n || !isValidK8sName(n) || seen.has(n)) continue;
    seen.add(n);
    list.push(n);
    if (list.length >= batchApplyMaxNames) break;
  }
  if (list.length === 0) {
    return [] as { op: 'add'; path: string; value: unknown }[];
  }
  const value = list.join(',');
  // JSON Pointer escapes "/" as "~1" in the nested annotation path.
  const annPath = `/metadata/annotations/${BATCH_APPLY_ANNOTATION.replace(/\//g, '~1')}`;
  return hasAnnotations
    ? [
        {
          op: 'add' as const,
          path: annPath,
          value,
        },
      ]
    : [
        {
          op: 'add' as const,
          path: '/metadata/annotations',
          value: { [BATCH_APPLY_ANNOTATION]: value },
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

// metav1.Time JSON is RFC3339. Date.parse alone is too loose (e.g. "March 1,
// 2026", locale MM/DD, calendar overflow like 2026-02-31) and would pass here
// only to 422 at the apiserver, or worse accept a browser-overflowed day.
// Require RFC3339 shape, a real calendar date, and a finite instant.
const rfc3339TimeRe =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
const isParseableTime = (s: string): boolean => {
  const trimmed = s.trim();
  const m = rfc3339TimeRe.exec(trimmed);
  if (!m) {
    return false;
  }
  const year = Number(m[1]);
  const month = Number(m[2]);
  const day = Number(m[3]);
  // UTC calendar check so 2026-02-31 cannot pass via Date overflow.
  const cal = new Date(Date.UTC(year, month - 1, day));
  if (
    cal.getUTCFullYear() !== year ||
    cal.getUTCMonth() !== month - 1 ||
    cal.getUTCDate() !== day
  ) {
    return false;
  }
  return !Number.isNaN(Date.parse(trimmed));
};

// JSON patch adding a waiver for a check. When the array is absent, create it;
// when it exists (including empty after the last remove), append with "/-".
// If the name is already waived, replace that entry (updates reason, avoids
// duplicate list-map keys from a double-click race). Empty or invalid names
// (not DNS-1123) yield no ops so CRD admission is not the first failure mode.
export const addWaiverPatch = (waivers: Waiver[] | undefined | null, entry: Waiver) => {
  const name = entry.name;
  // Trim optional text fields once: whitespace-only is empty; MaxLength is on
  // the stored value so padding cannot smuggle past the bound after a later trim.
  const reason = entry.reason?.trim() ?? '';
  const requestedBy = entry.requestedBy?.trim() ?? '';
  const approvedBy = entry.approvedBy?.trim() ?? '';
  // Match ClusterBaseline CRD bounds so over-long / malformed fields fail closed
  // here (empty ops) instead of only at apiserver admission.
  if (
    !isValidK8sName(name) ||
    reason.length > 1024 ||
    requestedBy.length > 253 ||
    approvedBy.length > 253 ||
    (entry.expiresAt != null && entry.expiresAt !== '' && !isParseableTime(entry.expiresAt)) ||
    (entry.reviewBy != null && entry.reviewBy !== '' && !isParseableTime(entry.reviewBy))
  ) {
    return [] as { op: 'add' | 'replace' | 'test'; path: string; value: unknown }[];
  }
  // Drop empty optional fields so the stored entry stays minimal.
  const clean: Waiver = { name };
  if (reason) clean.reason = reason;
  if (requestedBy) clean.requestedBy = requestedBy;
  if (approvedBy) clean.approvedBy = approvedBy;
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
    // CRD MaxItems=256: refuse a new entry past the bound (replace still allowed).
    if (waivers.length >= WAIVER_MAX_ITEMS) {
      return [] as { op: 'add' | 'replace' | 'test'; path: string; value: unknown }[];
    }
    return [{ op: 'add' as const, path: '/spec/waivers/-', value: clean }];
  }
  return [{ op: 'add' as const, path: '/spec/waivers', value: [clean] }];
};

// JSON patch removing the waiver at index i (test-guards the name so a
// concurrent reorder cannot delete the wrong entry). Invalid index / empty name
// yield no ops so a bad call site cannot emit a patch that always 404s.
export const removeWaiverPatch = (index: number, name: string) => {
  if (!Number.isInteger(index) || index < 0 || !name) {
    return [] as { op: 'test' | 'remove'; path: string; value?: unknown }[];
  }
  return [
    { op: 'test' as const, path: `/spec/waivers/${index}/name`, value: name },
    { op: 'remove' as const, path: `/spec/waivers/${index}` },
  ];
};

// JSON patch to trigger a Compliance Operator rescan. value must change each
// click so a re-rescan is observed when the annotation already exists.
// When metadata.annotations is missing, add the whole map (nested add fails).
export const rescanPatch = (
  hasAnnotations: boolean,
  value: string,
  resourceVersion?: string,
) => {
  // Empty/whitespace tokens are not observed as a change by CO and would make a
  // successful patch look like a rescan when nothing useful was written.
  const token = typeof value === 'string' ? value.trim() : '';
  if (!token) {
    return [] as { op: 'add' | 'test'; path: string; value: unknown }[];
  }
  // A nested add cannot erase siblings. Guard only whole-map creation, where a
  // concurrent writer could otherwise have its newly-created map replaced.
  const guard = !hasAnnotations ? resourceVersionTest(resourceVersion) : [];
  return hasAnnotations
    ? [
        ...guard,
        {
          op: 'add' as const,
          path: '/metadata/annotations/compliance.openshift.io~1rescan',
          value: token,
        },
      ]
    : [
        ...guard,
        {
          op: 'add' as const,
          path: '/metadata/annotations',
          value: { 'compliance.openshift.io/rescan': token },
        },
      ];
};
