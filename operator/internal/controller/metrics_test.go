package controller

import (
	"testing"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
				Pass: 10, Fail: 2, Manual: 3, Info: 4, Error: 0, Inconsistent: 5, NotApplicable: 1,
			},
		},
	}
	cb.Status.TailoredProfiles = []baselinev1alpha1.TailoredProfileStatus{
		{Name: "custom", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 7, Fail: 1}},
	}
	publishMetrics(cb)

	if got := testutil.ToFloat64(complianceScore); got != 87 {
		t.Fatalf("score gauge = %v, want 87", got)
	}
	// Every published status series, including the ones alerting/dashboards read
	// (inconsistent, info) and the tailored tp:<name> label path.
	for _, tc := range []struct {
		profile, status string
		want            float64
	}{
		{"cis", "pass", 10}, {"cis", "fail", 2}, {"cis", "manual", 3},
		{"cis", "info", 4}, {"cis", "error", 0}, {"cis", "inconsistent", 5},
		{"cis", "notApplicable", 1}, {"tp:custom", "pass", 7}, {"tp:custom", "fail", 1},
	} {
		if got := testutil.ToFloat64(complianceChecks.WithLabelValues(tc.profile, tc.status)); got != tc.want {
			t.Fatalf("%s/%s = %v, want %v", tc.profile, tc.status, got, tc.want)
		}
	}

	// No score -> -1 sentinel; profiles cleared.
	cb.Status.Score = nil
	cb.Status.Profiles = nil
	cb.Status.TailoredProfiles = nil
	publishMetrics(cb)
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score gauge = %v, want -1", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks not reset: %d series remain", got)
	}
}
