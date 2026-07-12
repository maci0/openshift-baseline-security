import {
  ClusterBaseline,
  ComplianceCheckResult,
  isOwnedByBaseline,
  ResultCounts,
  suiteFilterKey,
  Waiver,
} from './models';
import { effectiveStatus } from './status';
import { activeWaivedNames } from './waivers';

// Operator annotation recording which scoring mode wrote the latest history
// ring points (lockstep with operator historyScoringModeAnn). Used to detect
// Flat <-> SeverityWeighted flips so charts can warn about incomparable points.
export const HISTORY_SCORING_MODE_ANN = 'baselinesecurity.io/history-scoring-mode';

export type ScoringMode = 'Flat' | 'SeverityWeighted';

// Effective scoring mode (Flat when unset). Matches operator scoringMode().
export const effectiveScoringMode = (
  baseline?: Pick<ClusterBaseline, 'spec'> | null,
): ScoringMode =>
  baseline?.spec.scoring?.mode === 'SeverityWeighted' ? 'SeverityWeighted' : 'Flat';

// True when history ring points were stamped under a different formula than the
// current mode (ADR-008). Missing annotation (pre-feature CRs) is not a mismatch.
export const historyScoringModeMismatch = (
  baseline?: Pick<ClusterBaseline, 'spec' | 'metadata'> | null,
): boolean => {
  const stamped = baseline?.metadata.annotations?.[HISTORY_SCORING_MODE_ANN];
  if (!stamped) {
    return false;
  }
  return stamped !== effectiveScoringMode(baseline);
};

// Pick the singleton ClusterBaseline (named "cluster", else the first) and
// return its score, or null when there is none / it has not scored yet. Shared
// by the cluster Overview detail item.
export const clusterScore = (baselines?: ClusterBaseline[]): number | null => {
  const b = baselines?.find((x) => x.metadata.name === 'cluster') ?? baselines?.[0];
  return b?.status?.score ?? null;
};

// Sum result counts across profiles (built-in + tailored) for the composition
// donut, so its slices match the score, which includes all of them.
// Mutates a single accumulator (no per-group intermediate objects).
const count = (n: number | undefined): number => n ?? 0;

export const aggregateCounts = (...groups: ResultCounts[]): ResultCounts => {
  const a: ResultCounts = {
    pass: 0,
    fail: 0,
    manual: 0,
    info: 0,
    error: 0,
    inconsistent: 0,
    waived: 0,
    notApplicable: 0,
  };
  for (const g of groups) {
    a.pass += count(g.pass);
    a.fail += count(g.fail);
    a.manual += count(g.manual);
    a.info += count(g.info);
    a.error += count(g.error);
    a.inconsistent += count(g.inconsistent);
    a.waived += count(g.waived);
    a.notApplicable += count(g.notApplicable);
  }
  return a;
};

// PatternFly semantic status color token for a 0-100 score.
export const scoreColor = (score?: number): string =>
  score == null || Number.isNaN(score) || score < 60
    ? 'var(--pf-t--global--icon--color--status--danger--default)'
    : score < 90
      ? 'var(--pf-t--global--icon--color--status--warning--default)'
      : 'var(--pf-t--global--icon--color--status--success--default)';

// Severity weights for SeverityWeighted scoring. Must stay in lockstep with the
// operator's severityWeight table (high=10, medium=5, low=2, else 1).
export const severityWeight = (sev: string | undefined): number => {
  switch (sev) {
    case 'high':
      return 10;
    case 'medium':
      return 5;
    case 'low':
      return 2;
    default:
      return 1;
  }
};

// CO label used when the typed .severity field is empty. Must match the
// operator checkSeverityLabel / checkSeverity helper.
const checkSeverityLabel = 'compliance.openshift.io/check-severity';

