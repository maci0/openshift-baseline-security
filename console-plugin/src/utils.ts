import { ComplianceCheckResult } from './models';

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
export const checkTitle = (r: ComplianceCheckResult): string =>
  r.description?.split('\n')[0]?.trim() || r.metadata.name;

export const checkBody = (r: ComplianceCheckResult): string =>
  r.description?.split('\n').slice(1).join('\n').trim() ?? '';

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

// JSON patch for spec.remediation.autoApply: 'add' must create the parent
// object when spec.remediation is absent, 'replace' must not clobber siblings.
export const autoApplyPatch = (hasRemediation: boolean, checked: boolean) =>
  hasRemediation
    ? [{ op: 'replace' as const, path: '/spec/remediation/autoApply', value: checked }]
    : [{ op: 'add' as const, path: '/spec/remediation', value: { autoApply: checked } }];

// PatternFly semantic status color token for a 0-100 score.
export const scoreColor = (score?: number): string =>
  score == null || score < 60
    ? 'var(--pf-t--global--icon--color--status--danger--default)'
    : score < 90
      ? 'var(--pf-t--global--icon--color--status--warning--default)'
      : 'var(--pf-t--global--icon--color--status--success--default)';
