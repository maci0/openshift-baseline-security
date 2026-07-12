package controller

import (
	"context"
	_ "embed"
	"maps"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

//go:embed assets/compliance-dashboard.json
var complianceDashboardJSON string

const dashboardJSONKey = "baseline-security-compliance.json"

// ensureComplianceDashboard creates the score-trend dashboard as a ConfigMap in
// openshift-config-managed labeled console.openshift.io/dashboard, so the console
// renders it under Observe -> Dashboards (no Grafana). Data needs user-workload
// monitoring + the metrics ServiceMonitor; the dashboard renders regardless.
// Best-effort: a write failure here is logged, not Degrading, since the dashboard
// is cosmetic and must never block scanning or status.
//
// Steady-state reconcilers hit this every poll. When the ConfigMap already has
// the embedded JSON, labels, and our owner ref, skip CreateOrUpdate (avoids a
// full Semantic.DeepEqual of ~6KB JSON every minute).
func (r *ClusterBaselineReconciler) ensureComplianceDashboard(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) {
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: dashboardName, Namespace: dashboardNS}, existing)
	if err == nil && dashboardConfigMapCurrent(existing, cb) {
		return
	}
	if err != nil && !apierrors.IsNotFound(err) {
		log.FromContext(ctx).Error(err, "compliance dashboard configmap get failed",
			"namespace", dashboardNS, "name", dashboardName)
		// Fall through to CreateOrUpdate: a transient Get error must not skip
		// repair when the object is wrong or missing.
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: dashboardName, Namespace: dashboardNS}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		// Clone before mutate: do not edit a label map shared with a cache entry.
		labels := maps.Clone(cm.Labels)
		if labels == nil {
			labels = map[string]string{}
		}
		labels["console.openshift.io/dashboard"] = "true"
		labels["app.kubernetes.io/part-of"] = "baseline-security"
		cm.Labels = labels
		cm.Data = map[string]string{dashboardJSONKey: complianceDashboardJSON}
		// cb is cluster-scoped, so a namespaced dependent in another namespace is a
		// valid ownerref target; the ConfigMap is GCed when the CR is deleted.
		return controllerutil.SetControllerReference(cb, cm, r.Scheme)
	})
	if err != nil {
		// Error (not Info): best-effort cosmetic resource, but operators need to
		// see RBAC/namespace failures when the dashboard never appears.
		log.FromContext(ctx).Error(err, "compliance dashboard configmap not reconciled",
			"namespace", dashboardNS, "name", dashboardName)
	}
}

// dashboardConfigMapCurrent is true when the ConfigMap already carries the
// embedded dashboard payload, console labels, and ownership by this baseline.
func dashboardConfigMapCurrent(cm *corev1.ConfigMap, cb *baselinev1alpha1.ClusterBaseline) bool {
	if cm.Labels["console.openshift.io/dashboard"] != "true" {
		return false
	}
	if cm.Labels["app.kubernetes.io/part-of"] != "baseline-security" {
		return false
	}
	if cm.Data[dashboardJSONKey] != complianceDashboardJSON {
		return false
	}
	// Owner ref required so CR deletion still GCs the dashboard. Missing or
	// wrong UID means CreateOrUpdate must re-apply SetControllerReference.
	for _, ref := range cm.OwnerReferences {
		if ref.UID == cb.UID && ref.UID != "" {
			return true
		}
	}
	return false
}
