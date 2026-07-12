package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func (r *ClusterBaselineReconciler) ensureComplianceOperator(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	sub := u(subscriptionGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if err == nil {
		// Keep catalog source in sync when we manage install. createIfMissing only
		// writes the Subscription once; without this, changing
		// spec.complianceCatalogSource (OKD / disconnected) is a silent no-op.
		if cb.Spec.InstallComplianceOperator != baselinev1alpha1.InstallManual {
			if err := r.syncComplianceSubscriptionSource(ctx, cb, sub); err != nil {
				return fmt.Errorf("syncing compliance-operator Subscription source: %w", err)
			}
		}
		// Always evaluate readiness, including InstallManual, so Available cannot
		// stay True after CO is removed.
		return r.setComplianceOperatorReady(ctx, cb, sub)
	}
	if meta.IsNoMatchError(err) {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled",
			"OLM Subscription API not available")
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting compliance-operator Subscription: %w", err)
	}

	csv, err := r.findComplianceOperatorCSV(ctx)
	if err != nil {
		return fmt.Errorf("finding compliance-operator CSV: %w", err)
	}
	if csv != nil {
		setComplianceOperatorReadyFromCSV(cb, csv)
		return nil
	}

	if cb.Spec.InstallComplianceOperator == baselinev1alpha1.InstallManual {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled",
			"compliance-operator Subscription not found; install manually or set installComplianceOperator=Automatic")
		return nil
	}

	if err := createIfMissing(ctx, r.Client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: complianceNamespace}}); err != nil {
		return fmt.Errorf("ensuring compliance namespace %s: %w", complianceNamespace, err)
	}
	// Must keep targetNamespaces set: createIfMissing alone leaves a pre-existing
	// empty OperatorGroup untouched, and empty targetNamespaces installs CO cluster-wide.
	if err := r.ensureComplianceOperatorGroup(ctx); err != nil {
		return err
	}

	sub = u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": desiredComplianceCatalogSource(cb), "sourceNamespace": "openshift-marketplace",
	}
	if err := createIfMissing(ctx, r.Client, sub); err != nil {
		return fmt.Errorf("ensuring compliance-operator Subscription: %w", err)
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV")
	return nil
}

// ensureComplianceOperatorGroup creates or updates the compliance-operator
// OperatorGroup so targetNamespaces is exactly openshift-compliance. create-only
// would leave a pre-existing empty OG (cluster-wide install) as a silent hazard.
func (r *ClusterBaselineReconciler) ensureComplianceOperatorGroup(ctx context.Context) error {
	og := u(operatorGroupGVK)
	og.SetName("compliance-operator")
	og.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, og, func() error {
		return unstructured.SetNestedStringSlice(og.Object, []string{complianceNamespace}, "spec", "targetNamespaces")
	})
	if err != nil {
		return fmt.Errorf("ensuring OperatorGroup targetNamespaces: %w", err)
	}
	return nil
}

// desiredComplianceCatalogSource is the OLM CatalogSource name for the CO
// Subscription (default redhat-operators).
func desiredComplianceCatalogSource(cb *baselinev1alpha1.ClusterBaseline) string {
	if s := cb.Spec.ComplianceCatalogSource; s != "" {
		return s
	}
	return "redhat-operators"
}

// syncComplianceSubscriptionSource updates an existing Subscription's
// spec.source when it diverges from the CR. No-op when already matched.
// Retries on conflict: OLM and other controllers race Subscription updates, and
// a single failed Update would Degrade the whole reconcile for a catalog move.
func (r *ClusterBaselineReconciler) syncComplianceSubscriptionSource(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, sub *unstructured.Unstructured,
) error {
	desired := desiredComplianceCatalogSource(cb)
	current, _, err := unstructured.NestedString(sub.Object, "spec", "source")
	if err != nil {
		return fmt.Errorf("reading Subscription spec.source: %w", err)
	}
	if current == desired {
		return nil
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := u(subscriptionGVK)
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: complianceNamespace, Name: "compliance-operator",
		}, latest); err != nil {
			return err
		}
		cur, _, err := unstructured.NestedString(latest.Object, "spec", "source")
		if err != nil {
			return fmt.Errorf("reading Subscription spec.source: %w", err)
		}
		if cur == desired {
			return nil
		}
		if err := unstructured.SetNestedField(latest.Object, desired, "spec", "source"); err != nil {
			return err
		}
		return r.Update(ctx, latest)
	}); err != nil {
		return err
	}
	// Catalog moves (OKD / disconnected) change CO install resolution; without
	// this log, a stuck Installing condition has no marker that source flipped.
	log.FromContext(ctx).Info("synced compliance-operator Subscription catalog source",
		"from", current, "to", desired, "name", cb.Name)
	return nil
}

