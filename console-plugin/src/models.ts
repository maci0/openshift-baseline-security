import { K8sGroupVersionKind } from '@openshift-console/dynamic-plugin-sdk';

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

export type CheckStatus = 'PASS' | 'FAIL' | 'INFO' | 'MANUAL' | 'ERROR' | 'NOT-APPLICABLE';

export type ComplianceCheckResult = {
  apiVersion: string;
  kind: string;
  metadata: { name: string; namespace: string; labels?: Record<string, string> };
  id: string;
  status: CheckStatus;
  severity: 'unknown' | 'info' | 'low' | 'medium' | 'high';
  description?: string;
  instructions?: string;
};

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
  apiVersion: string;
  kind: string;
  metadata: { name: string };
  spec: {
    profiles: string[];
    schedule?: string;
    installComplianceOperator?: boolean;
    console?: { enabled?: boolean };
  };
  status?: {
    score?: number;
    lastScanTime?: string;
    complianceOperatorVersion?: string;
    profiles?: ProfileStatus[];
    conditions?: { type: string; status: string; message?: string }[];
  };
};

// Profile keys the operator understands, mirrored from the CRD enum.
export const PROFILE_KEYS = [
  'cis',
  'pci-dss',
  'nist-moderate',
  'nist-high',
  'stig',
  'nerc-cip',
  'e8',
  'bsi',
] as const;
