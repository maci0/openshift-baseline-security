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

export const ClusterBaselineModel = model(ClusterBaselineGVK, 'clusterbaselines', false);
export const ComplianceScanModel = model(ComplianceScanGVK, 'compliancescans', true);
export const ComplianceRemediationModel = model(ComplianceRemediationGVK, 'complianceremediations', true);

export type CheckStatus =
  | 'PASS'
  | 'FAIL'
  | 'INFO'
  | 'MANUAL'
  | 'ERROR'
  | 'INCONSISTENT'
  | 'NOT-APPLICABLE';

export type ComplianceCheckResult = {
  metadata: { name: string; namespace: string; labels?: Record<string, string> };
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
  error: number;
  notApplicable: number;
};

export type ProfileStatus = ResultCounts & {
  key: string;
  profileNames: string[];
};

export type TailoredProfileStatus = ResultCounts & { name: string };

export type ClusterBaseline = {
  metadata: { name: string };
  spec: {
    profiles: string[];
    tailoredProfiles?: string[];
    schedule?: string;
    installComplianceOperator?: 'Automatic' | 'Manual';
    console?: { managementState?: 'Managed' | 'Removed' };
    remediation?: { apply?: 'Automatic' | 'Manual' };
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
  };
};

/**
 * Display key encoded in a CO object's suite label. Built-in profiles use
 * "baseline-<key>"; tailored profiles use "baseline-tp-<name>" (mirroring the
 * operator's binding names). Returns undefined when the label is not ours.
 */
export const suiteProfileKey = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const suite = labels?.['compliance.openshift.io/suite'];
  return suite?.startsWith('baseline-') ? suite.slice('baseline-'.length) : undefined;
};

/** TailoredProfile name for a "baseline-tp-<name>" suite, else undefined. */
export const suiteTailoredName = (
  labels: Record<string, string> | undefined,
): string | undefined => {
  const suite = labels?.['compliance.openshift.io/suite'];
  return suite?.startsWith('baseline-tp-') ? suite.slice('baseline-tp-'.length) : undefined;
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
