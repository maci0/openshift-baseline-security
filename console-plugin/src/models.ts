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

export type CheckStatus = 'PASS' | 'FAIL' | 'INFO' | 'MANUAL' | 'ERROR' | 'NOT-APPLICABLE';

export type ComplianceCheckResult = {
  metadata: { name: string; namespace: string; labels?: Record<string, string> };
  status: CheckStatus;
  severity: 'unknown' | 'info' | 'low' | 'medium' | 'high';
  description?: string;
  instructions?: string;
};

export type ComplianceRemediation = {
  metadata: { name: string; namespace: string; labels?: Record<string, string> };
  spec: { apply: boolean; current?: { object?: { kind?: string } } };
  status?: {
    applicationState?: 'Applied' | 'NotApplied' | 'Error' | 'Outdated' | 'MissingDependencies';
  };
};

export type ScoreSnapshot = { time: string; score: number };

export type ProfileStatus = {
  key: string;
  profileNames: string[];
  pass: number;
  fail: number;
  manual: number;
  error: number;
  notApplicable: number;
};

export type ClusterBaseline = {
  metadata: { name: string };
  spec: {
    profiles: string[];
    schedule?: string;
    installComplianceOperator?: boolean;
    console?: { enabled?: boolean };
    remediation?: { autoApply?: boolean };
  };
  status?: {
    score?: number;
    lastScanTime?: string;
    complianceOperatorVersion?: string;
    profiles?: ProfileStatus[];
    conditions?: { type: string; status: string; message?: string }[];
    history?: ScoreSnapshot[];
  };
};

/** True when a CO object belongs to a baseline-* suite for a selected profile. */
export const isOwnedByBaseline = (
  labels: Record<string, string> | undefined,
  profiles: string[] | undefined,
): boolean => {
  const suite = labels?.['compliance.openshift.io/suite'];
  // suite = "baseline-<key>"; bindingName in the operator.
  return !!suite?.startsWith('baseline-') && !!profiles?.includes(suite.slice('baseline-'.length));
};
