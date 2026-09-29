// ClusterBaseline / TailoredProfile JSON patches (optimistic concurrency, fail-closed).
import { isValidCron } from './cron';
import { TAILORED_PROFILE_MAX_ITEMS, WAIVER_MAX_ITEMS, Waiver } from './models';
import { isValidK8sName, isValidTailoredProfileName } from './names';
import { isString, stripControlAndFormat, stripInvisibleText } from './parse';
import { codePointLength, trimGoSpace } from './text';

// One RFC 6902 operation emitted by the patch builders in this module. value
// is the JSON payload written at path; tests assert exact shapes.
export interface PatchOp {
  op: 'add' | 'replace' | 'test' | 'remove';
  path: string;
  value?: unknown;
}

// Matches operator batchApplyAnnotation / batchMaxRemediations so the console
// and reconciler cannot drift on key or size.
const BATCH_APPLY_ANNOTATION = 'baselinesecurity.openshift.io/batch-apply';
export const batchApplyMaxNames = 256;

// True when the batch-apply annotation names at least one remediation.
// Operator parity (batchRemediationNames / splitCSV): key presence alone is not
// enough; empty, whitespace, or comma-only values are not a batch request.
// Used so the Remediations UI does not stick on "in progress" for a stale empty key.
export const batchApplyRequested = (
  annotations?: Record<string, string> | null,
): boolean => {
  const raw = annotations?.[BATCH_APPLY_ANNOTATION];
  if (!isString(raw) || !raw) {
    return false;
  }
  for (const part of raw.split(',')) {
    if (trimGoSpace(part)) {
      return true;
    }
  }
  return false;
};

// Optimistic concurrency test op prepended to ClusterBaseline JSON patches.
// Empty when resourceVersion is unknown (no guard rather than a false conflict).
export const resourceVersionTest = (resourceVersion?: string): PatchOp[] =>
  resourceVersion
    ? [{ op: 'test', path: '/metadata/resourceVersion', value: resourceVersion }]
    : [];

// Idempotent, optimistic JSON patch for binding a newly-created TailoredProfile.
// The resourceVersion guard prevents an absent-list add from replacing a list
// another admin created after this render. Invalid names (CRD MaxLength 51 /
// DNS-1123) yield no ops so admission is not the first failure mode.
export const tailoredProfileBindingPatch = (
  current: string[] | undefined,
  name: string,
  resourceVersion?: string,
): PatchOp[] => {
  if (!isValidTailoredProfileName(name) || current?.includes(name)) return [];
  // CRD MaxItems=32: refuse growth past the bound (replace/duplicate already no-op above).
  if ((current?.length ?? 0) >= TAILORED_PROFILE_MAX_ITEMS) return [];
  const guard = resourceVersionTest(resourceVersion);
  return current != null
    ? [
        ...guard,
        { op: 'test', path: '/spec/tailoredProfiles', value: current },
        { op: 'add', path: '/spec/tailoredProfiles/-', value: name },
      ]
    : [...guard, { op: 'add', path: '/spec/tailoredProfiles', value: [name] }];
};

// JSON patch for spec.schedule. Always uses `add` so missing, empty-string, and
// already-set values all succeed (RFC 6902 add creates or replaces an object
// member; matches remediationApplyPatch leaf handling for defaulted-absent
// fields). Invalid cron yields no ops so CRD/controller rejection is not the
// first failure mode. trimGoSpace, not trim: the outer separators are exactly the
// ones the operator's strings.Fields would drop, so the value stored is the one
// the operator sees.
export const schedulePatch = (cron: string): PatchOp[] => {
  const value = trimGoSpace(cron);
  if (!isValidCron(value)) {
    return [];
  }
  return [{ op: 'add', path: '/spec/schedule', value }];
};

