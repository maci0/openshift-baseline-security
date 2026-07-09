import { ComplianceCheckResult } from './models';

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
export const checkTitle = (r: ComplianceCheckResult): string =>
  r.description?.split('\n')[0]?.trim() || r.metadata.name;

// PatternFly semantic status color token for a 0-100 score.
export const scoreColor = (score?: number): string =>
  score == null || score < 60
    ? 'var(--pf-t--global--icon--color--status--danger--default)'
    : score < 90
      ? 'var(--pf-t--global--icon--color--status--warning--default)'
      : 'var(--pf-t--global--icon--color--status--success--default)';
