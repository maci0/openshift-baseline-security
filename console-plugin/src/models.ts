// K8s types, GVKs, CRD-aligned constants, profile display metadata, and ownership
// selectors. Keep MaxItems / ProfileKey values in lockstep with the operator API.
import { K8sGroupVersionKind, K8sModel } from '@openshift-console/dynamic-plugin-sdk';

// ProfileKey matches ClusterBaselineSpec.profiles CRD enum. Keep in lockstep with
// the operator ProfileKey constants / AllProfileKeys and Profiles MaxItems=8.
export type ProfileKey =
  | 'cis'
  | 'pci-dss'
  | 'nist-moderate'
  | 'nist-high'
  | 'stig'
  | 'nerc-cip'
  | 'e8'
  | 'bsi';

export const PROFILE_KEYS: readonly ProfileKey[] = [
  'cis',
  'pci-dss',
  'nist-moderate',
  'nist-high',
  'stig',
  'nerc-cip',
  'e8',
  'bsi',
] as const;

// Display metadata for every known ProfileKey. Record<ProfileKey, ...> fails
// typecheck if a CRD enum value is missing here (lockstep with PROFILE_KEYS).
// title/description are i18n source keys (English); pass through t() at render.
export const PROFILE_INFO: Record<ProfileKey, { title: string; description: string }> = {
  cis: { title: 'CIS', description: 'CIS Red Hat OpenShift Container Platform Benchmark' },
  'pci-dss': { title: 'PCI-DSS', description: 'Payment Card Industry Data Security Standard' },
  'nist-moderate': { title: 'NIST 800-53 Moderate', description: 'FedRAMP Moderate impact baseline' },
  'nist-high': { title: 'NIST 800-53 High', description: 'FedRAMP High impact baseline' },
  stig: {
    title: 'DISA STIG',
    description: 'Defense Information Systems Agency Security Technical Implementation Guide',
  },
  'nerc-cip': {
    title: 'NERC CIP',
    description: 'North American Electric Reliability Corporation Critical Infrastructure Protection',
  },
  e8: { title: 'ACSC Essential Eight', description: 'Australian Cyber Security Centre Essential Eight' },
  bsi: {
    title: 'BSI',
    description: 'German Federal Office for Information Security IT-Grundschutz',
  },
};

// O(1) membership for ProfileKey enum (lockstep with PROFILE_KEYS / CRD).
const PROFILE_KEY_SET: ReadonlySet<string> = new Set(PROFILE_KEYS);

// True when key is a known built-in ProfileKey (CRD enum). Shared by profileTitle
// and toggledProfiles so membership checks cannot drift.
export const isProfileKey = (key: string): key is ProfileKey => PROFILE_KEY_SET.has(key);

// Human title for a built-in profile key (i18n source string). Unknown keys fall
// back to uppercased key so Overview/filter stay readable for future enums.
export const profileTitle = (key: string): string => {
  if (isProfileKey(key)) {
    return PROFILE_INFO[key].title;
  }
  // CR status.profiles[].key is not runtime type-checked; coerce so a tampered
  // non-string key cannot throw on .toUpperCase.
  return String(key ?? '').toUpperCase();
};

// CRD list MaxItems for ClusterBaselineSpec (client fail-closed before admission).
export const PROFILE_MAX_ITEMS = 8;
export const TAILORED_PROFILE_MAX_ITEMS = 32;
export const WAIVER_MAX_ITEMS = 256;

// Matches operator DefaultScanSchedule / CRD default for spec.schedule when empty.
export const DEFAULT_SCAN_SCHEDULE = '0 1 * * *';

// Compliance Operator install namespace (product default on OpenShift). Single
// source for watches, access reviews, create payloads, and console deep-links.
export const COMPLIANCE_NAMESPACE = 'openshift-compliance';

export const ClusterBaselineGVK: K8sGroupVersionKind = {
  group: 'baselinesecurity.openshift.io',
  version: 'v1alpha1',
  kind: 'ClusterBaseline',
};

export const ComplianceCheckResultGVK: K8sGroupVersionKind = {
  group: 'compliance.openshift.io',
  version: 'v1alpha1',
  kind: 'ComplianceCheckResult',
};

export const ComplianceScanGVK: K8sGroupVersionKind = {
  group: 'compliance.openshift.io',
  version: 'v1alpha1',
  kind: 'ComplianceScan',
};

