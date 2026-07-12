import { K8sGroupVersionKind, K8sModel } from '@openshift-console/dynamic-plugin-sdk';

export const ClusterBaselineGVK: K8sGroupVersionKind = {
  group: 'baselinesecurity.io',
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

export const ClusterBaselineModel = model(ClusterBaselineGVK, 'clusterbaselines', false);
export const ComplianceScanModel = model(ComplianceScanGVK, 'compliancescans', true);
export const ComplianceRemediationModel = model(ComplianceRemediationGVK, 'complianceremediations', true);
export const TailoredProfileModel = model(TailoredProfileGVK, 'tailoredprofiles', true);

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
  severity: 'unknown' | 'info' | 'low' | 'medium' | 'high';
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
  metadata: { name: string; namespace: string; labels?: Record<string, string> };
  spec: { apply: boolean; current?: { object?: RemediationObject } };
  status?: {
    applicationState?: 'Applied' | 'NotApplied' | 'Error' | 'Outdated' | 'MissingDependencies';
  };
};

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
  profileNames: string[];
  history?: ScoreSnapshot[];
};

export type TailoredProfileStatus = ResultCounts & { name: string; history?: ScoreSnapshot[] };

export type ClusterBaseline = {
  metadata: { name: string; resourceVersion?: string; annotations?: Record<string, string> };
  spec: {
    profiles: string[];
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
    conditions?: { type: string; status: string; reason?: string; message?: string }[];
    history?: ScoreSnapshot[];
    newlyFailed?: string[];
    fixed?: string[];
    // Set when a prior completed scan exists for regression diff (operator
    // internal bookkeeping exposed on the CR). Used by Overview empty-state
    // copy so a second scan with a thin history ring is not "no prior scan".
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

/**
 * Display key encoded in a CO object's suite label for built-in profiles
 * ("baseline-<key>"). Tailored suites ("baseline-tp-<name>") are excluded so
 * callers use suiteTailoredName / suiteFilterKey instead. Returns undefined
 * when not a built-in baseline suite.
 */
export const suiteProfileKey = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const suite = labels?.['compliance.openshift.io/suite'];
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
  const suite = labels?.['compliance.openshift.io/suite'];
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
 */
export const suiteFilterKey = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const tailored = suiteTailoredName(labels);
  if (tailored !== undefined) {
    return `tp-${tailored}`;
  }
  return suiteProfileKey(labels);
};

/**
 * Human label for a check's owning profile, for the Results Profile column:
 * built-in benchmark keys uppercased (CIS, PCI-DSS) to match the Overview cards,
 * a tailored profile by its plain name, and an em dash when unknown.
 */
export const checkProfileLabel = (labels: Record<string, string> | undefined): string => {
  const tailored = suiteTailoredName(labels);
  if (tailored !== undefined) {
    return tailored;
  }
  const key = suiteProfileKey(labels);
  return key ? key.toUpperCase() : '—';
};

/**
 * True when a CO object belongs to this baseline: a built-in profile suite for
 * a selected profile, or a tailored suite for a bound TailoredProfile.
 */
export const isOwnedByBaseline = (
  labels: Record<string, string> | undefined,
  profiles: string[] | undefined,
  tailoredProfiles?: string[],
): boolean => {
  const tailored = suiteTailoredName(labels);
  if (tailored !== undefined) {
    return !!tailoredProfiles?.includes(tailored);
  }
  const key = suiteProfileKey(labels);
  return key !== undefined && !!profiles?.includes(key);
};
