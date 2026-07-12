package controller

import baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"

// historyScoringModeAnn records which scoring mode wrote the latest history
// ring points. Late CCR refresh for the same LastScanTime must not rewrite those
// snapshots under a different formula when the admin flips Flat <-> SeverityWeighted.
const historyScoringModeAnn = "baselinesecurity.io/history-scoring-mode"

// scoringMode returns the effective scoring mode (Flat when unset).
func scoringMode(cb *baselinev1alpha1.ClusterBaseline) baselinev1alpha1.ScoringMode {
	if cb.Spec.Scoring.Mode == baselinev1alpha1.ScoringSeverityWeighted {
		return baselinev1alpha1.ScoringSeverityWeighted
	}
	return baselinev1alpha1.ScoringFlat
}

// historyModeMatches is true when late history refresh may rewrite the latest
// snapshots under the current formula. Missing annotation (pre-feature CRs)
// allows refresh so existing clusters keep late-CCR correction; the next history
// write stamps the mode.
func historyModeMatches(cb *baselinev1alpha1.ClusterBaseline) bool {
	got := ""
	if cb.Annotations != nil {
		got = cb.Annotations[historyScoringModeAnn]
	}
	if got == "" {
		return true
	}
	return got == string(scoringMode(cb))
}

// score is pass/(pass+fail)*100, or nil when there are no countable results.
// Widens to int64 so pass+fail and pass*100 cannot overflow int32.
// Overall score is a single pooled ratio across every selected profile (and
// tailored binding), not the mean of per-profile scores.
func score(pass, fail int32) *int32 {
	return score64(int64(pass), int64(fail))
}

// score64 is score() over already-int64 (severity-weighted) sums.
func score64(pass, fail int64) *int32 {
	if pass < 0 || fail < 0 || pass+fail == 0 {
		return nil
	}
	s := int32(pass * 100 / (pass + fail))
	return &s
}

// severityWeight maps a ComplianceCheckResult severity to a fixed score weight
// used when spec.scoring.mode is SeverityWeighted. The table is the product
// contract: high=10, medium=5, low=2, unknown/info/missing=1. Must stay in
// lockstep with the console plugin's severityWeight helper.
func severityWeight(sev string) int64 {
	switch sev {
	case "high":
		return 10
	case "medium":
		return 5
	case "low":
		return 2
	default: // "unknown", "info", or missing
		return 1
	}
}

// weightedSum is severity-weighted PASS/FAIL mass for one profile bucket.
type weightedSum struct {
	pass, fail int64
}

// scoreWeights holds per-bucket weighted totals so per-profile history can
// follow the same scoring mode as status.score. Nil maps mean fall back to
// flat pass/fail counts (tests, or Flat mode).
type scoreWeights struct {
	profiles map[baselinev1alpha1.ProfileKey]weightedSum
	tailored map[string]weightedSum
}

// clampScore keeps status.score inside the CRD [0,100] bounds so a hand-edited
// out-of-range value cannot lock out Status().Update admission.
func clampScore(s *int32) *int32 {
	if s == nil {
		return nil
	}
	switch {
	case *s < 0:
		z := int32(0)
		return &z
	case *s > 100:
		z := int32(100)
		return &z
	default:
		return s
	}
}
