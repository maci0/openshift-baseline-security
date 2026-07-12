package controller

import (
	"math"
	"math/bits"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

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
	got := cb.Annotations[historyScoringModeAnn]
	if got == "" {
		return true
	}
	return got == string(scoringMode(cb))
}

// score is pass/(pass+fail)*100, or nil when there are no countable results.
// Widens to int64 so pass+fail and pass*100 cannot overflow int32.
// Overall score is a single pooled ratio across every selected profile (and
// tailored binding), not the mean of per-profile scores (ADR-014).
func score(pass, fail int32) *int32 {
	return score64(int64(pass), int64(fail))
}

// score64 is score() over already-int64 (severity-weighted) sums.
// Addition and pass*100 use overflow-safe arithmetic so a pathological weighted
// total cannot wrap into a nonsense ratio (the old int64 multiply matched a
// wrong oracle and gave false confidence under the fuzzer).
func score64(pass, fail int64) *int32 {
	if pass < 0 || fail < 0 {
		return nil
	}
	// pass+fail would wrap past MaxInt64: treat as uncountable.
	if pass > 0 && fail > math.MaxInt64-pass {
		return nil
	}
	total := pass + fail
	if total == 0 {
		return nil
	}
	// 128-bit pass*100 / total so huge mass still floors correctly into [0,100].
	hi, lo := bits.Mul64(uint64(pass), 100)
	q, _ := bits.Div64(hi, lo, uint64(total))
	s := int32(q)
	return &s
}

// SeverityWeighted product weights (ADR-022). Named so the table is one place
// in Go; must stay equal to console-plugin severityWeight (verify-product-lockstep).
const (
	severityWeightHigh   int64 = 10
	severityWeightMedium int64 = 5
	severityWeightLow    int64 = 2
	severityWeightOther  int64 = 1 // unknown, info, missing, unexpected casing
)

// severityWeight maps a ComplianceCheckResult severity to a fixed score weight
// used when spec.scoring.mode is SeverityWeighted. Case-sensitive product
// contract (ADR-022): high/medium/low only; everything else is weight 1.
func severityWeight(sev string) int64 {
	switch sev {
	case "high":
		return severityWeightHigh
	case "medium":
		return severityWeightMedium
	case "low":
		return severityWeightLow
	default: // "unknown", "info", missing, or unexpected casing
		return severityWeightOther
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

// checkSeverity returns a ComplianceCheckResult severity for weighting. Prefer
// the typed .severity field (CO source of truth on the CR); fall back to the
// check-severity label CO also sets for selection. Missing/empty both sides is
// "unknown" so the weight table (unknown/info/missing=1) and the console
// checkSeverity helper stay aligned. Uses unstructuredLabel so the
// SeverityWeighted CCR hot path does not copy the full labels map via
// GetLabels on every PASS/FAIL result.
func checkSeverity(item *unstructured.Unstructured) string {
	if sev, ok := item.Object["severity"].(string); ok && sev != "" {
		return sev
	}
	if label := unstructuredLabel(item.Object, checkSeverityLabel); label != "" {
		return label
	}
	return "unknown"
}

// profileBucketScore is the score recorded for one profile's history ring.
// When SeverityWeighted and weights are present, uses the same weight table as
// status.score; otherwise flat pass/(pass+fail) from ResultCounts.
func profileBucketScore(pass, fail int32, w weightedSum, mode baselinev1alpha1.ScoringMode, haveWeights bool) *int32 {
	if mode == baselinev1alpha1.ScoringSeverityWeighted && haveWeights {
		return score64(w.pass, w.fail)
	}
	return score(pass, fail)
}
