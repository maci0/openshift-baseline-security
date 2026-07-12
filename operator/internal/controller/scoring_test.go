package controller

import (
	"testing"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// TestScoringMode: unset and unknown modes fall back to Flat so history and
// status.score use one formula; only SeverityWeighted opts into weights.
func TestScoringMode(t *testing.T) {
	if got := scoringMode(&baselinev1alpha1.ClusterBaseline{}); got != baselinev1alpha1.ScoringFlat {
		t.Fatalf("empty spec = %q, want Flat", got)
	}
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringFlat
	if got := scoringMode(cb); got != baselinev1alpha1.ScoringFlat {
		t.Fatalf("Flat = %q", got)
	}
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
	if got := scoringMode(cb); got != baselinev1alpha1.ScoringSeverityWeighted {
		t.Fatalf("SeverityWeighted = %q", got)
	}
	// Hand-edited / unknown enum value must not invent weights.
	cb.Spec.Scoring.Mode = "UnknownMode"
	if got := scoringMode(cb); got != baselinev1alpha1.ScoringFlat {
		t.Fatalf("unknown mode = %q, want Flat", got)
	}
}

// TestHistoryModeMatches: late CCR refresh must not rewrite history ring points
// under a different formula when the admin flips Flat <-> SeverityWeighted.
// Missing annotation (pre-feature CRs) allows refresh so existing clusters keep
// late-CCR correction; mismatched stamp blocks rewrite.
func TestHistoryModeMatches(t *testing.T) {
	// No annotation: allow refresh (pre-feature CR path).
	cb := &baselinev1alpha1.ClusterBaseline{}
	if !historyModeMatches(cb) {
		t.Fatal("missing annotation must allow late history refresh")
	}
	// Annotation matches current mode.
	cb.Annotations = map[string]string{historyScoringModeAnn: string(baselinev1alpha1.ScoringFlat)}
	if !historyModeMatches(cb) {
		t.Fatal("Flat stamp + Flat mode must match")
	}
	// Mode flipped after history was stamped: block rewrite under new formula.
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
	if historyModeMatches(cb) {
		t.Fatal("Flat stamp + SeverityWeighted mode must not match")
	}
	// Stamp matches weighted mode.
	cb.Annotations[historyScoringModeAnn] = string(baselinev1alpha1.ScoringSeverityWeighted)
	if !historyModeMatches(cb) {
		t.Fatal("SeverityWeighted stamp + mode must match")
	}
	// Empty annotation value is treated as missing (allow refresh).
	cb.Annotations[historyScoringModeAnn] = ""
	if !historyModeMatches(cb) {
		t.Fatal("empty stamp must allow refresh")
	}
	// Nil annotations map (distinct from empty string on present map).
	cb.Annotations = nil
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
	if !historyModeMatches(cb) {
		t.Fatal("nil annotations must allow refresh even when mode is weighted")
	}
}

func TestScore(t *testing.T) {
	if score(0, 0) != nil || score(-1, 0) != nil || score(1, -1) != nil {
		t.Fatal("zero/negative countable should be nil")
	}
	if s := score(2, 1); s == nil || *s != 66 {
		t.Fatalf("score(2,1) = %v, want 66", s)
	}
	if s := score(1, 0); s == nil || *s != 100 {
		t.Fatalf("score(1,0) = %v, want 100", s)
	}
	if s := score(0, 5); s == nil || *s != 0 {
		t.Fatalf("score(0,5) = %v, want 0", s)
	}
	// Integer division floors: a single FAIL among many PASS must not round up
	// to 100 (would hide the failure on the dashboard).
	if s := score(999, 1); s == nil || *s != 99 {
		t.Fatalf("score(999,1) = %v, want 99 (floor, not round)", s)
	}
	if s := score(1, 2); s == nil || *s != 33 {
		t.Fatalf("score(1,2) = %v, want 33", s)
	}
}

func TestClampScore(t *testing.T) {
	if clampScore(nil) != nil {
		t.Fatal("nil stays nil")
	}
	neg := int32(-1)
	if s := clampScore(&neg); s == nil || *s != 0 {
		t.Fatalf("neg = %v", s)
	}
	hi := int32(101)
	if s := clampScore(&hi); s == nil || *s != 100 {
		t.Fatalf("hi = %v", s)
	}
	ok := int32(77)
	if s := clampScore(&ok); s == nil || *s != 77 {
		t.Fatalf("ok = %v", s)
	}
}

func FuzzScore(f *testing.F) {
	f.Add(int32(0), int32(0))
	f.Add(int32(1), int32(0))
	f.Add(int32(0), int32(1))
	f.Add(int32(2), int32(1))
	f.Add(int32(-1), int32(5))
	f.Add(int32(2147483647), int32(0)) // int32-overflow regression seed
	f.Fuzz(func(t *testing.T, pass, fail int32) {
		s := score(pass, fail)
		// Oracle must use int64 sums (same as score) so int32 overflow is not expected nil.
		if pass < 0 || fail < 0 || int64(pass)+int64(fail) == 0 {
			if s != nil {
				t.Fatalf("expected nil for pass=%d fail=%d", pass, fail)
			}
			return
		}
		if s == nil {
			t.Fatal("expected non-nil")
		}
		if *s < 0 || *s > 100 {
			t.Fatalf("score %d out of range", *s)
		}
		want := int32(int64(pass) * 100 / (int64(pass) + int64(fail)))
		if *s != want {
			t.Fatalf("got %d want %d", *s, want)
		}
	})
}

// FuzzScore64: severity-weighted totals are int64 sums over cluster checks.
// Same invariants as score(): nil on non-positive totals, result in [0,100].
func FuzzScore64(f *testing.F) {
	f.Add(int64(0), int64(0))
	f.Add(int64(1), int64(0))
	f.Add(int64(0), int64(1))
	f.Add(int64(10), int64(5))
	f.Add(int64(-1), int64(5))
	f.Add(int64(1<<62), int64(1<<62))
	f.Fuzz(func(t *testing.T, pass, fail int64) {
		s := score64(pass, fail)
		if pass < 0 || fail < 0 || pass+fail == 0 {
			if s != nil {
				t.Fatalf("expected nil for pass=%d fail=%d", pass, fail)
			}
			return
		}
		if s == nil {
			t.Fatal("expected non-nil")
		}
		if *s < 0 || *s > 100 {
			t.Fatalf("score %d out of range", *s)
		}
		// When pass+fail can overflow int64 addition above, Go wraps; still
		// require the returned ratio uses the same arithmetic as production.
		want := int32(pass * 100 / (pass + fail))
		if *s != want {
			t.Fatalf("got %d want %d", *s, want)
		}
	})
}

// FuzzSeverityWeight: untrusted ComplianceCheckResult severity strings map to
// the product weight table and never panic.

func FuzzSeverityWeight(f *testing.F) {
	for _, seed := range []string{"", "high", "medium", "low", "unknown", "info", "HIGH", "x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, sev string) {
		w := severityWeight(sev)
		switch sev {
		case "high":
			if w != 10 {
				t.Fatalf("high weight = %d", w)
			}
		case "medium":
			if w != 5 {
				t.Fatalf("medium weight = %d", w)
			}
		case "low":
			if w != 2 {
				t.Fatalf("low weight = %d", w)
			}
		default:
			if w != 1 {
				t.Fatalf("default weight for %q = %d", sev, w)
			}
		}
	})
}

// FuzzClampScore: hand-edited or buggy status.score must stay in [0,100] or nil
// so Status().Update cannot fail CRD admission and freeze reconciliation.

func FuzzClampScore(f *testing.F) {
	f.Add(int32(0))
	f.Add(int32(100))
	f.Add(int32(-1))
	f.Add(int32(101))
	f.Add(int32(50))
	f.Fuzz(func(t *testing.T, v int32) {
		// nil path
		if clampScore(nil) != nil {
			t.Fatal("clampScore(nil) must be nil")
		}
		got := clampScore(&v)
		if got == nil {
			t.Fatal("clampScore(non-nil) returned nil")
		}
		if *got < 0 || *got > 100 {
			t.Fatalf("clamped score %d out of [0,100]", *got)
		}
		switch {
		case v < 0:
			if *got != 0 {
				t.Fatalf("negative %d clamped to %d, want 0", v, *got)
			}
		case v > 100:
			if *got != 100 {
				t.Fatalf("over %d clamped to %d, want 100", v, *got)
			}
		default:
			if *got != v {
				t.Fatalf("in-range %d mutated to %d", v, *got)
			}
		}
	})
}