/**
 * Severity for weighting and display. Prefer the typed .severity field (CO
 * source of truth); fall back to the check-severity label CO also sets for
 * selection. Missing/empty both sides is "unknown" so Results severity filters
 * and CSV match the weight table (unknown/info/missing=1) and TEST-PLAN
 * expectation that empty severity is visible under the "unknown" chip.
 * Field/label values are otherwise returned as-is (case-sensitive lockstep
 * with the operator weight table).
 */
export const checkSeverity = (r: {
  severity?: string;
  metadata?: { labels?: Record<string, string> };
}): string => {
  const field = r.severity;
  if (typeof field === 'string' && field) {
    return field;
  }
  const label = r.metadata?.labels?.[checkSeverityLabel];
  if (typeof label === 'string' && label) {
    return label;
  }
  return 'unknown';
};

// Flat pass/(pass+fail) score from ResultCounts, floored like the operator.
// Reject negative / non-finite mass the same way operator score() does (nil),
// so a stale or hand-edited ResultCounts cannot paint a false badge (e.g.
// pass=-1, fail=5 must not become a negative "score").
export const flatProfileScore = (pass?: number, fail?: number): number | null => {
  const p = pass ?? 0;
  const f = fail ?? 0;
  if (!Number.isFinite(p) || !Number.isFinite(f) || p < 0 || f < 0 || p + f === 0) {
    return null;
  }
  return Math.floor((p * 100) / (p + f));
};

/**
 * Score for one profile card. Flat mode (default) uses ResultCounts. When
 * scoring.mode is SeverityWeighted, recomputes from owned check results with
 * the same weight table as the operator so Overview badges match status.score.
 * filterKey is a built-in profile key or "tp-<name>" (suiteFilterKey shape).
 */
export const profileScore = (
  counts: { pass?: number; fail?: number },
  opts?: {
    mode?: 'Flat' | 'SeverityWeighted';
    filterKey?: string;
    results?: ComplianceCheckResult[];
    profiles?: string[];
    tailoredProfiles?: string[];
    waivers?: Waiver[];
    // Prebuilt active-waiver set from Overview (one Set for all cards).
    activeWaived?: ReadonlySet<string>;
    now?: Date;
  },
): number | null => {
  // profiles may be empty (tailored-only baseline); ownership still filters via
  // tailoredProfiles. results is required to recompute weights client-side.
  if (opts?.mode === 'SeverityWeighted' && opts.filterKey && opts.results) {
    // Empty CCR list (watch still loading or suite has no results yet): use flat
    // counts from status so Overview badges are not blank while status already
    // has tallies. A loaded empty bucket after weighing still returns null below.
    if (opts.results.length === 0) {
      return flatProfileScore(counts.pass, counts.fail);
    }
    // Prefer a shared Set from the caller; otherwise build once for this score.
    const activeWaived = opts.activeWaived ?? activeWaivedNames(opts.waivers, opts.now);
    // When Overview pre-buckets by suiteFilterKey + ownership, profiles is
    // omitted and results are already scoped: skip the second membership scan.
    const prefiltered = opts.profiles === undefined && opts.tailoredProfiles === undefined;
    const profileSet = !prefiltered && opts.profiles ? new Set(opts.profiles) : undefined;
    const tailoredSet = !prefiltered && opts.tailoredProfiles ? new Set(opts.tailoredProfiles) : undefined;
    let wPass = 0;
    let wFail = 0;
    for (const r of opts.results) {
      if (
        !prefiltered &&
        (!isOwnedByBaseline(r.metadata.labels, profileSet, tailoredSet) ||
          suiteFilterKey(r.metadata.labels) !== opts.filterKey)
      ) {
        continue;
      }
      const eff = effectiveStatus(r);
      const sev = checkSeverity(r);
      if (eff === 'PASS') {
        wPass += severityWeight(sev);
      } else if (eff === 'FAIL' && !activeWaived.has(r.metadata.name)) {
        wFail += severityWeight(sev);
      }
    }
    const total = wPass + wFail;
    return total > 0 ? Math.floor((wPass * 100) / total) : null;
  }
  return flatProfileScore(counts.pass, counts.fail);
};
