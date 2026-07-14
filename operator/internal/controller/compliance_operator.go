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
	getErr := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if getErr == nil {
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
	if meta.IsNoMatchError(getErr) {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled",
			"OLM Subscription API not available")
		return nil
	}
	if !apierrors.IsNotFound(getErr) {
		return fmt.Errorf("getting compliance-operator Subscription: %w", getErr)
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
	// Create uses the best-guess source even if detection was unconfident: a wrong
	// first guess surfaces as InstallStalled and self-corrects once the sync path
	// re-resolves confidently.
	createSource, _ := r.resolveCatalogSource(ctx, cb)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": createSource, "sourceNamespace": "openshift-marketplace",
	}
	if err := createIfMissing(ctx, r.Client, sub); err != nil {
		return fmt.Errorf("ensuring compliance-operator Subscription: %w", err)
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV")
	return nil
}

// ensureComplianceOperatorGroup makes the openshift-compliance namespace carry
// a single OperatorGroup scoped to itself. targetNamespaces must stay set on our
// own OG: create-only would leave a pre-existing empty one (cluster-wide install)
// as a silent hazard. OLM permits only one OperatorGroup per namespace, so if a
// differently-named one already exists (user pre-staged the namespace, or it is
// shared), creating our fixed-name OG would make a second and invalidate the
// namespace (MultipleOperatorGroupsFound), wedging the install. Defer to that
// user-managed OG rather than duplicate it; we only manage our own named OG
// (write RBAC is scoped to compliance-operator, so we cannot repair a foreign one).
func (r *ClusterBaselineReconciler) ensureComplianceOperatorGroup(ctx context.Context) error {
	existing := uList(operatorGroupGVK)
	if err := r.List(ctx, existing, client.InNamespace(complianceNamespace)); err != nil {
		return fmt.Errorf("listing OperatorGroups in %s: %w", complianceNamespace, err)
	}
	for i := range existing.Items {
		if existing.Items[i].GetName() != "compliance-operator" {
			// A user-managed OperatorGroup already owns the namespace. Leave it and
			// add nothing: the Compliance Operator installs through it, and a second
			// OG here would break OLM for the whole namespace.
			log.FromContext(ctx).Info("deferring to existing OperatorGroup in compliance namespace",
				"operatorGroup", existing.Items[i].GetName(), "namespace", complianceNamespace)
			return nil
		}
	}
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

// resolveCatalogSource picks the OLM CatalogSource for the CO Subscription and
// reports whether the choice is confident. An explicit spec value always wins
// (and is confident). When unset it auto-detects the cluster flavor: OCP carries
// the Compliance Operator in redhat-operators, OKD in community-operators. Prefer
// redhat-operators when present; fall back to community-operators only if
// redhat-operators is definitely absent (OKD); otherwise the default.
//
// confident is false when detection had to assume-present on a transient API
// error (so the answer is a best guess, not a verified choice). The create path
// uses the source regardless (a wrong first guess surfaces as InstallStalled and
// self-corrects next reconcile); syncComplianceSubscriptionSource must NOT rewrite
// a working Subscription on a non-confident guess, or a transient error on the
// redhat-operators check would flap an OKD Subscription off community-operators.
func (r *ClusterBaselineReconciler) resolveCatalogSource(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) (source string, confident bool) {
	if s := strings.TrimSpace(cb.Spec.ComplianceCatalogSource); s != "" {
		return s, true
	}
	if present, definite := r.catalogSourcePresent(ctx, baselinev1alpha1.DefaultComplianceCatalogSource); present {
		return baselinev1alpha1.DefaultComplianceCatalogSource, definite
	}
	if present, definite := r.catalogSourcePresent(ctx, baselinev1alpha1.CommunityCatalogSource); present {
		return baselinev1alpha1.CommunityCatalogSource, definite
	}
	// Neither found (both definitely absent, or both errored): default, unconfident.
	return baselinev1alpha1.DefaultComplianceCatalogSource, false
}

// catalogSourcePresent reports whether a CatalogSource of the given name exists in
// openshift-marketplace, and whether that answer is definite. A clean Get is a
// definite presence; NotFound / NoMatch (the CatalogSource CRD absent) is a
// definite absence. A transient/forbidden error assumes present (so detection
// keeps its priority-ordered choice rather than wrongly falling through to the
// default catalog) but marks the answer NOT definite, so a writing caller can
// decline to act on a guess.
func (r *ClusterBaselineReconciler) catalogSourcePresent(ctx context.Context, name string) (present, definite bool) {
	cs := u(catalogSourceGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: "openshift-marketplace", Name: name}, cs)
	if err == nil {
		return true, true
	}
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, true
	}
	return true, false
}

// syncComplianceSubscriptionSource updates an existing Subscription's
// spec.source when it diverges from the CR. No-op when already matched.
// Retries on conflict: OLM and other controllers race Subscription updates, and
// a single failed Update would Degrade the whole reconcile for a catalog move.
func (r *ClusterBaselineReconciler) syncComplianceSubscriptionSource(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, sub *unstructured.Unstructured,
) error {
	desired, confident := r.resolveCatalogSource(ctx, cb)
	current, _, err := unstructured.NestedString(sub.Object, "spec", "source")
	if err != nil {
		return fmt.Errorf("reading Subscription spec.source: %w", err)
	}
	if current == desired {
		return nil
	}
	if !confident {
		// Auto-detection was uncertain (a transient API error made a CatalogSource
		// check assume-present). Do not rewrite a working Subscription onto a guessed
		// source; a confident reconcile corrects any real drift. Prevents flapping an
		// OKD Subscription off community-operators when the redhat-operators check
		// transiently errors.
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
			return fmt.Errorf("setting Subscription spec.source to %q: %w", desired, err)
		}
		return r.Update(ctx, latest)
	}); err != nil {
		return fmt.Errorf("updating compliance-operator Subscription catalog source to %q: %w", desired, err)
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
	// Wrong-type installedCSV must not look like "still Installing" forever
	// (empty string path): surface the shape error so Degraded is actionable.
	csvName, _, err := unstructured.NestedString(sub.Object, "status", "installedCSV")
	if err != nil {
		return fmt.Errorf("reading Subscription status.installedCSV: %w", err)
	}
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