// JSON patch setting the batch-apply annotation on the ClusterBaseline, which
// the operator consumes to pause MachineConfigPools, apply the listed
// remediations, and resume so nodes reboot once. Adds the annotations map when
// absent (a nested add would 404). Empty / invalid names yield no ops (matches
// operator skip of comma-only annotations). Cap matches batchMaxRemediations.
//
// Every shape is resourceVersion-guarded, not just the whole-map one. The
// annotation is a one-shot request the operator consumes and clears, so a
// resubmit built from a stale read can land after a newer batch already ran:
// the operator would pause the pools and re-apply remediations a second time.
// The guard turns that into a 409 the caller surfaces, which is the only
// thing that keeps "apply twice" from meaning "two reboots".
export const batchApplyPatch = (
  hasAnnotations: boolean,
  names: string[],
  resourceVersion?: string,
): PatchOp[] => {
  const seen = new Set<string>();
  const list: string[] = [];
  for (const raw of names) {
    const n = trimGoSpace(raw);
    if (!n || !isValidK8sName(n) || seen.has(n)) continue;
    seen.add(n);
    list.push(n);
    if (list.length >= batchApplyMaxNames) break;
  }
  if (list.length === 0) {
    return [];
  }
  const value = list.join(',');
  // JSON Pointer escapes "/" as "~1" in the nested annotation path.
  const annPath = `/metadata/annotations/${BATCH_APPLY_ANNOTATION.replace(/\//g, '~1')}`;
  const guard = resourceVersionTest(resourceVersion);
  return hasAnnotations
    ? [
        ...guard,
        { op: 'add', path: annPath, value },
      ]
    : [
        ...guard,
        {
          op: 'add',
          path: '/metadata/annotations',
          value: { [BATCH_APPLY_ANNOTATION]: value },
        },
      ];
};

// JSON patch for spec.remediation.apply (Automatic|Manual).
export const remediationApplyPatch = (hasRemediation: boolean, automatic: boolean): PatchOp[] => {
  const apply = automatic ? 'Automatic' : 'Manual';
  return hasRemediation
    ? [{ op: 'add', path: '/spec/remediation/apply', value: apply }]
    : [{ op: 'add', path: '/spec/remediation', value: { apply } }];
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
  const hour = Number(m[4]);
  const minute = Number(m[5]);
  const second = Number(m[6]);
  // Go RFC3339 (metav1.Time) rejects hour 24 / minute 60 / leap-second 60.
  // Date.parse accepts T24:00:00Z as next-day midnight, which would 422 at
  // admission after the UI claimed the waiver saved.
  if (hour > 23 || minute > 59 || second > 59) {
    return false;
  }
  // UTC calendar check so 2026-02-31 cannot pass via Date overflow.
  // setUTCFullYear on the epoch, not Date.UTC: Date.UTC applies the legacy
  // two-digit-year rule, reading a year 0000-0099 (four digits, and accepted by
  // the apiserver's RFC3339 parser) as 1900-0099.
  const cal = new Date(0);
  cal.setUTCFullYear(year, month - 1, day);
  if (
    cal.getUTCFullYear() !== year ||
    cal.getUTCMonth() !== month - 1 ||
    cal.getUTCDate() !== day
  ) {
    return false;
  }
  return !Number.isNaN(Date.parse(trimmed));
};

// ClusterBaseline CRD bounds for waiver text fields, in the CRD's own unit:
// Unicode code points (codePointLength), which is what the API server counts
// for maxLength. Shared by the patch validator and the waive form's maxLength
// attributes so the widget is never looser than the validator: the DOM
// attribute counts UTF-16 code units, so every astral character counts double
// and the field can be stricter than the bound, never more permissive.
export const WAIVER_REASON_MAX_LEN = 1024;
export const WAIVER_ATTRIBUTION_MAX_LEN = 253;

