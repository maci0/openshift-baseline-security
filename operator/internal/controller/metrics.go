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
		profile := string(p.Key)
		complianceChecks.WithLabelValues(profile, "pass").Set(float64(p.Pass))
		complianceChecks.WithLabelValues(profile, "fail").Set(float64(p.Fail))
		complianceChecks.WithLabelValues(profile, "manual").Set(float64(p.Manual))
		complianceChecks.WithLabelValues(profile, "error").Set(float64(p.Error))
		complianceChecks.WithLabelValues(profile, "notApplicable").Set(float64(p.NotApplicable))
	}
}
