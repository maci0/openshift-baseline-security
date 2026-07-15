package controller

import (
	"sync"
	"testing"
	"time"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// resetMetrics clears package gauges so tests do not depend on execution order
// under `go test -shuffle=on` (shared Prometheus state across Test* in this file).
func resetMetrics(t *testing.T) {
	t.Helper()
	clearPublishedMetrics()
}

// The gauge must read the -1 sentinel before any publish, so a pre-aggregation
// error state does not look like a real low score to alerting.
func TestComplianceScoreSeededSentinel(t *testing.T) {
	resetMetrics(t)
	// init() seeds -1; publishMetrics with a nil score keeps it there.
	cb := &baselinev1alpha1.ClusterBaseline{}
	publishMetrics(cb)
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("unpublished score gauge = %v, want -1 sentinel", got)
	}
}

func TestPublishMetrics(t *testing.T) {
	resetMetrics(t)
	cb := &baselinev1alpha1.ClusterBaseline{}
	// Spec profiles selected so last-scan freshness is published (scanning on).
	cb.Spec.Profiles = []baselinev1alpha1.ProfileKey{"cis"}
	cb.Spec.TailoredProfiles = []string{"custom"}
	cb.Spec.Schedule = "0 * * * *" // hourly -> 3600s scan interval

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
	// Fixed epoch so the last-scan gauge assert is deterministic.
	scanAt := metav1.NewTime(time.Unix(1_700_000_000, 0))
	cb.Status.LastScanTime = &scanAt
	cb.Status.NewlyFailed = []string{"check-a", "check-b"}
	publishMetrics(cb)

	if got := testutil.ToFloat64(complianceScore); got != 87 {
		t.Fatalf("score gauge = %v, want 87", got)
	}
	if got := testutil.ToFloat64(statusObservedTimestamp); got <= 0 || time.Now().Unix()-int64(got) > 60 {
		t.Fatalf("status observation timestamp = %v, want ~now Unix time (a stale constant must not pass)", got)
	}
	if got := testutil.ToFloat64(remediationBatchActive); got != 1 {
		t.Fatalf("batch active gauge = %v, want 1", got)
	}
	// Batch StartedAt is zero in this fixture; started-at gauge must be 0.
	if got := testutil.ToFloat64(remediationBatchStartedTimestamp); got != 0 {
		t.Fatalf("batch started timestamp with zero StartedAt = %v, want 0", got)
	}
	if got := testutil.ToFloat64(lastScanTimestamp); got != 1_700_000_000 {
		t.Fatalf("last scan timestamp = %v, want 1700000000", got)
	}
	if got := testutil.ToFloat64(newlyFailedCount); got != 2 {
		t.Fatalf("newly failed count = %v, want 2", got)
	}
	// Scan interval gauge tracks the (hourly) schedule while scanning is on.
	if got := testutil.ToFloat64(scanIntervalSecondsGauge); got != 3600 {
		t.Fatalf("scan interval gauge = %v, want 3600", got)
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

	// No score -> -1 sentinel; profiles/scan/regressions cleared; batch cleared; conditions cleared.
	cb.Status.Score = nil
	cb.Status.Profiles = nil
	cb.Status.TailoredProfiles = nil
	cb.Status.RemediationBatch = nil
	cb.Status.Conditions = nil
	cb.Status.LastScanTime = nil
	cb.Status.NewlyFailed = nil
	publishMetrics(cb)
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score gauge = %v, want -1", got)
	}
	if got := testutil.ToFloat64(lastScanTimestamp); got != 0 {
		t.Fatalf("last scan timestamp after clear = %v, want 0", got)
	}
	// Scanning disabled: last-scan gauge is 0 even if status still holds a
	// historical LastScanTime (UI keeps it; ComplianceScanStale must not fire).
	cb.Spec.Profiles = nil
	cb.Spec.TailoredProfiles = nil
	cb.Status.LastScanTime = &scanAt
	publishMetrics(cb)
	if got := testutil.ToFloat64(lastScanTimestamp); got != 0 {
		t.Fatalf("last scan timestamp while scanning disabled = %v, want 0", got)
	}
	// Interval gauge also drops to 0 so ComplianceScanStale cannot fire on a
	// leftover schedule once scanning is off.
	if got := testutil.ToFloat64(scanIntervalSecondsGauge); got != 0 {
		t.Fatalf("scan interval gauge while scanning disabled = %v, want 0", got)
	}
	if got := testutil.ToFloat64(newlyFailedCount); got != 0 {
		t.Fatalf("newly failed after clear = %v, want 0", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks not reset: %d series remain", got)
	}
	if got := testutil.ToFloat64(remediationBatchActive); got != 0 {
		t.Fatalf("batch active gauge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(remediationBatchStartedTimestamp); got != 0 {
		t.Fatalf("batch started timestamp after clear = %v, want 0", got)
	}
	for _, typ := range publishedConditionTypes {
		if got := testutil.ToFloat64(conditionStatus.WithLabelValues(typ)); got != 0 {
			t.Fatalf("%s condition gauge = %v, want 0 after clear", typ, got)
		}
	}
}

// Batch started-at gauge tracks StartedAt so dashboards can show pause age
// without scraping the CR; clears with the batch.
func TestPublishMetricsBatchStartedTimestamp(t *testing.T) {
	resetMetrics(t)
	started := metav1.NewTime(time.Unix(1_700_000_100, 0))
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase:     baselinev1alpha1.RemediationBatchPhaseApplying,
		StartedAt: started,
	}
	publishMetrics(cb)
	if got := testutil.ToFloat64(remediationBatchActive); got != 1 {
		t.Fatalf("batch active = %v, want 1", got)
	}
	if got := testutil.ToFloat64(remediationBatchStartedTimestamp); got != 1_700_000_100 {
		t.Fatalf("batch started timestamp = %v, want 1700000100", got)
	}
	cb.Status.RemediationBatch = nil
	publishMetrics(cb)
	if got := testutil.ToFloat64(remediationBatchStartedTimestamp); got != 0 {
		t.Fatalf("batch started timestamp after finish = %v, want 0", got)
	}
}

// TestPublishMetricsDropsRemovedProfile: when a profile leaves status (admin
// removed it from spec and aggregate no longer reports it), its label series
// must not linger for alerts/dashboards after the next publish.
func TestPublishMetricsDropsRemovedProfile(t *testing.T) {
	resetMetrics(t)
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Status.Score = ptr.To(int32(90))
	cb.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 10, Fail: 1}},
		{Key: "pci-dss", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 8, Fail: 2}},
	}
	cb.Status.TailoredProfiles = []baselinev1alpha1.TailoredProfileStatus{
		{Name: "custom", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 3, Fail: 0}},
	}
	publishMetrics(cb)
	// Two built-ins + one tailored × eight statuses = 24 series.
	if got := testutil.CollectAndCount(complianceChecks); got != 24 {
		t.Fatalf("check series before drop = %d, want 24", got)
	}
	if got := testutil.ToFloat64(complianceChecks.WithLabelValues("cis", "pass")); got != 10 {
		t.Fatalf("cis.pass before drop = %v, want 10", got)
	}
	if got := testutil.ToFloat64(complianceChecks.WithLabelValues("tp:custom", "pass")); got != 3 {
		t.Fatalf("tp:custom.pass before drop = %v, want 3", got)
	}

	// Only pci-dss remains; cis and tailored series must be deleted.
	cb.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "pci-dss", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 8, Fail: 2}},
	}
	cb.Status.TailoredProfiles = nil
	publishMetrics(cb)

	// CollectAndCount is the only safe stale-label check: WithLabelValues on a
	// deleted series would recreate it at 0 and pollute subsequent tests.
	if got := testutil.CollectAndCount(complianceChecks); got != 8 {
		t.Fatalf("check series after profile drop = %d, want 8 (one profile × eight statuses)", got)
	}
	if got := testutil.ToFloat64(complianceChecks.WithLabelValues("pci-dss", "pass")); got != 8 {
		t.Fatalf("pci-dss.pass after drop = %v, want 8", got)
	}
	if got := testutil.ToFloat64(complianceChecks.WithLabelValues("pci-dss", "fail")); got != 2 {
		t.Fatalf("pci-dss.fail after drop = %v, want 2", got)
	}
	// Still exactly one profile's series (pci-dss reads above must not have
	// reintroduced cis/tp labels via WithLabelValues on deleted keys).
	if got := testutil.CollectAndCount(complianceChecks); got != 8 {
		t.Fatalf("check series polluted after read = %d, want 8", got)
	}
}