// JSON patch adding a waiver for a check. When the array is absent, create it;
// when it exists (including empty after the last remove), append with "/-".
// Append tests the current list so a retry against a stale snapshot 409s
// instead of adding a second row (CRD listType=map is a merge key, not
// uniqueness admission). If the name is already waived, replace that entry
// (updates reason, avoids duplicate list-map keys from a double-click race).
// Empty or invalid names (not DNS-1123) yield no ops so CRD admission is not
// the first failure mode.
//
// Every shape carries the resourceVersion test, because the create shape
// writes /spec/waivers itself: RFC 6902 add on a member REPLACES it, so a
// second submit that still reads the list as absent (the watch has not
// delivered the first write, or a retried request reuses its snapshot) would
// overwrite the whole list and drop the waiver the first submit stored. The
// test is emitted here rather than left to a call site so the builder is safe
// on its own.
export const addWaiverPatch = (
  waivers: Waiver[] | undefined | null,
  entry: Waiver,
  resourceVersion?: string,
): PatchOp[] => {
  const name = entry.name;
  // Trim optional text fields once: whitespace-only is empty; MaxLength is on
  // the stored value so padding cannot smuggle past the bound after a later trim.
  // Strip controls and BIDI/zero-width marks so audit names cannot spoof another
  // identity in the UI, CSV, or printable report. The reason is prose, not an
  // identity, so it keeps its ZWJ/ZWNJ: those are script content (emoji
  // sequences, Arabic/Persian compounds) and dropping them would store text the
  // user never typed. The two attribution fields keep the full strip, where a
  // leading joiner in front of "admin" is exactly the spoof to refuse.
  const reason = stripInvisibleText(entry.reason?.trim() ?? '');
  const requestedBy = stripControlAndFormat(entry.requestedBy?.trim() ?? '');
  const approvedBy = stripControlAndFormat(entry.approvedBy?.trim() ?? '');
  // Match ClusterBaseline CRD bounds so over-long / malformed fields fail closed
  // here (empty ops) instead of only at apiserver admission.
  if (
    !isValidK8sName(name) ||
    codePointLength(reason) > WAIVER_REASON_MAX_LEN ||
    codePointLength(requestedBy) > WAIVER_ATTRIBUTION_MAX_LEN ||
    codePointLength(approvedBy) > WAIVER_ATTRIBUTION_MAX_LEN ||
    (entry.expiresAt != null && entry.expiresAt !== '' && !isParseableTime(entry.expiresAt)) ||
    (entry.reviewBy != null && entry.reviewBy !== '' && !isParseableTime(entry.reviewBy))
  ) {
    return [];
  }
  // Drop empty optional fields so the stored entry stays minimal.
  const clean: Waiver = { name };
  if (reason) clean.reason = reason;
  if (requestedBy) clean.requestedBy = requestedBy;
  if (approvedBy) clean.approvedBy = approvedBy;
  if (entry.expiresAt) clean.expiresAt = entry.expiresAt;
  if (entry.reviewBy) clean.reviewBy = entry.reviewBy;
  const guard = resourceVersionTest(resourceVersion);
  if (waivers != null) {
    const idx = waivers.findIndex((w) => w.name === name);
    if (idx >= 0) {
      return [
        ...guard,
        { op: 'test', path: `/spec/waivers/${idx}/name`, value: name },
        { op: 'replace', path: `/spec/waivers/${idx}`, value: clean },
      ];
    }
    // CRD MaxItems=256: refuse a new entry past the bound (replace still allowed).
    if (waivers.length >= WAIVER_MAX_ITEMS) {
      return [];
    }
    // Same list-test as tailoredProfileBindingPatch: two Waive clicks that both
    // saw the pre-add list cannot both append. The test 409s; the caller re-reads
    // and the replace path above then updates in place.
    return [
      ...guard,
      { op: 'test', path: '/spec/waivers', value: waivers },
      { op: 'add', path: '/spec/waivers/-', value: clean },
    ];
  }
  return [...guard, { op: 'add', path: '/spec/waivers', value: [clean] }];
};

// JSON patch removing the waiver at index i (test-guards the name so a
// concurrent reorder cannot delete the wrong entry). Invalid index / empty name
// yield no ops so a bad call site cannot emit a patch that always 404s.
export const removeWaiverPatch = (
  index: number,
  name: string,
  resourceVersion?: string,
): PatchOp[] => {
  if (!Number.isInteger(index) || index < 0 || !name) {
    return [];
  }
  return [
    ...resourceVersionTest(resourceVersion),
    { op: 'test', path: `/spec/waivers/${index}/name`, value: name },
    { op: 'remove', path: `/spec/waivers/${index}` },
  ];
};

// Value for the rescan annotation on one click. CO watches the ComplianceScan
// annotation for a CHANGE, so the token must differ from whatever the scan
// already carries. A bare per-page-load counter restarts at 1 after a reload or
// a tab switch back to the plugin, so the first rescan of the new session would
// rewrite the same value the apiserver already stores, CO would not observe a
// change, no scan would start, and the UI would still report "Rescan started".
// The wall-clock prefix makes a token unique across page loads; the sequence
// disambiguates two clicks inside the same millisecond.
export const rescanToken = (nowMs: number, seq: number): string =>
  `${nowMs}-${seq}`;

// JSON patch to trigger a Compliance Operator rescan. value must change each
// click so a re-rescan is observed when the annotation already exists.
// When metadata.annotations is missing, add the whole map (nested add fails).
export const rescanPatch = (
  hasAnnotations: boolean,
  value: string,
  resourceVersion?: string,
): PatchOp[] => {
  // Empty/whitespace tokens are not observed as a change by CO and would make a
  // successful patch look like a rescan when nothing useful was written.
  const token = isString(value) ? value.trim() : '';
  if (!token) {
    return [];
  }
  // A nested add cannot erase siblings. Guard only whole-map creation, where a
  // concurrent writer could otherwise have its newly-created map replaced.
  const guard = !hasAnnotations ? resourceVersionTest(resourceVersion) : [];
  return hasAnnotations
    ? [
        ...guard,
        { op: 'add', path: '/metadata/annotations/compliance.openshift.io~1rescan', value: token },
      ]
    : [
        ...guard,
        { op: 'add', path: '/metadata/annotations', value: { 'compliance.openshift.io/rescan': token } },
      ];
};