export const ComplianceRemediationGVK: K8sGroupVersionKind = {
  group: 'compliance.openshift.io',
  version: 'v1alpha1',
  kind: 'ComplianceRemediation',
};

const model = (gvk: K8sGroupVersionKind, plural: string, namespaced: boolean): K8sModel => ({
  apiGroup: gvk.group,
  apiVersion: gvk.version,
  kind: gvk.kind,
  plural,
  abbr: '',
  label: gvk.kind,
  labelPlural: plural,
  id: '',
  namespaced,
});

export const TailoredProfileGVK: K8sGroupVersionKind = {
  group: 'compliance.openshift.io',
  version: 'v1alpha1',
  kind: 'TailoredProfile',
};

// Compliance Operator Profile: a built-in benchmark a TailoredProfile extends.
// The object carries a top-level `rules: []string` (the rule names in the
// profile), which populates the tailored-profile "disable rules" selection.
export const ProfileGVK: K8sGroupVersionKind = {
  group: 'compliance.openshift.io',
  version: 'v1alpha1',
  kind: 'Profile',
};

export const ClusterBaselineModel = model(ClusterBaselineGVK, 'clusterbaselines', false);
export const ComplianceScanModel = model(ComplianceScanGVK, 'compliancescans', true);
export const ComplianceRemediationModel = model(ComplianceRemediationGVK, 'complianceremediations', true);
export const TailoredProfileModel = model(TailoredProfileGVK, 'tailoredprofiles', true);
export const ProfileModel = model(ProfileGVK, 'profiles', true);

// Compliance Operator Profile object (subset): name + the rule names it contains.
export type ComplianceProfile = {
  metadata?: { name?: string };
  rules?: string[];
};

export type CheckStatus =
  | 'PASS'
  | 'FAIL'
  | 'INFO'
  | 'MANUAL'
  | 'ERROR'
  | 'INCONSISTENT'
  | 'SKIP'
  | 'NOT-APPLICABLE';

export type ComplianceCheckResult = {
  metadata: {
    name: string;
    namespace: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
  };
  status: CheckStatus;
  // Optional: CO usually sets .severity; when absent, use the
  // compliance.openshift.io/check-severity label (see checkSeverity).
  severity?: 'unknown' | 'info' | 'low' | 'medium' | 'high' | string;
  description?: string;
  instructions?: string;
};

// The rendered object a remediation applies (a MachineConfig for node
// remediations, or another cluster resource); shown in the detail view.
export type RemediationObject = {
  apiVersion?: string;
  kind?: string;
  metadata?: { name?: string };
  [k: string]: unknown;
};

export type ComplianceRemediation = {
  metadata: {
    name: string;
    namespace: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    resourceVersion?: string;
  };
  spec: { apply: boolean; current?: { object?: RemediationObject } };
  status?: {
    applicationState?: 'Applied' | 'NotApplied' | 'Error' | 'Outdated' | 'MissingDependencies';
    // CO status.errorMessage when applicationState is Error (or sometimes
    // MissingDependencies); shown in the Remediations table for diagnosis.
    errorMessage?: string;
  };
};

// Score under the scoring mode active when the operator wrote the point.
// Flat <-> SeverityWeighted flips do not rewrite prior points under the new
// formula; on the next completed scan under the new mode the operator clears
// overall and per-profile rings and appends a fresh point so charts never mix
// modes (ADR-008).
export type ScoreSnapshot = { time: string; score: number };

export type ResultCounts = {
  pass: number;
  fail: number;
  manual: number;
  info: number;
  error: number;
  inconsistent: number;
  waived: number;
  notApplicable: number;
};

export type Waiver = {
  name: string;
  reason?: string;
  requestedBy?: string;
  approvedBy?: string;
  expiresAt?: string;
  reviewBy?: string;
};

export type ProfileStatus = ResultCounts & {
  key: string;
  // Optional on the CR (omitempty); absent until the first aggregate fill.
  profileNames?: string[];
  history?: ScoreSnapshot[];
};

export type TailoredProfileStatus = ResultCounts & { name: string; history?: ScoreSnapshot[] };