func (r *ClusterBaselineReconciler) findComplianceOperatorCSV(ctx context.Context) (*unstructured.Unstructured, error) {
	// Priority (newest version within each tier):
	//  1. Succeeded in openshift-compliance (where we install / Get installedCSV)
	//  2. Succeeded anywhere (manual install in another NS)
	//  3. Non-Succeeded in openshift-compliance
	//  4. Non-Succeeded anywhere
	// Tiering avoids two attacks: (a) stale high-version Succeeded leftovers in a
	// foreign NS beating the live local CSV; (b) a local Failed/Installing remnant
	// hiding a healthy Succeeded CSV elsewhere.
	//
	// Common path: Succeeded CSV already in openshift-compliance. List that
	// namespace first so every reconcile does not pull cluster-wide CSVs (can be
	// large on multi-operator clusters). Fall back to a full list only when
	// local Succeeded is absent.
	local := uList(csvGVK)
	if err := r.List(ctx, local, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing CSVs in %s: %w", complianceNamespace, err)
	}
	if csv := pickComplianceOperatorCSV(local.Items, complianceNamespace, true); csv != nil {
		return csv, nil
	}

	csvs := uList(csvGVK)
	if err := r.List(ctx, csvs); err != nil {
		if meta.IsNoMatchError(err) {
			// CRD still present for namespaced list; only non-Succeeded local remains.
			return pickComplianceOperatorCSV(local.Items, complianceNamespace, false), nil
		}
		return nil, fmt.Errorf("listing CSVs cluster-wide: %w", err)
	}
	if csv := pickComplianceOperatorCSV(csvs.Items, "", true); csv != nil {
		return csv, nil
	}
	if csv := pickComplianceOperatorCSV(local.Items, complianceNamespace, false); csv != nil {
		return csv, nil
	}
	return pickComplianceOperatorCSV(csvs.Items, "", false), nil
}

// pickComplianceOperatorCSV chooses the newest compliance-operator CSV among items.
// If ns is non-empty, only that namespace is considered. If succeededOnly, only
// phase=Succeeded CSVs are candidates; otherwise only non-Succeeded.
// DeepCopy runs once for the winner so candidate comparisons stay cheap.
func pickComplianceOperatorCSV(items []unstructured.Unstructured, ns string, succeededOnly bool) *unstructured.Unstructured {
	bestIdx := -1
	for i := range items {
		csv := &items[i]
		if ns != "" && csv.GetNamespace() != ns {
			continue
		}
		if !strings.HasPrefix(csv.GetName(), "compliance-operator.v") {
			continue
		}
		phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
		isSucceeded := phase == "Succeeded"
		if succeededOnly != isSucceeded {
			continue
		}
		if bestIdx < 0 || compareComplianceCSVVersion(csv.GetName(), items[bestIdx].GetName()) > 0 {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return nil
	}
	return items[bestIdx].DeepCopy()
}

func (r *ClusterBaselineReconciler) setComplianceOperatorReady(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, sub *unstructured.Unstructured) error {
	csvName, _, _ := unstructured.NestedString(sub.Object, "status", "installedCSV")
	if csvName == "" {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "installedCSV empty")
		return nil
	}

	csv := u(csvGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: csvName}, csv); err != nil {
		if apierrors.IsNotFound(err) {
			cb.Status.ComplianceOperatorVersion = ""
			setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV "+csvName)
			return nil
		}
		return fmt.Errorf("getting CSV %s/%s: %w", complianceNamespace, csvName, err)
	}
	setComplianceOperatorReadyFromCSV(cb, csv)
	return nil
}

func setComplianceOperatorReadyFromCSV(cb *baselinev1alpha1.ClusterBaseline, csv *unstructured.Unstructured) {
	phase, _, err := unstructured.NestedString(csv.Object, "status", "phase")
	// Wrong-type or missing phase must not report "phase=" (empty); treat as unknown.
	if err != nil || phase == "" {
		phase = "unknown"
	}
	if phase == "Succeeded" {
		cb.Status.ComplianceOperatorVersion = strings.TrimPrefix(csv.GetName(), "compliance-operator.v")
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionTrue, "CSVSucceeded", "")
		return
	}
	// Keep version empty until Succeeded so the UI does not show a green-looking
	// version string while the CSV is still Installing/Failed.
	cb.Status.ComplianceOperatorVersion = ""
	// Failed is terminal (not install progress); rollup marks Degraded.
	if phase == "Failed" {
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVFailed", "phase=Failed")
		return
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", "phase="+phase)
}

// setScanCRDsMissing marks ScanConfigured false when the compliance.openshift.io
// CRDs are absent (no REST mapping), so a missing Compliance Operator degrades
// gracefully instead of erroring the reconcile.
func setScanCRDsMissing(cb *baselinev1alpha1.ClusterBaseline) {
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing",
		"compliance.openshift.io CRDs not installed")
}