// Concurrent publishMetrics must not panic or leave a torn check series set
// after both writers finish (mutex + set-then-delete, not Reset).
func TestPublishMetricsConcurrent(t *testing.T) {
	resetMetrics(t)
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
	clearPublishedMetrics()
}

// clearPublishedMetrics must drop score/fail series so alerts cannot stick after
// CR delete, while still advancing the observation timestamp (not StatusStale).
func TestClearPublishedMetrics(t *testing.T) {
	resetMetrics(t)
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Status.Score = ptr.To(int32(42))
	cb.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Fail: 3}},
	}
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{Phase: "Applying"}
	setCond(cb, "Degraded", metav1.ConditionTrue, "ReconcileError", "boom")
	publishMetrics(cb)

	clearPublishedMetrics()

	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score after clear = %v, want -1", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks after clear: %d series remain", got)
	}
	if got := testutil.ToFloat64(remediationBatchActive); got != 0 {
		t.Fatalf("batch active after clear = %v, want 0", got)
	}
	if got := testutil.ToFloat64(remediationBatchStartedTimestamp); got != 0 {
		t.Fatalf("batch started timestamp after clear = %v, want 0", got)
	}
	if got := testutil.ToFloat64(conditionStatus.WithLabelValues("Degraded")); got != 0 {
		t.Fatalf("Degraded after clear = %v, want 0", got)
	}
	if got := testutil.ToFloat64(lastScanTimestamp); got != 0 {
		t.Fatalf("last scan after clear = %v, want 0", got)
	}
	if got := testutil.ToFloat64(newlyFailedCount); got != 0 {
		t.Fatalf("newly failed after clear = %v, want 0", got)
	}
	if got := testutil.ToFloat64(statusObservedTimestamp); got <= 0 || time.Now().Unix()-int64(got) > 60 {
		t.Fatalf("observation timestamp after clear = %v, want ~now (avoid StatusStale; stale constant must not pass)", got)
	}
}
