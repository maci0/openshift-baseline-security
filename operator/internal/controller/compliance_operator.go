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
	getErr := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: complianceOperatorName}, sub)
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
		setComplianceOperatorReadyFromCSV(ctx, cb, csv)
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
	sub.SetName(complianceOperatorName)
	sub.SetNamespace(complianceNamespace)
	// No CSV exists yet, so no version is installed: clear it on this path too so
	// a stale version from a previous install cannot survive alongside
	// ComplianceOperatorReady=False/Installing (a CO deleted out from under us).
	cb.Status.ComplianceOperatorVersion = ""
	// Create uses the best-guess source even if detection was unconfident: a wrong
	// first guess surfaces as InstallStalled and self-corrects once the sync path
	// re-resolves confidently.
	createSource, confident := r.resolveCatalogSource(ctx, cb)
	sub.Object["spec"] = map[string]any{
		"name": complianceOperatorName, "channel": "stable",
		"source": createSource, "sourceNamespace": marketplaceNS,
	}
	if err := createIfMissing(ctx, r.Client, sub); err != nil {
		return fmt.Errorf("ensuring compliance-operator Subscription: %w", err)
	}
	if !confident {
		// Auto-detection could not verify either catalog, so the Subscription is now
		// pinned to the default source. A resolve failure for it surfaces only as
		// InstallStalled, and the read failures that produced the guess are
		// rate-limited in catalogSourcePresent, so without this line an operator has
		// no marker that the source was never verified. Logged after the write so it
		// fires once per install rather than on every reconcile before the create.
		log.FromContext(ctx).Info("compliance-operator Subscription created with an unverified catalog source",
			"name", cb.Name, "source", createSource)
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
		if existing.Items[i].GetName() != complianceOperatorName {
			// A user-managed OperatorGroup already owns the namespace. Leave it and
			// add nothing: the Compliance Operator installs through it, and a second
			// OG here would break OLM for the whole namespace.
			log.FromContext(ctx).Info("deferring to existing OperatorGroup in compliance namespace",
				"operatorGroup", existing.Items[i].GetName(), "namespace", complianceNamespace)
			return nil
		}
	}
	og := u(operatorGroupGVK)
	og.SetName(complianceOperatorName)
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
// reports whether the choice is confident. An explicit spec value wins (and is
// confident), except when it equals the 0.5.6 CRD default redhat-operators:
// that value is persisted on every CR written under the old schema, so
// detection decides instead (see below). When unset it auto-detects the
// cluster flavor: OCP carries the Compliance Operator in redhat-operators, OKD
// in community-operators. Prefer redhat-operators when present; fall back to
// community-operators only if redhat-operators is definitely absent (OKD);
// otherwise the default.
//
// confident is false when detection had to assume-present on a transient API
// error (so the answer is a best guess, not a verified choice). The create path
// uses the source regardless (a wrong first guess surfaces as InstallStalled and
// self-corrects next reconcile); syncComplianceSubscriptionSource must NOT rewrite
// a working Subscription on a non-confident guess, or a transient error on the
// redhat-operators check would flap an OKD Subscription off community-operators.
func (r *ClusterBaselineReconciler) resolveCatalogSource(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) (source string, confident bool) {
	// The 0.5.6 CRD defaulted this field to redhat-operators, so every CR
	// written under that schema has the default persisted in spec; trusting it
	// as an explicit choice would pin OKD clusters to a nonexistent catalog
	// forever after upgrade. Treat the default value as "not set" and let
	// detection decide: when redhat-operators exists detection returns it
	// anyway, and when it is definitely absent a Subscription pinned to it
	// could never resolve, so falling through only ever heals.
	if s := strings.TrimSpace(cb.Spec.ComplianceCatalogSource); s != "" && s != baselinev1alpha1.DefaultComplianceCatalogSource {
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

// marketplaceNS holds the OLM CatalogSources auto-detection reads. One constant
// so the namespace in the read, in the written Subscription, and in the failure
// log cannot drift apart.
const marketplaceNS = "openshift-marketplace"

// catalogSourcePresent reports whether a CatalogSource of the given name exists in
// openshift-marketplace, and whether that answer is definite. A clean Get is a
// definite presence; NotFound / NoMatch (the CatalogSource CRD absent) is a
// definite absence. A transient/forbidden error assumes present (so detection
// keeps its priority-ordered choice rather than wrongly falling through to the
// default catalog) but marks the answer NOT definite, so a writing caller can
// decline to act on a guess. That guess is a fail-safe choice, not a silent one:
// the error is logged, because a persistent failure here leaves the Subscription
// pinned to an unverified source and the sync path declining to correct it, with
// InstallStalled as the only other symptom.
func (r *ClusterBaselineReconciler) catalogSourcePresent(ctx context.Context, name string) (present, definite bool) {
	cs := u(catalogSourceGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: marketplaceNS, Name: name}, cs)
	if err == nil {
		return true, true
	}
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, true
	}
	r.logCatalogReadErr(ctx, name, err)
	return true, false
}

