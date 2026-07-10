package controller

import (
	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Custom metrics served on the operator's secure metrics endpoint. They let
// cluster monitoring alert on compliance posture (see config/prometheus).
var (
	complianceScore = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "baseline_security_compliance_score",
		Help: "Overall compliance score (0-100, pass/(pass+fail)) for the ClusterBaseline. -1 when no score is available.",
	})
	complianceChecks = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "baseline_security_checks",
		Help: "Number of compliance check results by profile and status.",
	}, []string{"profile", "status"})
)

func init() {
	metrics.Registry.MustRegister(complianceScore, complianceChecks)
	// Seed the "no score yet" sentinel so a never-reconciled or
	// error-before-aggregation state reads as -1, not the gauge default of 0
	// (which the ComplianceScoreLow alert's `>= 0 and < 80` would treat as a
	// real low score).
	complianceScore.Set(-1)
}

// publishMetrics reflects the aggregated status onto the Prometheus gauges.
// Called after each aggregation so the metrics track the CR status exactly.
func publishMetrics(cb *baselinev1alpha1.ClusterBaseline) {
	if cb.Status.Score != nil {
		complianceScore.Set(float64(*cb.Status.Score))
	} else {
		complianceScore.Set(-1)
	}
	// Reset so profiles/statuses that dropped to zero (or profiles removed from
	// spec) do not linger with stale values.
	complianceChecks.Reset()
	for _, p := range cb.Status.Profiles {
		setCheckCounts(string(p.Key), p.ResultCounts)
	}
	for _, tp := range cb.Status.TailoredProfiles {
		setCheckCounts("tp:"+tp.Name, tp.ResultCounts)
	}
}

func setCheckCounts(profile string, c baselinev1alpha1.ResultCounts) {
	complianceChecks.WithLabelValues(profile, "pass").Set(float64(c.Pass))
	complianceChecks.WithLabelValues(profile, "fail").Set(float64(c.Fail))
	complianceChecks.WithLabelValues(profile, "manual").Set(float64(c.Manual))
	complianceChecks.WithLabelValues(profile, "error").Set(float64(c.Error))
	complianceChecks.WithLabelValues(profile, "inconsistent").Set(float64(c.Inconsistent))
	complianceChecks.WithLabelValues(profile, "notApplicable").Set(float64(c.NotApplicable))
}