export type ClusterBaseline = {
  metadata: { name: string; resourceVersion?: string; annotations?: Record<string, string> };
  spec: {
    // ProfileKey when from a valid CR; string retained so partial/hand-edited CRs still typecheck.
    profiles: ProfileKey[] | string[];
    tailoredProfiles?: string[];
    schedule?: string;
    installComplianceOperator?: 'Automatic' | 'Manual';
    // OLM CatalogSource name for the compliance-operator package (default
    // redhat-operators; override for OKD / disconnected). Matches CRD
    // spec.complianceCatalogSource.
    complianceCatalogSource?: string;
    console?: { managementState?: 'Managed' | 'Removed' };
    remediation?: { apply?: 'Automatic' | 'Manual' };
    scoring?: { mode?: 'Flat' | 'SeverityWeighted' };
    waivers?: Waiver[];
  };
  status?: {
    score?: number;
    lastScanTime?: string;
    nextScanTime?: string;
    complianceOperatorVersion?: string;
    profiles?: ProfileStatus[];
    tailoredProfiles?: TailoredProfileStatus[];
    conditions?: {
      type: string;
      status: string;
      reason?: string;
      message?: string;
      lastTransitionTime?: string;
      observedGeneration?: number;
    }[];
    history?: ScoreSnapshot[];
    newlyFailed?: string[];
    fixed?: string[];
    // Owned/driven resources for must-gather (status.relatedObjects).
    relatedObjects?: {
      group?: string;
      resource: string;
      name: string;
      namespace?: string;
    }[];
    // Operator-internal scan-diff bookkeeping (not a consumer contract; may
    // change in 0.x). Overview only treats presence as "a prior scan exists"
    // when history is still thin; prefer newlyFailed/fixed for regressions.
    diffBaseScanTime?: string;
    remediationBatch?: {
      phase: string;
      pools?: string[];
      remediations?: string[];
      startedAt: string;
      pauseOwner?: string;
    };
  };
};

const SUITE_LABEL = 'compliance.openshift.io/suite';

// CO label on scans / check results / remediations naming the scan that produced
// the object. Shared by nodeScanPool and isNodeRemediation ("…-node-<pool>").
export const SCAN_NAME_LABEL = 'compliance.openshift.io/scan-name';

// MachineConfigPool suffix from a CO scan-name ("<profile>-node-<pool>"), or
// null when not a node scan / empty pool. lastIndex so tailored names that
// contain "-node-" still resolve to the final pool segment (operator parity).
// Does not validate DNS-1123; isNodeRemediation applies isValidK8sName so batch
// eligibility matches operator validMCPPoolName.
export const nodePoolFromScanName = (scan: string): string | null => {
  const i = scan.lastIndexOf('-node-');
  return i < 0 ? null : scan.slice(i + '-node-'.length) || null;
};

/**
 * Display key encoded in a CO object's suite label for built-in profiles
 * ("baseline-<key>"). Tailored suites ("baseline-tp-<name>") are excluded so
 * callers use suiteTailoredName / suiteFilterKey instead. Returns undefined
 * when not a built-in baseline suite.
 */
export const suiteProfileKey = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const suite = labels?.[SUITE_LABEL];
  if (!suite?.startsWith('baseline-') || suite.startsWith('baseline-tp-')) {
    return undefined;
  }
  const key = suite.slice('baseline-'.length);
  return key || undefined;
};

/** TailoredProfile name for a "baseline-tp-<name>" suite, else undefined. */
export const suiteTailoredName = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const suite = labels?.[SUITE_LABEL];
  if (!suite?.startsWith('baseline-tp-')) {
    return undefined;
  }
  // Reject empty name ("baseline-tp-") to match the operator's tailoredNameFromSuite.
  const name = suite.slice('baseline-tp-'.length);
  return name || undefined;
};

/**
 * Row-filter / deep-link id for a suite label: built-in profile key, or
 * "tp-<name>" for tailored (matches Overview resultsHref and e2e filters).
 * Parses the suite label once (hot path: Results filters over thousands of rows).
 */
export const suiteFilterKey = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const suite = labels?.[SUITE_LABEL];
  if (!suite?.startsWith('baseline-')) {
    return undefined;
  }
  if (suite.startsWith('baseline-tp-')) {
    const name = suite.slice('baseline-tp-'.length);
    return name ? `tp-${name}` : undefined;
  }
  const key = suite.slice('baseline-'.length);
  return key || undefined;
};

