import {
  ClusterBaseline,
  ComplianceCheckResult,
  isOwnedByBaseline,
  ResultCounts,
  suiteFilterKey,
  Waiver,
} from './models';
import { effectiveStatus } from './status';
import { isWaived } from './waivers';

// Pick the singleton ClusterBaseline (named "cluster", else the first) and
// return its score, or null when there is none / it has not scored yet. Shared
// by the cluster Overview detail item.
export const clusterScore = (baselines?: ClusterBaseline[]): number | null => {
  const b = baselines?.find((x) => x.metadata.name === 'cluster') ?? baselines?.[0];
  return b?.status?.score ?? null;
};

// Sum result counts across profiles (built-in + tailored) for the composition
// donut, so its slices match the score, which includes all of them.
const count = (n: number | undefined): number => n ?? 0;

export const aggregateCounts = (...groups: ResultCounts[]): ResultCounts =>
  groups.reduce(
    (a, g) => ({
      pass: a.pass + count(g.pass),
      fail: a.fail + count(g.fail),
      manual: a.manual + count(g.manual),
      info: a.info + count(g.info),
      error: a.error + count(g.error),
      inconsistent: a.inconsistent + count(g.inconsistent),
      waived: a.waived + count(g.waived),
      notApplicable: a.notApplicable + count(g.notApplicable),
    }),
    { pass: 0, fail: 0, manual: 0, info: 0, error: 0, inconsistent: 0, waived: 0, notApplicable: 0 },
  );

// PatternFly semantic status color token for a 0-100 score.
export const scoreColor = (score?: number): string =>
  score == null || score < 60
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

// Flat pass/(pass+fail) score from ResultCounts, floored like the operator.
export const flatProfileScore = (pass?: number, fail?: number): number | null => {
  const p = pass ?? 0;
  const f = fail ?? 0;
  const denom = p + f;
  return denom > 0 ? Math.floor((p * 100) / denom) : null;
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
    now?: Date;
  },
): number | null => {
  // profiles may be empty (tailored-only baseline); ownership still filters via
  // tailoredProfiles. results is required to recompute weights client-side.
  if (opts?.mode === 'SeverityWeighted' && opts.filterKey && opts.results) {
    let wPass = 0;
    let wFail = 0;
    for (const r of opts.results) {
      if (
        !isOwnedByBaseline(r.metadata.labels, opts.profiles, opts.tailoredProfiles) ||
        suiteFilterKey(r.metadata.labels) !== opts.filterKey
      ) {
        continue;
      }
      const eff = effectiveStatus(r);
      if (eff === 'PASS') {
        wPass += severityWeight(r.severity);
      } else if (eff === 'FAIL' && !isWaived(r.metadata.name, opts.waivers, opts.now)) {
        wFail += severityWeight(r.severity);
      }
    }
    const total = wPass + wFail;
    return total > 0 ? Math.floor((wPass * 100) / total) : null;
  }
  return flatProfileScore(counts.pass, counts.fail);
};
