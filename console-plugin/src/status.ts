import { ComplianceCheckResult, Waiver } from './models';
import { isWaived } from './waivers';

export type NodeStatus = { node: string; status: string };

// Per-node breakdown of an INCONSISTENT check. The Compliance Operator records
// the nodes that diverge from the majority in the inconsistent-source annotation
// ("node:STATUS,node:STATUS"), and the status the rest share in
// most-common-status. Untrusted cluster data: never throws on a malformed value.
export const inconsistentSources = (
  result: ComplianceCheckResult,
): { sources: NodeStatus[]; mostCommon: string | null } => {
  const ann = result.metadata?.annotations ?? {};
  const raw = ann['compliance.openshift.io/inconsistent-source'] ?? '';
  const sources = raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const i = s.indexOf(':');
      return i < 0
        ? { node: s, status: '' }
        : { node: s.slice(0, i).trim(), status: s.slice(i + 1).trim() };
    });
  const mostCommon = (ann['compliance.openshift.io/most-common-status'] ?? '').trim();
  return { sources, mostCommon: mostCommon || null };
};

// Known node states for INCONSISTENT collapse. Module-level so the filter hot
// path does not allocate a fresh array on every includes() call.
const knownInconsistentStates = new Set([
  'PASS',
  'FAIL',
  'ERROR',
  'NOT-APPLICABLE',
  'SKIP',
]);

// Effective status of a check, collapsing a benign INCONSISTENT (mirrors the
// operator). The Compliance Operator flags a check INCONSISTENT whenever nodes in
// a pool disagree, including when it simply does not apply on some nodes (PASS
// where it applies, NOT-APPLICABLE elsewhere). That is not a real conflict:
//   - any FAIL/ERROR among the node states -> INCONSISTENT (genuine)
//   - else at least one PASS               -> PASS
//   - else only NOT-APPLICABLE/SKIP        -> NOT-APPLICABLE
//   - unknown/empty                        -> INCONSISTENT (keep the raw signal)
// Top-level SKIP is folded into NOT-APPLICABLE the same way the operator tallies
// ResultCounts (SKIP is "check skipped for this system"), so Overview N/A counts
// and resultsHref('NOT-APPLICABLE') deep-links include those rows.
export const effectiveStatus = (
  r: { status: string; metadata?: { annotations?: Record<string, string> } },
): string => {
  if (r.status === 'SKIP') {
    return 'NOT-APPLICABLE';
  }
  if (r.status !== 'INCONSISTENT') {
    return r.status;
  }
  const { sources, mostCommon } = inconsistentSources(r as ComplianceCheckResult);
  const states = new Set<string>();
  for (const s of sources) {
    if (s.status) {
      states.add(s.status.toUpperCase());
    }
  }
  if (mostCommon) {
    states.add(mostCommon.toUpperCase());
  }
  for (const state of states) {
    if (!knownInconsistentStates.has(state)) {
      return 'INCONSISTENT';
    }
  }
  if (states.has('FAIL') || states.has('ERROR')) {
    return 'INCONSISTENT';
  }
  if (states.has('PASS')) {
    return 'PASS';
  }
  if (states.has('NOT-APPLICABLE') || states.has('SKIP')) {
    return 'NOT-APPLICABLE';
  }
  return 'INCONSISTENT';
};

// Filter-chip / deep-link status for a result. FAIL+waiver is "WAIVED" so the
// Results FAIL filter matches Overview fail counts (operator score math excludes
// waived fails from the Fail bucket). A waived PASS stays PASS (still scored).
export const resultFilterStatus = (
  r: { status: string; metadata: { name: string; annotations?: Record<string, string> } },
  waivers?: Waiver[],
): string => {
  const eff = effectiveStatus(r);
  return eff === 'FAIL' && isWaived(r.metadata.name, waivers) ? 'WAIVED' : eff;
};