// logCatalogReadErr emits a CatalogSource detection read failure at Error level
// at most once per historyStallLogInterval, and V(1) in between. Detection runs
// on every reconcile that owns the Subscription, so a persistent RBAC denial must
// surface without streaming an unbounded Error log. Same shape and interval as
// logInfraReadErr, which guards the other fail-safe read in the reconciler.
func (r *ClusterBaselineReconciler) logCatalogReadErr(ctx context.Context, name string, err error) {
	logger := log.FromContext(ctx)
	kv := []any{"catalogSource", name, "namespace", marketplaceNS}
	if r.lastCatalogErrLog.IsZero() || r.elapsed(r.lastCatalogErrLog) >= historyStallLogInterval {
		r.lastCatalogErrLog = r.now()
		logger.Error(err,
			"cannot read CatalogSource; assuming it is present so catalog detection keeps its priority order",
			kv...)
		return
	}
	logger.V(1).Info("CatalogSource read failed; assuming it is present so catalog detection keeps its priority order",
		append(kv, "error", err)...)
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
			Namespace: complianceNamespace, Name: complianceOperatorName,
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

// csvListPageSize bounds one apiserver List of ClusterServiceVersions. A CSV
// carries the whole install spec and the alm-examples annotation, so an unpaged
// cluster-wide List can hold hundreds of MB of decoded JSON; paging caps what
// one response pins in the reconciler. Pages are folded into a running best and
// released (see foldComplianceOperatorCSVs), so the bound holds across the walk
// and not just within one response.
const csvListPageSize int64 = 100

// csvListMaxPages bounds the cluster-wide fallback walk. A repeat token or an
// apiserver that never stops handing pages back would otherwise burn the whole
// reconcileTimeout on a path that only needs a few pages to answer the question.
// Stopping early degrades to the same "no CSV found" result the other exits use,
// which the caller turns into a NotInstalled condition, rather than wedging the
// singleton worker.
const csvListMaxPages = 50

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

	// Cluster-wide fallback. Paged: a CSVCatalog CR carries the full install
	// spec plus the alm-examples annotation, so one unpaged List of a
	// multi-operator cluster decodes tens to hundreds of MB into maps on a
	// path the steady-state never takes (the local Succeeded lookup above
	// already answered it). Only name, namespace and status.phase are read.
	//
	// Each page is folded into a running best per tier and then released: the
	// page is reused by the next List, and accumulating pages would restore the
	// whole-cluster footprint paging exists to avoid. Held across the walk are
	// two objects (the newest Succeeded and the newest non-Succeeded), not every
	// CSV on the cluster.
	var bestSucceeded, bestOther *unstructured.Unstructured
	cluster := uList(csvGVK)
	cont := ""
	for page := 0; ; page++ {
		opts := []client.ListOption{client.Limit(csvListPageSize)}
		if cont != "" {
			opts = append(opts, client.Continue(cont))
		}
		if err := r.List(ctx, cluster, opts...); err != nil {
			if meta.IsNoMatchError(err) {
				// CRD still present for namespaced list; only non-Succeeded local remains.
				return pickComplianceOperatorCSV(local.Items, complianceNamespace, false), nil
			}
			return nil, fmt.Errorf("listing CSVs cluster-wide: %w", err)
		}
		bestSucceeded, bestOther = foldComplianceOperatorCSVs(cluster.Items, bestSucceeded, bestOther)
		next := cluster.GetContinue()
		if next == "" {
			break
		}
		// The request token must advance or the next List would return the same
		// page for as long as the reconcile deadline holds.
		if next == cont {
			return pickComplianceOperatorCSV(local.Items, complianceNamespace, false), nil
		}
		cont = next
		if page+1 >= csvListMaxPages {
			return pickComplianceOperatorCSV(local.Items, complianceNamespace, false), nil
		}
	}
	if bestSucceeded != nil {
		return bestSucceeded, nil
	}
	if csv := pickComplianceOperatorCSV(local.Items, complianceNamespace, false); csv != nil {
		return csv, nil
	}
	return bestOther, nil
}