/**
 * Human title for a suiteFilterKey id: built-in keys use profileTitle (i18n
 * source string; pass through t() at render), tailored "tp-<name>" drops the
 * prefix. Shared by Results chips/column and checkProfileLabel so display cannot
 * drift between filter keys and label-derived titles.
 */
export const suiteFilterKeyTitle = (key: string): string =>
  key.startsWith('tp-') ? key.slice(3) : profileTitle(key);

/**
 * Suite label values this baseline owns ("baseline-<key>", "baseline-tp-<name>").
 * Used as a label selector so list watches do not pull foreign CO objects.
 */
export const ownedSuiteLabels = (
  profiles: readonly string[] | undefined,
  tailoredProfiles: readonly string[] | undefined,
): string[] => {
  // Pre-size for typical multi-profile + tailored baselines (watch selector rebuild).
  const out: string[] = new Array((profiles?.length ?? 0) + (tailoredProfiles?.length ?? 0));
  let n = 0;
  for (const p of profiles ?? []) {
    if (p) {
      out[n++] = `baseline-${p}`;
    }
  }
  for (const name of tailoredProfiles ?? []) {
    if (name) {
      out[n++] = `baseline-tp-${name}`;
    }
  }
  out.length = n;
  return out;
};

/**
 * Label selector for CO list watches scoped to this baseline's suites.
 * Shared by CompliancePage (scans/results) and RemediationsTab so the
 * matchExpressions shape cannot drift. Undefined when nothing is selected
 * (callers should skip the watch rather than list the whole namespace).
 */
export const ownedSuiteSelector = (
  profiles: readonly string[] | undefined,
  tailoredProfiles: readonly string[] | undefined,
): { matchExpressions: { key: string; operator: 'In'; values: string[] }[] } | undefined => {
  const values = ownedSuiteLabels(profiles, tailoredProfiles);
  if (!values.length) {
    return undefined;
  }
  return {
    matchExpressions: [{ key: SUITE_LABEL, operator: 'In', values }],
  };
};

/**
 * Human label for a check's owning profile, for the Results Profile column:
 * built-in keys use profileTitle (i18n source string; pass through t() at render),
 * a tailored profile by its plain name, and an em dash when unknown.
 * Parses the suite label once via suiteFilterKey (hot path: report FAIL rows).
 */
export const checkProfileLabel = (labels: Record<string, string> | undefined): string => {
  const key = suiteFilterKey(labels);
  return key ? suiteFilterKeyTitle(key) : '—';
};

/** Profile / tailored name list: array (includes) or Set (has) for O(1) hot paths. */
export type NameSet = readonly string[] | ReadonlySet<string>;

const nameIn = (list: NameSet | undefined, name: string): boolean => {
  if (!list) {
    return false;
  }
  if (list instanceof Set) {
    return list.has(name);
  }
  return (list as readonly string[]).includes(name);
};

/**
 * True when a CO object belongs to this baseline: a built-in profile suite for
 * a selected profile, or a tailored suite for a bound TailoredProfile.
 * Callers that filter thousands of results should pass Set instances so
 * membership is O(1) per check instead of a linear includes scan.
 * Parses the suite label once (avoids dual suiteTailoredName + suiteProfileKey).
 */
export const isOwnedByBaseline = (
  labels: Record<string, string> | undefined,
  profiles: NameSet | undefined,
  tailoredProfiles?: NameSet,
): boolean => {
  const suite = labels?.[SUITE_LABEL];
  if (!suite?.startsWith('baseline-')) {
    return false;
  }
  if (suite.startsWith('baseline-tp-')) {
    const name = suite.slice('baseline-tp-'.length);
    return !!name && nameIn(tailoredProfiles, name);
  }
  const key = suite.slice('baseline-'.length);
  return !!key && nameIn(profiles, key);
};

/**
 * Filter a list of CO objects down to those owned by this baseline. Builds the
 * profile / tailored membership Sets once so isOwnedByBaseline stays O(1) per
 * object when filtering thousands of scans / check results / remediations.
 */
export const filterOwnedByBaseline = <T extends { metadata: { labels?: Record<string, string> } }>(
  list: T[] | undefined,
  profiles: readonly string[] | undefined,
  tailoredProfiles: readonly string[] | undefined,
): T[] => {
  const p = new Set(profiles ?? []);
  const t = new Set(tailoredProfiles ?? []);
  return (list ?? []).filter((r) => isOwnedByBaseline(r.metadata.labels, p, t));
};
