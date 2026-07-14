// INCONSISTENT collapse, SKIP to N/A, and waived-FAIL filter status helpers.
import { ComplianceCheckResult, Waiver } from './models';
import { isWaived } from './waivers';

export type NodeStatus = { node: string; status: string };

// CO annotations on INCONSISTENT results. Lockstep with operator
// inconsistentSourceAnn / mostCommonStatusAnn.
const inconsistentSourceAnn = 'compliance.openshift.io/inconsistent-source';
const mostCommonStatusAnn = 'compliance.openshift.io/most-common-status';

// Recognized CO result statuses that effectiveStatus passes through unchanged.
// SKIP folds to NOT-APPLICABLE and INCONSISTENT collapses separately; anything
// else non-empty folds to ERROR (the operator tally's default bucket).
const KNOWN_EFFECTIVE_STATUSES = new Set([
  'PASS',
  'FAIL',
  'ERROR',
  'INFO',
  'MANUAL',
  'NOT-APPLICABLE',
]);

// Uppercase a CO status token without allocating when the value is already a
// common uppercase enum (PASS/FAIL/…) or has no ASCII lowercase letters.
// Lockstep with operator upperStatusToken: multi-node INCONSISTENT annotations
// call this per node on Results filter / CSV / score paths.
const upperStatusToken = (s: string): string => {
  if (!s) {
    return '';
  }
  switch (s) {
    case 'PASS':
    case 'FAIL':
    case 'ERROR':
    case 'SKIP':
    case 'INFO':
    case 'MANUAL':
    case 'INCONSISTENT':
    case 'NOT-APPLICABLE':
    case 'WAIVED':
      return s;
    default:
      break;
  }
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c >= 97 && c <= 122) {
      return s.toUpperCase();
    }
  }
  return s;
};

// Per-node breakdown of an INCONSISTENT check. The Compliance Operator records
// the nodes that diverge from the majority in the inconsistent-source annotation
// ("node:STATUS,node:STATUS"), and the status the rest share in
// most-common-status. Untrusted cluster data: never throws on a malformed value.
// Status tokens are normalized with upperStatusToken so the detail table matches
// effectiveStatus / operator collapse (CO is usually uppercase already).
export const inconsistentSources = (
  result: ComplianceCheckResult,
): { sources: NodeStatus[]; mostCommon: string | null } => {
  const ann = result.metadata?.annotations ?? {};
  const raw = ann[inconsistentSourceAnn] ?? '';
  const sources = raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const i = s.indexOf(':');
      return i < 0
        ? { node: s, status: '' }
        : { node: s.slice(0, i).trim(), status: upperStatusToken(s.slice(i + 1).trim()) };
    });
  const mostCommon = upperStatusToken((ann[mostCommonStatusAnn] ?? '').trim());
  return { sources, mostCommon: mostCommon || null };
};

// Effective status of a check, collapsing a benign INCONSISTENT (mirrors the
// operator). The Compliance Operator flags a check INCONSISTENT whenever nodes in
// a pool disagree, including when it simply does not apply on some nodes (PASS
// where it applies, NOT-APPLICABLE elsewhere). That is not a real conflict:
//   - any FAIL/ERROR/unknown node state    -> INCONSISTENT (keep the raw signal)
//   - else at least one PASS               -> PASS
//   - else only NOT-APPLICABLE/SKIP        -> NOT-APPLICABLE
//   - none of the above (empty)            -> INCONSISTENT
// Top-level SKIP is folded into NOT-APPLICABLE the same way the operator tallies
// ResultCounts (SKIP is "check skipped for this system"), so Overview N/A counts
// and resultsHref('NOT-APPLICABLE') deep-links include those rows.
//
// Bit flags (no Set) so the Results filter / CSV / score hot paths stay cheap
// over multi-thousand rows when a few are INCONSISTENT.
export const effectiveStatus = (
  r: { status: string; metadata?: { annotations?: Record<string, string> } },
): string => {
  // Non-string / empty status matches the operator tally (unknown/empty -> ERROR
  // so a CCR is never silently dropped from ResultCounts). CRs are not runtime
  // type-checked; a missing field must not yield a blank filter chip or a
  // non-string that crashes CSV export.
  if (typeof r.status !== 'string' || !r.status) {
    return 'ERROR';
  }
  if (r.status === 'SKIP') {
    return 'NOT-APPLICABLE';
  }
  if (r.status !== 'INCONSISTENT') {
    // Pass through only recognized statuses. A non-empty but unknown token (a
    // future CO status, corruption, or a wrong-case value) folds to ERROR, the
    // same bucket the operator tally uses, so the row stays visible under the
    // Error filter and matches the Overview donut instead of showing a bogus
    // label that matches no status filter chip.
    return KNOWN_EFFECTIVE_STATUSES.has(r.status) ? r.status : 'ERROR';
  }
  // One annotations object read for both CO keys (filter/CSV/score hot path).
  const ann = r.metadata?.annotations;
  const raw = ann?.[inconsistentSourceAnn] ?? '';
  let hasPass = false;
  let hasFail = false;
  let hasError = false;
  let hasNA = false;
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
      case 'SKIP':
        // Both CO states collapse to NOT-APPLICABLE (lockstep with the operator).
        hasNA = true;
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
        const st = upperStatusToken(s.slice(colon + 1).trim());
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
  const mostCommon = (ann?.[mostCommonStatusAnn] ?? '').trim();
  if (mostCommon) {
    add(upperStatusToken(mostCommon));
  }
  if (hasUnknown || hasFail || hasError) {
    return 'INCONSISTENT';
  }
  if (hasPass) {
    return 'PASS';
  }
  if (hasNA) {
    return 'NOT-APPLICABLE';
  }
  return 'INCONSISTENT';
};

// Filter-chip / deep-link / CSV status for a result. FAIL+active-waiver is
// "WAIVED" so Results FAIL filters match Overview fail counts (operator score
// math excludes waived fails from the Fail bucket). A waived PASS stays PASS
// (still scored). Pass a prebuilt Set (from activeWaivedNames) for O(1) per-row
// lookups on multi-thousand CCR lists; a Waiver[] falls back to isWaived.
export const resultFilterStatus = (
  r: { status: string; metadata?: { name?: string; annotations?: Record<string, string> } },
  waivers?: Waiver[] | ReadonlySet<string>,
): string => {
  const eff = effectiveStatus(r);
  if (eff !== 'FAIL' || !waivers) {
    return eff;
  }
  // Partial list items may lack metadata.name; empty never matches a waiver.
  const name = r.metadata?.name ?? '';
  if (!name) {
    return eff;
  }
  // Array.isArray narrows Waiver[] for isWaived; Set/ReadonlySet uses .has.
  if (Array.isArray(waivers)) {
    return isWaived(name, waivers) ? 'WAIVED' : eff;
  }
  return waivers.has(name) ? 'WAIVED' : eff;
};
