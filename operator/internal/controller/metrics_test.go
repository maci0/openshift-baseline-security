package controller

import (
	"testing"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/utils/ptr"
)

func TestPublishMetrics(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Status.Score = ptr.To(int32(87))
	cb.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "cis", Pass: 10, Fail: 2, Manual: 3, Error: 0, NotApplicable: 1},
	}
	publishMetrics(cb)

	if got := testutil.ToFloat64(complianceScore); got != 87 {
		t.Fatalf("score gauge = %v, want 87", got)
	}
	if got := testutil.ToFloat64(complianceChecks.WithLabelValues("cis", "fail")); got != 2 {
		t.Fatalf("cis/fail = %v, want 2", got)
	}
	if got := testutil.ToFloat64(complianceChecks.WithLabelValues("cis", "pass")); got != 10 {
		t.Fatalf("cis/pass = %v, want 10", got)
	}

	// No score -> -1 sentinel; profiles cleared.
	cb.Status.Score = nil
	cb.Status.Profiles = nil
	publishMetrics(cb)
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score gauge = %v, want -1", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks not reset: %d series remain", got)
	}
}
