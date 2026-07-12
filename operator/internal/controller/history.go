package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func syncFailureDiff(cb *baselinev1alpha1.ClusterBaseline, currentFails, baseFailures []string) {
	base := make(map[string]bool, len(baseFailures))
	for _, name := range baseFailures {
		base[name] = true
	}
	current := make(map[string]bool, len(currentFails))
	for _, name := range currentFails {
		current[name] = true
	}
	cb.Status.NewlyFailed = notIn(currentFails, base)
	cb.Status.Fixed = notIn(baseFailures, current)
}

// appendHistoryRing appends a snapshot and keeps at most max entries (oldest first).
// The returned slice does not alias the input backing array after truncation.
func appendHistoryRing(hist []baselinev1alpha1.ScoreSnapshot, t metav1.Time, s int32, max int) []baselinev1alpha1.ScoreSnapshot {
	hist = append(hist, baselinev1alpha1.ScoreSnapshot{Time: t, Score: s})
	return clampHistory(hist, max)
}

// syncHistorySnapshot refreshes a late-arriving rollup for the same completed
// scan, or appends the first point for a new scan. A score becoming unavailable
// removes only that scan's stale point.
func syncHistorySnapshot(
	hist []baselinev1alpha1.ScoreSnapshot, t metav1.Time, s *int32,
) []baselinev1alpha1.ScoreSnapshot {
	if n := len(hist); n > 0 && hist[n-1].Time.Equal(&t) {
		if s == nil {
			return hist[:n-1]
		}
		hist[n-1].Score = *s
		return clampHistory(hist, historyMax)
	}
	if s == nil {
		return hist
	}
	return appendHistoryRing(hist, t, *s, historyMax)
}

// checkSeverity returns a ComplianceCheckResult severity for weighting. Prefer
// the typed .severity field (CO source of truth on the CR); fall back to the
// check-severity label CO also sets for selection.
func checkSeverity(item *unstructured.Unstructured) string {
	if sev, _, _ := unstructured.NestedString(item.Object, "severity"); sev != "" {
		return sev
	}
	return item.GetLabels()[checkSeverityLabel]
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

func syncProfileHistory(cb *baselinev1alpha1.ClusterBaseline, t metav1.Time, weights *scoreWeights) {
	mode := cb.Spec.Scoring.Mode
	haveWeights := weights != nil
	for i := range cb.Status.Profiles {
		var w weightedSum
		if haveWeights {
			w = weights.profiles[cb.Status.Profiles[i].Key]
		}
		ps := profileBucketScore(
			cb.Status.Profiles[i].Pass, cb.Status.Profiles[i].Fail, w, mode, haveWeights,
		)
		cb.Status.Profiles[i].History = syncHistorySnapshot(cb.Status.Profiles[i].History, t, ps)
	}
	for i := range cb.Status.TailoredProfiles {
		var w weightedSum
		if haveWeights {
			w = weights.tailored[cb.Status.TailoredProfiles[i].Name]
		}
		ps := profileBucketScore(
			cb.Status.TailoredProfiles[i].Pass, cb.Status.TailoredProfiles[i].Fail, w, mode, haveWeights,
		)
		cb.Status.TailoredProfiles[i].History = syncHistorySnapshot(
			cb.Status.TailoredProfiles[i].History, t, ps,
		)
	}
}

// clampHistory trims history to the CRD MaxItems bound and clamps each score
// into [0,100]. Without this, a status already over the limit or with an
// out-of-range score (hand-edit, old bug) makes every Status().Update fail
// admission and freezes reconciliation feedback.
func clampHistory(hist []baselinev1alpha1.ScoreSnapshot, max int) []baselinev1alpha1.ScoreSnapshot {
	if max > 0 && len(hist) > max {
		hist = append([]baselinev1alpha1.ScoreSnapshot(nil), hist[len(hist)-max:]...)
	}
	for i := range hist {
		if hist[i].Score < 0 {
			hist[i].Score = 0
		} else if hist[i].Score > 100 {
			hist[i].Score = 100
		}
	}
	return hist
}