// foldComplianceOperatorCSVs returns the newest compliance-operator CSV of each
// tier (Succeeded / not Succeeded) across items, preferring the incumbents
// already held. It is the streaming form of pickComplianceOperatorCSV: callers
// paged through a cluster-wide List cannot keep every page resident, so each
// page is folded in and dropped. Ties go to the incumbent, so the winner does
// not depend on how the pages were split. Only the winners are DeepCopied, and
// only when one actually replaces the incumbent.
func foldComplianceOperatorCSVs(
	items []unstructured.Unstructured,
	bestSucceeded, bestOther *unstructured.Unstructured,
) (*unstructured.Unstructured, *unstructured.Unstructured) {
	for i := range items {
		csv := &items[i]
		if !strings.HasPrefix(csv.GetName(), csvNamePrefix) {
			continue
		}
		phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
		if phase == "Succeeded" {
			if bestSucceeded == nil ||
				compareComplianceCSVVersion(csv.GetName(), bestSucceeded.GetName()) > 0 {
				bestSucceeded = csv.DeepCopy()
			}
			continue
		}
		if bestOther == nil ||
			compareComplianceCSVVersion(csv.GetName(), bestOther.GetName()) > 0 {
			bestOther = csv.DeepCopy()
		}
	}
	return bestSucceeded, bestOther
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
		if !strings.HasPrefix(csv.GetName(), csvNamePrefix) {
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
	// (empty string path): surface the shape error so Degraded names the cause.
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
	setComplianceOperatorReadyFromCSV(ctx, cb, csv)
	return nil
}

func setComplianceOperatorReadyFromCSV(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, csv *unstructured.Unstructured) {
	phase, _, err := unstructured.NestedString(csv.Object, "status", "phase")
	if err != nil {
		// Type-mismatched phase is not "unknown" (that reads as CO reporting an
		// empty phase). Keep CSVNotReady so rollup still Progresses then
		// InstallStalls; name the NestedString error so on-call sees the shape.
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady",
			fmt.Sprintf("unreadable CSV status.phase: %s", err.Error()))
		return
	}
	// Missing phase must not report "phase=" (empty); treat as unknown.
	if phase == "" {
		phase = "unknown"
	}
	if phase == "Succeeded" {
		cb.Status.ComplianceOperatorVersion = strings.TrimPrefix(csv.GetName(), csvNamePrefix)
		setCondTrueLogRecovered(ctx, cb, "ComplianceOperatorReady", "CSVSucceeded", "",
			"compliance-operator ready", "name", cb.Name,
			"version", cb.Status.ComplianceOperatorVersion)
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
	// %q, not concatenation: phase is a foreign ClusterServiceVersion status
	// string, and the message lands in the CR (and the console that renders it).
	// Raw newlines and ANSI escapes would survive condMessage, which caps
	// length only, so quote it the way the unreadable-phase branch above does.
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", fmt.Sprintf("phase=%q", phase))
}

// setScanCRDsMissing marks ScanConfigured false when the compliance.openshift.io
// CRDs are absent (no REST mapping), so a missing Compliance Operator degrades
// gracefully instead of erroring the reconcile. Logs on the False/CRDsMissing
// transition: this does not roll up to Degraded, and Available=False may be
// attributed to ComplianceOperatorReady first, so without this line the scan
// CRD gap is only on the CR.
func setScanCRDsMissing(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) {
	setCondFalseLogOnce(ctx, cb, "ScanConfigured", "CRDsMissing",
		"compliance.openshift.io CRDs not installed",
		"compliance CRDs not installed; scan config skipped",
		"name", cb.Name)
}
