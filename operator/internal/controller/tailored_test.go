package controller

import (
	"context"
	"testing"
	"time"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTailoredSuiteHelpers(t *testing.T) {
	if got := tailoredBindingName("custom"); got != "baseline-tp-custom" {
		t.Fatalf("tailoredBindingName = %q", got)
	}
	for suite, want := range map[string]string{
		"baseline-tp-custom": "custom",
		"baseline-cis":       "", // built-in, not tailored
		"baseline-tp-":       "", // empty name rejected
		"other":              "",
	} {
		got, ok := tailoredNameFromSuite(suite)
		if (want == "") == ok {
			t.Errorf("tailoredNameFromSuite(%q) ok=%v", suite, ok)
		}
		if ok && got != want {
			t.Errorf("tailoredNameFromSuite(%q) = %q, want %q", suite, got, want)
		}
	}
}

func TestAggregateStatusWithTailored(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("a", "baseline-cis", "PASS"),
			checkResult("b", "baseline-cis", "FAIL"),
			checkResult("c", "baseline-tp-custom", "PASS"),
			checkResult("d", "baseline-tp-custom", "PASS"),
			checkResult("e", "baseline-cis", "INFO"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles:         []baselinev1alpha1.ProfileKey{"cis"},
			TailoredProfiles: []string{"custom"},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	// Overall score counts cis (1/2) + tailored (2/2) = 3 pass / 1 fail = 75.
	// INFO is tallied but excluded from score.
	if cb.Status.Score == nil || *cb.Status.Score != 75 {
		t.Fatalf("score = %v, want 75", cb.Status.Score)
	}
	if len(cb.Status.Profiles) != 1 || cb.Status.Profiles[0].Info != 1 {
		t.Fatalf("cis info count = %+v, want Info=1", cb.Status.Profiles)
	}
	if len(cb.Status.TailoredProfiles) != 1 || cb.Status.TailoredProfiles[0].Name != "custom" ||
		cb.Status.TailoredProfiles[0].Pass != 2 {
		t.Fatalf("tailored status = %+v", cb.Status.TailoredProfiles)
	}
	if len(cb.Status.RelatedObjects) == 0 {
		t.Fatal("relatedObjects not populated")
	}
	if cb.Status.NextScanTime == nil {
		t.Fatal("nextScanTime not set")
	}
}

func TestNextScanTime(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	// Daily at 01:00 -> next is tomorrow 01:00.
	next := nextScanTime("0 1 * * *", now)
	if next == nil {
		t.Fatal("nil for valid schedule")
	}
	want := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	if !next.Time.Equal(want) {
		t.Fatalf("next = %v, want %v", next.Time, want)
	}
	if nextScanTime("not a cron", now) != nil {
		t.Fatal("invalid schedule should yield nil")
	}
	// Empty falls back to the default daily schedule (non-nil).
	if nextScanTime("", now) == nil {
		t.Fatal("empty schedule should use default")
	}
}
