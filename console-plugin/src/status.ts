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
//
// Bit flags (no Set) so the Results filter / CSV / score hot paths stay cheap
// over multi-thousand rows when a few are INCONSISTENT.
export const effectiveStatus = (
  r: { status: string; metadata?: { annotations?: Record<string, string> } },
): string => {
  if (r.status === 'SKIP') {
    return 'NOT-APPLICABLE';
  }
  if (r.status !== 'INCONSISTENT') {
    return r.status;
  }
  const ann = r.metadata?.annotations ?? {};
  const raw = ann['compliance.openshift.io/inconsistent-source'] ?? '';
  let hasPass = false;
  let hasFail = false;
  let hasError = false;
  let hasNA = false;
  let hasSkip = false;
  let hasUnknown = false;
  const add = (st: string): void => {
    switch (st) {
      case 'PASS':
        hasPass = true;
        break;
      case 'FAIL':
        hasFail = true;
        break;
      case 'ERROR':
        hasError = true;
        break;
      case 'NOT-APPLICABLE':
        hasNA = true;
        break;
      case 'SKIP':
        hasSkip = true;
        break;
      default:
        hasUnknown = true;
    }
  };
  // Walk comma-separated "node:STATUS" pairs (no split+map+filter chain).
  let start = 0;
  while (start <= raw.length) {
    const comma = raw.indexOf(',', start);
    const end = comma < 0 ? raw.length : comma;
    const s = raw.slice(start, end).trim();
    if (s) {
      const colon = s.indexOf(':');
      if (colon >= 0) {
        const st = s.slice(colon + 1).trim().toUpperCase();
        if (st) {
          add(st);
        }
      }
    }
    if (comma < 0) {
      break;
    }
    start = comma + 1;
  }
  const mostCommon = (ann['compliance.openshift.io/most-common-status'] ?? '').trim();
  if (mostCommon) {
    add(mostCommon.toUpperCase());
  }
  if (hasUnknown || hasFail || hasError) {
    return 'INCONSISTENT';
  }
  if (hasPass) {
    return 'PASS';
  }
  if (hasNA || hasSkip) {
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
