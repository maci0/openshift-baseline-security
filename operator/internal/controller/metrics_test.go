package controller

import (
	"sync"
	"testing"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// The gauge must read the -1 sentinel before any publish, so a pre-aggregation
// error state does not look like a real low score to alerting.
func TestComplianceScoreSeededSentinel(t *testing.T) {
	// init() seeds -1; publishMetrics with a nil score keeps it there.
	cb := &baselinev1alpha1.ClusterBaseline{}
	publishMetrics(cb)
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("unpublished score gauge = %v, want -1 sentinel", got)
	}
}

func TestPublishMetrics(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Status.Score = ptr.To(int32(87))
	cb.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{
			Key: "cis",
			ResultCounts: baselinev1alpha1.ResultCounts{
				Pass: 10, Fail: 2, Manual: 3, Info: 4, Error: 0, Inconsistent: 5, Waived: 6, NotApplicable: 1,
			},
		},
	}
	cb.Status.TailoredProfiles = []baselinev1alpha1.TailoredProfileStatus{
		{Name: "custom", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 7, Fail: 1}},
	}
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{Phase: "Applying"}
	publishMetrics(cb)

	if got := testutil.ToFloat64(complianceScore); got != 87 {
		t.Fatalf("score gauge = %v, want 87", got)
	}
	if got := testutil.ToFloat64(statusObservedTimestamp); got <= 0 {
		t.Fatalf("status observation timestamp = %v, want positive Unix time", got)
	}
	if got := testutil.ToFloat64(remediationBatchActive); got != 1 {
		t.Fatalf("batch active gauge = %v, want 1", got)
	}
	// Condition gauges: True -> 1, False/absent -> 0.
	setCond(cb, "Available", metav1.ConditionTrue, "AsExpected", "")
	setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	setCond(cb, "Degraded", metav1.ConditionTrue, "InvalidSchedule", "bad cron")
	publishMetrics(cb)
	if got := testutil.ToFloat64(conditionStatus.WithLabelValues("Available")); got != 1 {
		t.Fatalf("Available condition gauge = %v, want 1", got)
	}
	if got := testutil.ToFloat64(conditionStatus.WithLabelValues("Progressing")); got != 0 {
		t.Fatalf("Progressing condition gauge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(conditionStatus.WithLabelValues("Degraded")); got != 1 {
		t.Fatalf("Degraded condition gauge = %v, want 1", got)
	}
	// Every published status series, including the ones alerting/dashboards read
	// (inconsistent, info) and the tailored tp:<name> label path.
	for _, tc := range []struct {
		profile, status string
		want            float64
	}{
		{"cis", "pass", 10}, {"cis", "fail", 2}, {"cis", "manual", 3},
		{"cis", "info", 4}, {"cis", "error", 0}, {"cis", "inconsistent", 5},
		{"cis", "waived", 6}, {"cis", "notApplicable", 1},
		{"tp:custom", "pass", 7}, {"tp:custom", "fail", 1},
	} {
		if got := testutil.ToFloat64(complianceChecks.WithLabelValues(tc.profile, tc.status)); got != tc.want {
			t.Fatalf("%s/%s = %v, want %v", tc.profile, tc.status, got, tc.want)
		}
	}

	// No score -> -1 sentinel; profiles cleared; batch cleared; conditions cleared.
	cb.Status.Score = nil
	cb.Status.Profiles = nil
	cb.Status.TailoredProfiles = nil
	cb.Status.RemediationBatch = nil
	cb.Status.Conditions = nil
	publishMetrics(cb)
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score gauge = %v, want -1", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks not reset: %d series remain", got)
	}
	if got := testutil.ToFloat64(remediationBatchActive); got != 0 {
		t.Fatalf("batch active gauge = %v, want 0", got)
	}
	for _, typ := range publishedConditionTypes {
		if got := testutil.ToFloat64(conditionStatus.WithLabelValues(typ)); got != 0 {
			t.Fatalf("%s condition gauge = %v, want 0 after clear", typ, got)
		}
	}
}

// Concurrent publishMetrics must not panic or leave a torn check series set
// after both writers finish (mutex + set-then-delete, not Reset).
func TestPublishMetricsConcurrent(t *testing.T) {
	a := &baselinev1alpha1.ClusterBaseline{}
	a.Status.Score = ptr.To(int32(90))
	a.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 9, Fail: 1}},
	}
	b := &baselinev1alpha1.ClusterBaseline{}
	b.Status.Score = ptr.To(int32(80))
	b.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "pci-dss", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 8, Fail: 2}},
	}
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				publishMetrics(a)
			} else {
				publishMetrics(b)
			}
		}()
	}
	wg.Wait()
	// One of the two final states must win entirely: 8 series (one profile ×
	// eight statuses), never 16 mixed cis+pci leftovers from a torn Reset/Set.
	if got := testutil.CollectAndCount(complianceChecks); got != 8 {
		t.Fatalf("check series after concurrent publish = %d, want 8 (single profile)", got)
	}
	score := testutil.ToFloat64(complianceScore)
	switch score {
	case 90:
		if got := testutil.ToFloat64(complianceChecks.WithLabelValues("cis", "pass")); got != 9 {
			t.Fatalf("cis.pass = %v, want 9", got)
		}
	case 80:
		if got := testutil.ToFloat64(complianceChecks.WithLabelValues("pci-dss", "pass")); got != 8 {
			t.Fatalf("pci-dss.pass = %v, want 8", got)
		}
	default:
		t.Fatalf("score after concurrent publish = %v, want 90 or 80", score)
	}
	// Leave package gauges in the same cleared state as TestPublishMetrics end.
	publishMetrics(&baselinev1alpha1.ClusterBaseline{})
}
