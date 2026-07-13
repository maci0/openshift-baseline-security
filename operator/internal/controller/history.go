package controller

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// scopeToEvaluated returns base restricted to names also present in evaluated
// (both sorted ascending). A base check absent from evaluated is no longer
// scanned (its profile was deselected), so keeping it would make the next diff
// report it as Fixed though nothing was fixed. When evaluated is nil the base is
// returned as a fresh clone unchanged (legacy/test callers that omit the set).
func scopeToEvaluated(base, evaluated []string) []string {
	if evaluated == nil {
		return slices.Clone(base)
	}
	out := make([]string, 0, len(base))
	i, j := 0, 0
	for i < len(base) && j < len(evaluated) {
		switch {
		case base[i] == evaluated[j]:
			out = append(out, base[i])
			i++
			j++
		case base[i] < evaluated[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func syncFailureDiff(cb *baselinev1alpha1.ClusterBaseline, currentFails, baseFailures []string) {
	// Production lists are sorted (aggregate sorts currentFails; PreviousFailures /
	// DiffBaseFailures are clones). Two-pointer set-diff avoids two maps over
	// multi-thousand FAIL names on every completed-scan / late-CCR refresh.
	if slices.IsSorted(currentFails) && slices.IsSorted(baseFailures) {
		cb.Status.NewlyFailed = sortedDiff(currentFails, baseFailures)
		cb.Status.Fixed = sortedDiff(baseFailures, currentFails)
		return
	}
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

// clearHistoryRings drops overall and per-profile score history. Used when the
// scoring formula changes so rings never mix Flat and SeverityWeighted points
// (MiniTrend / Score trend charts; ADR-008).
func clearHistoryRings(cb *baselinev1alpha1.ClusterBaseline) {
	cb.Status.History = nil
	for i := range cb.Status.Profiles {
		cb.Status.Profiles[i].History = nil
	}
	for i := range cb.Status.TailoredProfiles {
		cb.Status.TailoredProfiles[i].History = nil
	}
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
			// Copy: hist[:n-1] would alias the caller's backing array and leave
			// capacity pointing at the removed snapshot (same class of bug as
			// appendHistoryRing / clampHistory after truncation).
			if n == 1 {
				return nil
			}
			return append([]baselinev1alpha1.ScoreSnapshot(nil), hist[:n-1]...)
		}
		hist[n-1].Score = *s
		return clampHistory(hist, historyMax)
	}
	if s == nil {
		return hist
	}
	return appendHistoryRing(hist, t, *s, historyMax)
}

func syncProfileHistory(cb *baselinev1alpha1.ClusterBaseline, t metav1.Time, weights *scoreWeights) {
	mode := scoringMode(cb)
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
