// Pass/fail and severity-weighted score math (lockstep with operator scoring).
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
export const HISTORY_SCORING_MODE_ANN = 'baselinesecurity.openshift.io/history-scoring-mode';

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

const count = (n: number | undefined): number => n ?? 0;

// Sum result counts across profiles (built-in + tailored) for the composition
// donut, so its slices match the score, which includes all of them.
// Mutates a single accumulator (no per-group intermediate objects).
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

// Score badge thresholds (danger below, warning mid, success at/above success).
// Shared by scoreColor (CSS vars) and scoreLabelColor (PatternFly Label) so the
// 60/90 bands cannot drift between the cluster Overview detail item and profile
// cards. Intentionally distinct from ComplianceScoreLow (Prometheus <80): UI
// color is more granular; paging stays less noisy (ADR-017).
export const SCORE_DANGER_BELOW = 60;
export const SCORE_SUCCESS_AT = 90;

// PatternFly semantic status color token for a 0-100 score.
export const scoreColor = (score?: number): string =>
  score == null || Number.isNaN(score) || score < SCORE_DANGER_BELOW
    ? 'var(--pf-t--global--icon--color--status--danger--default)'
    : score < SCORE_SUCCESS_AT
      ? 'var(--pf-t--global--icon--color--status--warning--default)'
      : 'var(--pf-t--global--icon--color--status--success--default)';

// PatternFly Label color for a profile score (same bands as scoreColor).
// Mirror scoreColor's NaN / threshold order so the two cannot diverge.
export const scoreLabelColor = (score: number): 'green' | 'orange' | 'red' =>
  Number.isNaN(score) || score < SCORE_DANGER_BELOW
    ? 'red'
    : score < SCORE_SUCCESS_AT
      ? 'orange'
      : 'green';
// SeverityWeighted product weights (ADR-022). Named table; must stay equal to
// operator severityWeightHigh/Medium/Low/Other (verify-product-lockstep).
const SEVERITY_WEIGHT_HIGH = 10;
const SEVERITY_WEIGHT_MEDIUM = 5;
const SEVERITY_WEIGHT_LOW = 2;
const SEVERITY_WEIGHT_OTHER = 1; // unknown, info, missing, unexpected casing

// Severity weights for SeverityWeighted scoring. Case-sensitive product
// contract (ADR-022): high/medium/low only; everything else is weight 1.
export const severityWeight = (sev: string | undefined): number => {
  switch (sev) {
    case 'high':
      return SEVERITY_WEIGHT_HIGH;
    case 'medium':
      return SEVERITY_WEIGHT_MEDIUM;
    case 'low':
      return SEVERITY_WEIGHT_LOW;
    default:
      return SEVERITY_WEIGHT_OTHER;
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
    // Per-profile history ring (status.profiles[].history). Used only when
    // SeverityWeighted and the CCR list is still empty so badges use the last
    // operator-written weighted point instead of a flat pass/fail approximation.
    history?: ReadonlyArray<{ score?: number }>;
  },
): number | null => {
  // profiles may be empty (tailored-only baseline); ownership still filters via
  // tailoredProfiles. results is required to recompute weights client-side.
  if (opts?.mode === 'SeverityWeighted' && opts.filterKey && opts.results) {
    // Empty CCR list (watch still loading or suite has no results yet): prefer
    // the latest history snapshot (operator wrote it under the weighted formula)
    // so badges do not flash a flat pass/fail ratio while overall score is
    // severity-weighted. Fall back to flat counts only when history is empty
    // (first scan / no points yet). A loaded empty bucket after weighing still
    // returns null below.
    if (opts.results.length === 0) {
      const hist = opts.history;
      if (hist && hist.length > 0) {
        const last = hist[hist.length - 1]?.score;
        if (typeof last === 'number' && Number.isFinite(last)) {
          return Math.min(100, Math.max(0, Math.floor(last)));
        }
      }
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
      // metadata is typed as required but list watches can yield partial/tampered
      // objects; optional-chain so Overview badges never throw mid-weigh.
      const labels = r.metadata?.labels;
      if (
        !prefiltered &&
        (!isOwnedByBaseline(labels, profileSet, tailoredSet) ||
          suiteFilterKey(labels) !== opts.filterKey)
      ) {
        continue;
      }
      // Severity only for score mass (PASS/FAIL). Skip label/field walks for
      // MANUAL/INFO/ERROR/N-A/INCONSISTENT/waived FAIL (often the majority).
      const eff = effectiveStatus(r);
      if (eff === 'PASS') {
        wPass += severityWeight(checkSeverity(r));
      } else if (eff === 'FAIL' && !activeWaived.has(r.metadata?.name ?? '')) {
        wFail += severityWeight(checkSeverity(r));
      }
    }
    const total = wPass + wFail;
    return total > 0 ? Math.floor((wPass * 100) / total) : null;
  }
  return flatProfileScore(counts.pass, counts.fail);
};
