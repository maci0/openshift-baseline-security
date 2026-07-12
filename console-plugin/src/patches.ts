import { isValidCron } from './cron';
import { TAILORED_PROFILE_MAX_ITEMS, WAIVER_MAX_ITEMS, Waiver } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';

// Matches operator batchMaxRemediations so the console never sets an
// annotation the reconciler will reject for size.
const batchApplyMaxNames = 256;

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

// True when s parses as a date (metav1.Time-shaped ISO). Empty is handled by
// callers; non-empty garbage must fail closed before apiserver admission.
const isParseableTime = (s: string): boolean => !Number.isNaN(Date.parse(s));

// JSON patch adding a waiver for a check. When the array is absent, create it;
// when it exists (including empty after the last remove), append with "/-".
// If the name is already waived, replace that entry (updates reason, avoids
// duplicate list-map keys from a double-click race). Empty or invalid names
// (not DNS-1123) yield no ops so CRD admission is not the first failure mode.
export const addWaiverPatch = (waivers: Waiver[] | undefined | null, entry: Waiver) => {
  const name = entry.name;
  // Match ClusterBaseline CRD bounds so over-long / malformed fields fail closed
  // here (empty ops) instead of only at apiserver admission.
  if (
    !isValidK8sName(name) ||
    (entry.reason != null && entry.reason.length > 1024) ||
    (entry.requestedBy != null && entry.requestedBy.length > 253) ||
    (entry.approvedBy != null && entry.approvedBy.length > 253) ||
    (entry.expiresAt != null && entry.expiresAt !== '' && !isParseableTime(entry.expiresAt)) ||
    (entry.reviewBy != null && entry.reviewBy !== '' && !isParseableTime(entry.reviewBy))
  ) {
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
    // CRD MaxItems=256: refuse a new entry past the bound (replace still allowed).
    if (waivers.length >= WAIVER_MAX_ITEMS) {
      return [] as { op: 'add' | 'replace' | 'test'; path: string; value: unknown }[];
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
export const rescanPatch = (
  hasAnnotations: boolean,
  value: string,
  resourceVersion?: string,
) => {
  // A nested add cannot erase siblings. Guard only whole-map creation, where a
  // concurrent writer could otherwise have its newly-created map replaced.
  const guard = !hasAnnotations ? resourceVersionTest(resourceVersion) : [];
  return hasAnnotations
    ? [
        ...guard,
        {
          op: 'add' as const,
          path: '/metadata/annotations/compliance.openshift.io~1rescan',
          value,
        },
      ]
    : [
        ...guard,
        {
          op: 'add' as const,
          path: '/metadata/annotations',
          value: { 'compliance.openshift.io/rescan': value },
        },
      ];
};
