package controller

import (
	"sync"
	"time"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Custom metrics served on the operator's secure metrics endpoint. They let
// cluster monitoring alert on compliance posture (see config/prometheus).
var (
	complianceScore = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "baseline_security_compliance_score",
		Help: "Overall compliance score 0-100 for the ClusterBaseline (Flat: pass/(pass+fail); SeverityWeighted: severity-weighted ratio). -1 when no score is available.",
	})
	complianceChecks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "baseline_security_checks",
		Help: "Number of compliance check results by profile and status.",
	}, []string{"profile", "status"})
	statusObservedTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "baseline_security_status_observed_timestamp_seconds",
		Help: "Unix timestamp when this operator replica last published ClusterBaseline status metrics.",
	})
	// 1 while status.remediationBatch is set (MachineConfigPools may be paused).
	// Alerts when stuck past batchResumeGrace + slack; operators must resume MCPs.
	remediationBatchActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "baseline_security_remediation_batch_active",
		Help: "1 when a remediation batch is in progress (MachineConfigPools may be paused), 0 otherwise.",
	})
	// Rollup conditions (Available / Progressing / Degraded). 1 when Status is
	// True, 0 when False or absent. Low cardinality (3 fixed types) so alerts can
	// fire on Degraded without scraping the CR API.
	conditionStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "baseline_security_condition",
		Help: "ClusterBaseline rollup condition: 1 if True, 0 if False or absent. Labels: type (Available|Progressing|Degraded).",
	}, []string{"type"})

	// Serialize publishMetrics so concurrent reconciles (or a future raise of
	// MaxConcurrentReconciles) cannot interleave Reset/Set sequences. Also track
	// the last published (profile, status) pairs so we can delete stale series
	// without GaugeVec.Reset(), which leaves scrapers a window of empty checks.
	metricsMu       sync.Mutex
	publishedChecks = map[[2]string]struct{}{}
)

// Rollup condition types published as gauges (must stay fixed; label cardinality).
var publishedConditionTypes = []string{"Available", "Progressing", "Degraded"}

func init() {
	metrics.Registry.MustRegister(complianceScore, complianceChecks, statusObservedTimestamp, remediationBatchActive, conditionStatus)
	// Seed the "no score yet" sentinel so a never-reconciled or
	// error-before-aggregation state reads as -1, not the gauge default of 0
	// (which the ComplianceScoreLow alert's `>= 0 and < 80` would treat as a
	// real low score).
	complianceScore.Set(-1)
	statusObservedTimestamp.Set(0)
	remediationBatchActive.Set(0)
	for _, typ := range publishedConditionTypes {
		conditionStatus.WithLabelValues(typ).Set(0)
	}
}

// publishMetrics reflects the aggregated status onto the Prometheus gauges.
// Call after setRollupConditions (and on the reconcile-error Degraded path) so
// condition gauges match the status about to be (or just) written.
func publishMetrics(cb *baselinev1alpha1.ClusterBaseline) {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	if cb.Status.Score != nil {
		complianceScore.Set(float64(*cb.Status.Score))
	} else {
		complianceScore.Set(-1)
	}

	// Set every desired series first, then delete only what dropped out. A
	// concurrent scrape may briefly see both old and new profile labels, but
	// never a full Reset() gap of zero check series (which would under-report
	// fail counts to alerts/dashboards).
	desired := make(map[[2]string]struct{})
	for _, p := range cb.Status.Profiles {
		setCheckCounts(string(p.Key), p.ResultCounts, desired)
	}
	for _, tp := range cb.Status.TailoredProfiles {
		setCheckCounts("tp:"+tp.Name, tp.ResultCounts, desired)
	}
	for key := range publishedChecks {
		if _, ok := desired[key]; !ok {
			complianceChecks.DeleteLabelValues(key[0], key[1])
		}
	}
	publishedChecks = desired

	if cb.Status.RemediationBatch != nil {
		remediationBatchActive.Set(1)
	} else {
		remediationBatchActive.Set(0)
	}

	for _, typ := range publishedConditionTypes {
		c := meta.FindStatusCondition(cb.Status.Conditions, typ)
		v := 0.0
		if c != nil && c.Status == metav1.ConditionTrue {
			v = 1.0
		}
		conditionStatus.WithLabelValues(typ).Set(v)
	}

	// Publish freshness last so a concurrent scrape cannot select this replica as
	// newest before its score and check gauges have been refreshed.
	statusObservedTimestamp.Set(float64(time.Now().UnixNano()) / 1e9)
}

func setCheckCounts(profile string, c baselinev1alpha1.ResultCounts, desired map[[2]string]struct{}) {
	for _, s := range []struct {
		status string
		v      float64
	}{
		{"pass", float64(c.Pass)},
		{"fail", float64(c.Fail)},
		{"manual", float64(c.Manual)},
		{"info", float64(c.Info)},
		{"error", float64(c.Error)},
		{"inconsistent", float64(c.Inconsistent)},
		{"waived", float64(c.Waived)},
		{"notApplicable", float64(c.NotApplicable)},
	} {
		complianceChecks.WithLabelValues(profile, s.status).Set(s.v)
		desired[[2]string{profile, s.status}] = struct{}{}
	}
}
