package controller

import (
	"context"
	_ "embed"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

const (
	complianceNamespace = "openshift-compliance"
	scanSettingName     = "baseline"
	finalizerName       = "baselinesecurity.io/cleanup"
	pluginName          = "baseline-security-console-plugin"
	pluginNS            = "openshift-baseline-security"
	// The console renders dashboards from ConfigMaps in this namespace; ours is
	// created here so it shows under Observe -> Dashboards without a Grafana.
	dashboardNS        = "openshift-config-managed"
	dashboardName      = "baseline-security-compliance-dashboard"
	suiteLabel         = "compliance.openshift.io/suite"
	checkSeverityLabel = "compliance.openshift.io/check-severity"
	historyMax         = 30
	// Grace before a not-ready Compliance Operator install rolls up to Degraded
	// (OLM resolve + CSV install + pods can take several minutes on a slow cluster).
	coInstallGrace = 15 * time.Minute
	// Desired HA for the console plugin Deployment.
	pluginReplicas = int32(2)
	// Ready threshold for ConsolePluginReady=True: one ready pod is enough for
	// the plugin to serve; partial (1/2) must not Progress forever as WaitingForPods.
	pluginReadyMin = int32(1)
)

// Foreign CRs are unstructured so we do not import their Go API modules.
var (
	subscriptionGVK  = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}
	csvGVK           = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "ClusterServiceVersion"}
	scanSettingGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSetting"}
	bindingGVK       = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSettingBinding"}
	suiteGVK         = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceSuite"}
	checkResultGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceCheckResult"}
	scanGVK          = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceScan"}
	consolePluginGVK = schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"}
	consoleGVK       = schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Console"}
	operatorGroupGVK = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"}
	remediationGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceRemediation"}
	mcpGVK           = schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfigPool"}
)

//go:embed assets/compliance-dashboard.json
var complianceDashboardJSON string

// ClusterBaselineReconciler reconciles the ClusterBaseline singleton.
type ClusterBaselineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines/finalizers,verbs=update
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=scansettings;scansettingbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=compliancecheckresults;compliancescans;compliancesuites,verbs=get;list;watch
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=complianceremediations,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch;patch
// Subscriptions need update/patch so complianceCatalogSource can be synced after
// the initial createIfMissing (OKD / disconnected catalog moves).
// +kubebuilder:rbac:groups=operators.coreos.com,resources=subscriptions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorgroups,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// ConfigMaps: the console score-trend dashboard in openshift-config-managed.
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=get;list;watch;update;patch

func (r *ClusterBaselineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, req.NamespacedName, cb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !cb.DeletionTimestamp.IsZero() {
		// Unpause any MCPs a live batch held. Finalizer removal + GC must not
		// leave MachineConfigPools stuck paused with no operator left to resume.
		if err := r.resumeBatchPoolsOnDelete(ctx, cb); err != nil {
			// Structured context: finalizer stays until resume succeeds; without
			// this log on-call only sees a generic reconcile error.
			logger.Error(err, "resume batch pools on delete failed", "name", cb.Name)
			return ctrl.Result{}, err
		}
		if err := r.deregisterConsolePlugin(ctx); err != nil {
			logger.Error(err, "deregister console plugin on delete failed", "name", cb.Name)
			return ctrl.Result{}, err
		}
		if controllerutil.RemoveFinalizer(cb, finalizerName) {
			if err := r.Update(ctx, cb); err != nil {
				logger.Error(err, "remove finalizer failed", "name", cb.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(cb, finalizerName) {
		return ctrl.Result{}, r.Update(ctx, cb) // update requeues
	}

	if err := r.reconcileOwned(ctx, cb); err != nil {
		// Persist a Degraded condition (best-effort) so a persistently failing
		// reconcile is visible on the CR instead of leaving stale healthy status.
		sanitizeStatusForUpdate(cb)
		setRollupConditions(cb)
		setCond(cb, "Degraded", metav1.ConditionTrue, "ReconcileError", err.Error())
		// Structured Error before return: controller-runtime also logs the error,
		// but without the CR name or that Degraded was attempted.
		logger.Error(err, "reconcile failed", "name", cb.Name)
		if serr := r.Status().Update(ctx, cb); serr != nil {
			// Error, not V(1): without this log the CR can look healthy while
			// every reconcile fails and the Degraded condition never sticks.
			logger.Error(serr, "status update after reconcile error failed", "name", cb.Name)
		}
		// Publish after Degraded is set so ClusterBaselineDegraded can fire even
		// when aggregation never ran (API blip, batch apply failure, etc.).
		publishMetrics(cb)
		return ctrl.Result{}, err
	}
	// OpenShift-style rollup conditions (Available / Progressing / Degraded).
	// Publish metrics after rollup so Available/Degraded gauges match status.
	sanitizeStatusForUpdate(cb)
	setRollupConditions(cb)
	publishMetrics(cb)
	if err := r.Status().Update(ctx, cb); err != nil {
		logger.Error(err, "status update failed", "name", cb.Name)
		return ctrl.Result{}, err
	}
	logger.V(1).Info("reconciled", "score", cb.Status.Score)
	return ctrl.Result{RequeueAfter: requeueAfter(cb)}, nil
}

// requeueAfter picks the poll cadence. Steady state is 1m; any Progressing
// rollup and an in-flight remediation batch use 15s so cancel/grace/Applied are
// not stuck behind a full minute when the dynamic informer is lagging or not yet up.
func requeueAfter(cb *baselinev1alpha1.ClusterBaseline) time.Duration {
	const fast = 15 * time.Second
	const slow = time.Minute
	if progressing := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); progressing != nil && progressing.Status == metav1.ConditionTrue {
		return fast
	}
	if cb.Status.RemediationBatch != nil {
		return fast
	}
	return slow
}

// reconcileOwned drives every owned object and refreshes status fields.
func (r *ClusterBaselineReconciler) reconcileOwned(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	// Remediation batch first: pause/resume/cancel/grace must not wait behind a
	// hard failure in CO install, scan config, or plugin ensure. Otherwise an
	// API blip on those steps leaves MachineConfigPools paused until it clears.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		return fmt.Errorf("applying remediation batch: %w", err)
	}
	if err := r.ensureComplianceOperator(ctx, cb); err != nil {
		return fmt.Errorf("ensuring compliance operator: %w", err)
	}
	if err := r.ensureScanConfig(ctx, cb); err != nil {
		return fmt.Errorf("ensuring scan config: %w", err)
	}
	if err := r.ensureConsolePlugin(ctx, cb); err != nil {
		return fmt.Errorf("ensuring console plugin: %w", err)
	}
	r.ensureComplianceDashboard(ctx, cb)
	if err := r.aggregateStatus(ctx, cb); err != nil {
		return fmt.Errorf("aggregating status: %w", err)
	}
	// Metrics are published in Reconcile after setRollupConditions so condition
	// gauges (Available/Progressing/Degraded) match the status being written.
	if err := r.checkScanStorage(ctx, cb); err != nil {
		return fmt.Errorf("checking scan storage: %w", err)
	}
	return nil
}

// preferredHostnameAntiAffinity spreads pods across nodes (CONVENTIONS.md HA).
func preferredHostnameAntiAffinity(labels map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
					TopologyKey:   "kubernetes.io/hostname",
				},
			}},
		},
	}
}

func createIfMissing(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func u(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(gvk)
	return o
}

func uList(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	l := &unstructured.UnstructuredList{}
	l.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	return l
}

func (r *ClusterBaselineReconciler) ensureComplianceOperator(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	sub := u(subscriptionGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if err == nil {
		// Keep catalog source in sync when we manage install. createIfMissing only
		// writes the Subscription once; without this, changing
		// spec.complianceCatalogSource (OKD / disconnected) is a silent no-op.
		if cb.Spec.InstallComplianceOperator != baselinev1alpha1.InstallManual {
			if err := r.syncComplianceSubscriptionSource(ctx, cb, sub); err != nil {
				return err
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
		return err
	}

	csv, err := r.findComplianceOperatorCSV(ctx)
	if err != nil {
		return err
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
		return err
	}
	og := u(operatorGroupGVK)
	og.SetName("compliance-operator")
	og.SetNamespace(complianceNamespace)
	// Must set targetNamespaces: an empty OperatorGroup can install cluster-wide.
	if err := unstructured.SetNestedStringSlice(og.Object, []string{complianceNamespace}, "spec", "targetNamespaces"); err != nil {
		return fmt.Errorf("setting OperatorGroup targetNamespaces: %w", err)
	}
	if err := createIfMissing(ctx, r.Client, og); err != nil {
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
		return err
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV")
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
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
	})
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
		return nil, err
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
		return nil, err
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
		return err
	}
	setComplianceOperatorReadyFromCSV(cb, csv)
	return nil
}

func setComplianceOperatorReadyFromCSV(cb *baselinev1alpha1.ClusterBaseline, csv *unstructured.Unstructured) {
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
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

func (r *ClusterBaselineReconciler) ensureScanConfig(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	// Validate schedule first, but still reconcile ScanSetting fields other than
	// schedule and all bindings so a bad cron does not freeze profile/tp or
	// auto-apply changes. Invalid schedule is reported as Degraded at the end.
	schedule, schedErr := normalizedSchedule(cb.Spec.Schedule)
	invalidScheduleMessage := ""
	if schedErr != nil {
		invalidScheduleMessage = schedErr.Error()
	}

	ss := u(scanSettingGVK)
	ss.SetName(scanSettingName)
	ss.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		autoApply := cb.Spec.Remediation.Apply == baselinev1alpha1.RemediationApplyAutomatic
		// Only write a validated schedule; keep the last-good cron if invalid.
		// On first create with a bad cron there is no last-good value: fall back
		// to the operator default so CO is not left with an empty schedule.
		if schedErr == nil {
			ss.Object["schedule"] = schedule
		} else if existing, found, err := unstructured.NestedString(ss.Object, "schedule"); err != nil {
			return fmt.Errorf("reading ScanSetting schedule: %w", err)
		} else if !found || existing == "" {
			ss.Object["schedule"] = defaultScanSchedule
		}
		ss.Object["roles"] = []any{"worker", "master"}
		// Set only the storage leaves we own; preserve server-defaulted siblings
		// (e.g. pvAccessModes) so this does not diff on every reconcile.
		// Wrong-type rawResultStorage must not be overwritten with a bare map
		// that would drop siblings; fail the reconcile so the shape is fixed.
		storage, _, err := unstructured.NestedMap(ss.Object, "rawResultStorage")
		if err != nil {
			return fmt.Errorf("reading ScanSetting rawResultStorage: %w", err)
		}
		if storage == nil {
			storage = map[string]any{}
		}
		storage["size"] = "1Gi"
		storage["rotation"] = int64(3)
		ss.Object["rawResultStorage"] = storage
		ss.Object["autoApplyRemediations"] = autoApply
		ss.Object["autoUpdateRemediations"] = autoApply
		return controllerutil.SetControllerReference(cb, ss, r.Scheme)
	})
	if err != nil {
		if meta.IsNoMatchError(err) {
			setScanCRDsMissing(cb)
			return nil
		}
		return err
	}

	for _, key := range cb.Spec.Profiles {
		binding := u(bindingGVK)
		binding.SetName(bindingName(key))
		binding.SetNamespace(complianceNamespace)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
			names := key.ProfileNames()
			profiles := make([]any, 0, len(names))
			for _, p := range names {
				profiles = append(profiles, map[string]any{
					"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "Profile", "name": p,
				})
			}
			binding.Object["profiles"] = profiles
			binding.Object["settingsRef"] = map[string]any{
				"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "ScanSetting", "name": scanSettingName,
			}
			return controllerutil.SetControllerReference(cb, binding, r.Scheme)
		})
		if err != nil {
			return err
		}
	}

	for _, name := range cb.Spec.TailoredProfiles {
		binding := u(bindingGVK)
		binding.SetName(tailoredBindingName(name))
		binding.SetNamespace(complianceNamespace)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
			binding.Object["profiles"] = []any{map[string]any{
				"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "TailoredProfile", "name": name,
			}}
			binding.Object["settingsRef"] = map[string]any{
				"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "ScanSetting", "name": scanSettingName,
			}
			return controllerutil.SetControllerReference(cb, binding, r.Scheme)
		})
		if err != nil {
			return err
		}
	}

	bindings := uList(bindingGVK)
	if err := r.List(ctx, bindings, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			setScanCRDsMissing(cb)
			return nil
		}
		return err
	}
	selected := ownedSuites(cb)
	for i := range bindings.Items {
		b := &bindings.Items[i]
		if selected[b.GetName()] || !metav1.IsControlledBy(b, cb) {
			continue
		}
		if err := r.Delete(ctx, b); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if invalidScheduleMessage != "" {
		setCond(cb, "ScanConfigured", metav1.ConditionFalse, "InvalidSchedule",
			fmt.Sprintf("spec.schedule %q is not a valid standard cron schedule: %s", cb.Spec.Schedule, invalidScheduleMessage))
		return nil
	}
	// No profiles and no tailored profiles: scanning is intentionally disabled.
	// Bindings were pruned above; report it as a healthy (not Degraded) state.
	if len(cb.Spec.Profiles) == 0 && len(cb.Spec.TailoredProfiles) == 0 {
		setCond(cb, "ScanConfigured", metav1.ConditionTrue, "ScanningDisabled",
			"No profiles selected; scanning is disabled.")
		return nil
	}
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	return nil
}

// deregisterConsolePlugin drops our entry from consoles.operator.openshift.io/cluster.
// Owned Deployment/Service/ConsolePlugin are GCed via owner refs on CR delete.
func (r *ClusterBaselineReconciler) deregisterConsolePlugin(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			// Console capability disabled (CRD absent) or config gone: nothing to
			// deregister. Must tolerate NoMatch so CR deletion is not wedged.
			if meta.IsNoMatchError(err) {
				return nil
			}
			return client.IgnoreNotFound(err)
		}
		plugins, _, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if err != nil {
			return fmt.Errorf("reading console plugins: %w", err)
		}
		kept := withoutPlugin(plugins, pluginName)
		if len(kept) == len(plugins) {
			return nil
		}
		if err := unstructured.SetNestedStringSlice(console.Object, kept, "spec", "plugins"); err != nil {
			return fmt.Errorf("removing console plugin registration: %w", err)
		}
		return r.Update(ctx, console)
	})
}

// removeConsolePlugin tears down plugin objects when managementState is Removed.
func (r *ClusterBaselineReconciler) removeConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	cp := u(consolePluginGVK)
	cp.SetName(pluginName)
	// NoMatch: the ConsolePlugin CRD is absent (Console capability disabled),
	// so there is nothing to remove; do not wedge on a permanent Degraded.
	if err := r.Delete(ctx, cp); err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return err
	}
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
	} {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := r.deregisterConsolePlugin(ctx); err != nil {
		return err
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "Disabled", "")
	return nil
}

func (r *ClusterBaselineReconciler) aggregateStatus(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	list := uList(checkResultGVK)
	if err := r.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			// CRDs gone: do not leave a stale score/profile rollup on the CR.
			cb.Status.Score = nil
			cb.Status.Profiles = nil
			cb.Status.TailoredProfiles = nil
			cb.Status.LastScanTime = nil
			cb.Status.NextScanTime = nil
			cb.Status.History = nil
			cb.Status.PreviousFailures = nil
			cb.Status.DiffBaseFailures = nil
			cb.Status.DiffBaseScanTime = nil
			cb.Status.NewlyFailed = nil
			cb.Status.Fixed = nil
			// Keep relatedObjects in sync with desired ownership even when CO is absent.
			cb.Status.RelatedObjects = relatedObjects(cb)
			return nil
		}
		return err
	}

	byProfile := map[baselinev1alpha1.ProfileKey]*baselinev1alpha1.ProfileStatus{}
	for _, key := range cb.Spec.Profiles {
		byProfile[key] = &baselinev1alpha1.ProfileStatus{Key: key, ProfileNames: key.ProfileNames()}
	}
	byTailored := map[string]*baselinev1alpha1.TailoredProfileStatus{}
	for _, name := range cb.Spec.TailoredProfiles {
		byTailored[name] = &baselinev1alpha1.TailoredProfileStatus{Name: name}
	}

	// Checks waived as accepted risk are pulled out of the pass/fail denominator
	// and reported in the Waived bucket, keyed by ComplianceCheckResult name.
	// Skip empty names so a corrupt entry cannot match every empty-named object.
	// An expired waiver no longer applies: the check is scored by its raw status.
	nowT := time.Now()
	waived := make(map[string]bool, len(cb.Spec.Waivers))
	for _, w := range cb.Spec.Waivers {
		if w.Name == "" {
			continue
		}
		if w.ExpiresAt != nil && !w.ExpiresAt.After(nowT) {
			continue
		}
		waived[w.Name] = true
	}

	var pass, fail int32
	var wPass, wFail int64 // severity-weighted totals (pooled)
	weights := &scoreWeights{
		profiles: make(map[baselinev1alpha1.ProfileKey]weightedSum, len(byProfile)),
		tailored: make(map[string]weightedSum, len(byTailored)),
	}
	var currentFails []string
	// tally routes one check result's status into the counts and the score.
	// INFO is counted (excluded from score) so Overview totals match Results.
	// SKIP is folded into NotApplicable (CO: check skipped for this system).
	// WAIVED is our synthetic status for accepted-risk checks (excluded from score).
	// Unknown/empty/corrupt status fails closed into ERROR so a CCR is never
	// silently dropped from ResultCounts (and metrics stay complete).
	tally := func(c *baselinev1alpha1.ResultCounts, status string) {
		switch status {
		case "PASS":
			c.Pass++
			pass++
		case "FAIL":
			c.Fail++
			fail++
		case "MANUAL":
			c.Manual++
		case "INFO":
			c.Info++
		case "ERROR":
			c.Error++
		case "INCONSISTENT":
			c.Inconsistent++
		case "WAIVED":
			c.Waived++
		case "SKIP", "NOT-APPLICABLE":
			c.NotApplicable++
		default:
			c.Error++
		}
	}
	// addWeight accumulates severity mass for the pooled score and the owning
	// profile/tailored bucket so per-profile history follows scoring.mode.
	addWeight := func(status string, item *unstructured.Unstructured, profileKey baselinev1alpha1.ProfileKey, tailoredName string, isTailored bool) {
		if status != "PASS" && status != "FAIL" {
			return
		}
		w := severityWeight(checkSeverity(item))
		if status == "FAIL" {
			wFail += w
			if isTailored {
				s := weights.tailored[tailoredName]
				s.fail += w
				weights.tailored[tailoredName] = s
			} else {
				s := weights.profiles[profileKey]
				s.fail += w
				weights.profiles[profileKey] = s
			}
			return
		}
		wPass += w
		if isTailored {
			s := weights.tailored[tailoredName]
			s.pass += w
			weights.tailored[tailoredName] = s
		} else {
			s := weights.profiles[profileKey]
			s.pass += w
			weights.profiles[profileKey] = s
		}
	}
	// Index range: avoid copying each Unstructured (map header + metadata) on
	// every iteration when multi-profile scans yield thousands of results.
	for i := range list.Items {
		item := &list.Items[i]
		suite := item.GetLabels()[suiteLabel]
		// Route to the owning bucket first so weighting/regression only see owned checks.
		var rc *baselinev1alpha1.ResultCounts
		var profileKey baselinev1alpha1.ProfileKey
		var tailoredName string
		var isTailored bool
		if name, ok := tailoredNameFromSuite(suite); ok {
			if ts := byTailored[name]; ts != nil {
				rc = &ts.ResultCounts
				tailoredName = name
				isTailored = true
			}
		} else if key, ok := profileKeyFromSuite(suite); ok {
			if ps := byProfile[key]; ps != nil {
				rc = &ps.ResultCounts
				profileKey = key
			}
		}
		if rc == nil {
			continue
		}
		status, _, err := unstructured.NestedString(item.Object, "status")
		// Wrong-type or missing status must not vanish from counts (tally default
		// maps empty/unknown to ERROR). NestedString returns "" on type error.
		if err != nil {
			status = ""
		}
		// A check the Compliance Operator marks INCONSISTENT only because it does
		// not apply on some nodes (PASS where it applies, NOT-APPLICABLE elsewhere)
		// is benign; collapse it so it does not read as "review each". A real
		// PASS-vs-FAIL split stays INCONSISTENT.
		if status == "INCONSISTENT" {
			status = effectiveInconsistentStatus(item)
		}
		// Waivers apply to failing checks only: a waived FAIL is pulled out of
		// the pass/fail denominator into the Waived bucket. If a waived check later passes it
		// counts as PASS again (self-healing), so a stale waiver never silently
		// depresses the score; the admin can still remove it from the UI.
		if status == "FAIL" && waived[item.GetName()] {
			status = "WAIVED"
		}
		tally(rc, status)
		if status == "FAIL" {
			currentFails = append(currentFails, item.GetName())
		}
		addWeight(status, item, profileKey, tailoredName, isTailored)
	}
	slices.Sort(currentFails)

	// Preserve per-profile score history across the status.Profiles rebuild.
	profHist := map[baselinev1alpha1.ProfileKey][]baselinev1alpha1.ScoreSnapshot{}
	for _, p := range cb.Status.Profiles {
		profHist[p.Key] = p.History
	}
	tpHist := map[string][]baselinev1alpha1.ScoreSnapshot{}
	for _, tp := range cb.Status.TailoredProfiles {
		tpHist[tp.Name] = tp.History
	}
	cb.Status.Profiles = cb.Status.Profiles[:0]
	for _, key := range cb.Spec.Profiles {
		p := *byProfile[key]
		p.History = profHist[key]
		cb.Status.Profiles = append(cb.Status.Profiles, p)
	}
	cb.Status.TailoredProfiles = cb.Status.TailoredProfiles[:0]
	for _, name := range cb.Spec.TailoredProfiles {
		tp := *byTailored[name]
		tp.History = tpHist[name]
		cb.Status.TailoredProfiles = append(cb.Status.TailoredProfiles, tp)
	}
	// LastScanTime is tracked even when no score is computable (all MANUAL /
	// ERROR / NOT-APPLICABLE results) so completed scans stay visible.
	if cb.Spec.Scoring.Mode == baselinev1alpha1.ScoringSeverityWeighted {
		cb.Status.Score = score64(wPass, wFail)
	} else {
		cb.Status.Score = score(pass, fail)
	}
	// Fill deterministic status fields before history so a scan-list failure
	// still leaves a coherent rollup on the error-path status update.
	cb.Status.NextScanTime = nextScanTime(cb.Spec.Schedule, time.Now())
	cb.Status.RelatedObjects = relatedObjects(cb)
	return r.recordHistory(ctx, cb, cb.Status.Score, currentFails, weights)
}

// relatedObjects lists the resources this baseline owns or drives, for
// must-gather / support tooling.
func relatedObjects(cb *baselinev1alpha1.ClusterBaseline) []baselinev1alpha1.ObjectRef {
	refs := []baselinev1alpha1.ObjectRef{
		{Group: "compliance.openshift.io", Resource: "scansettings", Name: scanSettingName, Namespace: complianceNamespace},
		{Group: "apps", Resource: "deployments", Name: pluginName, Namespace: pluginNS},
		{Group: "console.openshift.io", Resource: "consoleplugins", Name: pluginName},
	}
	suites := ownedSuites(cb)
	names := make([]string, 0, len(suites))
	for name := range suites {
		names = append(names, name)
	}
	slices.Sort(names) // deterministic order so status does not flap
	for _, name := range names {
		refs = append(refs, baselinev1alpha1.ObjectRef{
			Group: "compliance.openshift.io", Resource: "scansettingbindings", Name: name, Namespace: complianceNamespace,
		})
	}
	return refs
}

// poolFromRemediation returns the MachineConfigPool a node remediation targets,
// or "" for a non-node one. Prefer the rendered MachineConfig's role label, but
// the Compliance Operator does not always set it, so fall back to the scan-name
// label: node scans run per-MCP, named "<profile>-node-<pool>". Without this
// fallback a node remediation whose MachineConfig has no role label would pause
// no pool, so its apply would reboot the node uncoalesced.
// Role labels and scan-name suffixes are untrusted cluster data; non-DNS1123
// values are dropped so they never enter batch pool lists or MCP Get calls.
func poolFromRemediation(rem *unstructured.Unstructured) string {
	obj, _, err := unstructured.NestedMap(rem.Object, "spec", "current", "object")
	// Wrong-type object: ignore it and fall through to the scan-name label.
	if err == nil && obj != nil {
		kind, _, _ := unstructured.NestedString(obj, "kind")
		// Only reject known non-node kinds. Missing/empty kind still allows the
		// scan-name fallback so a partially rendered MachineConfig does not
		// skip MCP pause during batch apply.
		if kind != "" && kind != "MachineConfig" {
			return ""
		}
		if kind == "MachineConfig" {
			if role, _, _ := unstructured.NestedString(obj, "metadata", "labels", "machineconfiguration.openshift.io/role"); role != "" {
				return validMCPPoolName(role)
			}
		}
	}
	scan := rem.GetLabels()["compliance.openshift.io/scan-name"]
	if i := strings.LastIndex(scan, "-node-"); i >= 0 {
		return validMCPPoolName(scan[i+len("-node-"):])
	}
	return ""
}

// validMCPPoolName returns name when it is a non-empty DNS-1123 subdomain
// (Kubernetes resource name shape), otherwise "".
func validMCPPoolName(name string) string {
	if name == "" || len(utilvalidation.IsDNS1123Subdomain(name)) > 0 {
		return ""
	}
	return name
}

func batchPauseOwner(cb *baselinev1alpha1.ClusterBaseline) string {
	if cb.UID != "" {
		return string(cb.UID)
	}
	if cb.Name != "" {
		return cb.Name
	}
	return "cluster"
}

// setMCPPaused changes an MCP only when this batch owns the pause. A pool that
// was already paused without our marker is left alone and therefore remains
// paused after the batch. Empty owner is the upgrade path for a legacy batch
// status created before pause ownership was tracked.
func (r *ClusterBaselineReconciler) setMCPPaused(ctx context.Context, pool string, paused bool, owner string) error {
	if pool == "" {
		return nil
	}
	// Pool names come from untrusted remediation labels / scan-name suffixes.
	// An invalid name would make Get return a non-NotFound error and could
	// wedge batch pause/resume; skip rather than fail the batch.
	if len(utilvalidation.IsDNS1123Subdomain(pool)) > 0 {
		log.FromContext(ctx).Info("skipping MachineConfigPool with invalid name", "pool", pool)
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mcp := u(mcpGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: pool}, mcp); err != nil {
			// A missing pool or absent MCP CRD must not wedge the batch.
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				return nil
			}
			return err
		}

		current, _, err := unstructured.NestedBool(mcp.Object, "spec", "paused")
		if err != nil {
			return err
		}
		annotations := maps.Clone(mcp.GetAnnotations())
		marker := annotations[batchPauseOwnerAnnotation]
		before := mcp.DeepCopy()

		if paused {
			if owner == "" {
				return fmt.Errorf("pause owner is empty for MachineConfigPool %q", pool)
			}
			if marker != "" && marker != owner {
				return fmt.Errorf("MachineConfigPool %q pause is owned by another batch", pool)
			}
			if current && marker == "" {
				// Administrator-owned pause: use it, but never claim or undo it.
				return nil
			}
			if current && marker == owner {
				return nil
			}
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[batchPauseOwnerAnnotation] = owner
			mcp.SetAnnotations(annotations)
			if err := unstructured.SetNestedField(mcp.Object, true, "spec", "paused"); err != nil {
				return err
			}
		} else {
			if owner == "" {
				// Legacy active batches did not mark ownership. Preserve a marker from a
				// newer batch if one somehow overlaps the upgrade window.
				if marker != "" || !current {
					return nil
				}
				if err := unstructured.SetNestedField(mcp.Object, false, "spec", "paused"); err != nil {
					return err
				}
			} else {
				if marker != owner {
					return nil
				}
				delete(annotations, batchPauseOwnerAnnotation)
				if len(annotations) == 0 {
					annotations = nil
				}
				mcp.SetAnnotations(annotations)
				if err := unstructured.SetNestedField(mcp.Object, false, "spec", "paused"); err != nil {
					return err
				}
			}
		}

		// Info: pausing/resuming an MCP halts or resumes node rollouts. The batch
		// start/finish logs cover the happy path, but standalone resumes (orphan
		// cleanup, delete, grace-force) flip pools with no other marker; on-call
		// needs a per-pool audit trail when a pool is stuck paused.
		log.FromContext(ctx).Info("changing MachineConfigPool pause state", "pool", pool, "paused", paused, "owner", owner)
		return r.Patch(ctx, mcp, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

func batchRemediationNames(raw string) []string {
	return uniqueSortedStrings(splitCSV(raw))
}

func (r *ClusterBaselineReconciler) ensureBatchMetadata(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, pools []string,
) (metav1.Time, error) {
	desiredPools := strings.Join(uniqueSortedStrings(pools), ",")
	var started metav1.Time
	// RetryOnConflict: a concurrent console patch (waiver, schedule, rescan)
	// must not abort batch start after validation; without a stable
	// batch-started-at the grace clock can reset across attempts.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-read on every attempt so the ResourceVersion and any annotations
		// written by a racing client are current before we merge ours.
		latest := &baselinev1alpha1.ClusterBaseline{}
		if err := r.Get(ctx, types.NamespacedName{Name: cb.Name}, latest); err != nil {
			return err
		}
		annotations := maps.Clone(latest.GetAnnotations())
		if annotations == nil {
			annotations = map[string]string{}
		}
		changed := false
		started = metav1.Time{}
		if raw := annotations[batchStartedAtAnnotation]; raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && !parsed.IsZero() {
				started = metav1.NewTime(parsed)
			}
		}
		if started.IsZero() {
			started = metav1.Now()
			annotations[batchStartedAtAnnotation] = started.Time.UTC().Format(time.RFC3339Nano)
			changed = true
		}
		if annotations[batchPoolsAnnotation] != desiredPools {
			annotations[batchPoolsAnnotation] = desiredPools
			changed = true
		}
		// Keep the in-memory CR aligned: later batch steps and Status().Update
		// reuse this object and need the batch annotations + fresh RV.
		cb.SetAnnotations(annotations)
		cb.SetResourceVersion(latest.GetResourceVersion())
		if !changed {
			return nil
		}
		// Persist before pausing. A conflict leaves every MCP untouched.
		return r.Update(ctx, cb)
	})
	if err != nil {
		return metav1.Time{}, err
	}
	return started, nil
}

// resumeOrphanedBatch handles the crash/cancel window where metadata was
// persisted and MCPs may be paused, but status.remediationBatch is absent and
// the request annotation was removed.
func (r *ClusterBaselineReconciler) resumeOrphanedBatch(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline,
) error {
	annotations := cb.GetAnnotations()
	requestValue, hasRequest := annotations[batchApplyAnnotation]
	emptyRequest := hasRequest && len(batchRemediationNames(requestValue)) == 0
	if annotations[batchStartedAtAnnotation] == "" && annotations[batchPoolsAnnotation] == "" && !emptyRequest {
		return nil
	}
	owner := batchPauseOwner(cb)
	for _, pool := range batchRemediationNames(annotations[batchPoolsAnnotation]) {
		if err := r.setMCPPaused(ctx, pool, false, owner); err != nil {
			return err
		}
	}
	delete(annotations, batchStartedAtAnnotation)
	delete(annotations, batchPoolsAnnotation)
	if emptyRequest {
		delete(annotations, batchApplyAnnotation)
	}
	cb.SetAnnotations(annotations)
	return r.Update(ctx, cb)
}

func remediationOwnedByBaseline(cb *baselinev1alpha1.ClusterBaseline, rem *unstructured.Unstructured) bool {
	return ownedSuites(cb)[rem.GetLabels()[suiteLabel]]
}

// getBatchRemediation validates the confused-deputy boundary before the
// operator uses its stronger service-account permissions to apply a request.
// NotFound returns (nil, nil) so a race-deleted remediation can be skipped.
// NoMatch (CRDs absent) returns the error so batch start retries rather than
// pretending every target was missing and clearing the request annotation.
func (r *ClusterBaselineReconciler) getBatchRemediation(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, name string,
) (*unstructured.Unstructured, error) {
	rem := u(remediationGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if !remediationOwnedByBaseline(cb, rem) {
		return nil, fmt.Errorf("remediation %q does not belong to a selected baseline suite", name)
	}
	state, _, err := unstructured.NestedString(rem.Object, "status", "applicationState")
	if err != nil {
		return nil, fmt.Errorf("reading applicationState for remediation %q: %w", name, err)
	}
	if state == "MissingDependencies" {
		return nil, fmt.Errorf("remediation %q has missing dependencies", name)
	}
	return rem, nil
}

func (r *ClusterBaselineReconciler) applyOwnedRemediation(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, name string,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		rem, err := r.getBatchRemediation(ctx, cb, name)
		if err != nil || rem == nil {
			return err
		}
		apply, _, err := unstructured.NestedBool(rem.Object, "spec", "apply")
		if err != nil {
			return fmt.Errorf("reading spec.apply for remediation %q: %w", name, err)
		}
		if apply {
			return nil
		}
		before := rem.DeepCopy()
		if err := unstructured.SetNestedField(rem.Object, true, "spec", "apply"); err != nil {
			return err
		}
		return r.Patch(ctx, rem, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}

// resumeBatchPoolsOnDelete unpauses every MachineConfigPool a remediation batch
// may have paused. Prefer status.remediationBatch.pools; if status was lost but
// the batch-apply annotation remains, re-resolve pools from those remediations.
// Best-effort per pool: NotFound/NoMatch ignored; other errors fail deletion so
// we retry rather than drop the finalizer with pools still paused.
func (r *ClusterBaselineReconciler) resumeBatchPoolsOnDelete(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pools := map[string]bool{}
	owner := ""
	if batch := cb.Status.RemediationBatch; batch != nil {
		owner = batch.PauseOwner
		for _, p := range batch.Pools {
			if p != "" {
				pools[p] = true
			}
		}
	}
	// Status lost but annotation still present: rediscover pools from rem names.
	if len(pools) == 0 && cb.Annotations != nil {
		owner = batchPauseOwner(cb)
		for _, pool := range batchRemediationNames(cb.Annotations[batchPoolsAnnotation]) {
			pools[pool] = true
		}
		for _, name := range batchRemediationNames(cb.Annotations[batchApplyAnnotation]) {
			rem := u(remediationGVK)
			if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
				if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
					continue
				}
				return err
			}
			// A crafted foreign remediation must not make this finalizer mutate its
			// MachineConfigPool through the operator's service account.
			if !remediationOwnedByBaseline(cb, rem) {
				continue
			}
			if p := poolFromRemediation(rem); p != "" {
				pools[p] = true
			}
		}
	}
	for _, p := range slices.Sorted(maps.Keys(pools)) {
		if err := r.setMCPPaused(ctx, p, false, owner); err != nil {
			return err
		}
	}
	return nil
}

// applyRemediationBatch runs a two-phase batch apply driven by the batch-apply
// annotation: pause the affected MachineConfigPools and set apply on all listed
// remediations, then resume once they are Applied (or after a grace) so the pools
// reboot once. Resume is guaranteed: any failure still resumes the pools.
//
// The one-shot annotation is kept until pools are resumed. Clearing it before
// status.remediationBatch is persisted would leave pools paused forever if the
// end-of-reconcile Status().Update fails (annotation gone, batch nil, no recovery).
func (r *ClusterBaselineReconciler) applyRemediationBatch(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	batch := cb.Status.RemediationBatch
	names := ""
	if cb.Annotations != nil {
		names = cb.Annotations[batchApplyAnnotation]
	}

	if batch == nil {
		if strings.TrimSpace(names) == "" {
			return r.resumeOrphanedBatch(ctx, cb)
		}
		list := batchRemediationNames(names)
		// Annotation of only commas/whitespace: do not open an empty batch.
		if len(list) == 0 {
			return r.resumeOrphanedBatch(ctx, cb)
		}
		if len(list) > batchMaxRemediations {
			return fmt.Errorf("batch requests %d remediations; maximum is %d", len(list), batchMaxRemediations)
		}
		for _, name := range list {
			if errs := utilvalidation.IsDNS1123Subdomain(name); len(errs) > 0 {
				return fmt.Errorf("invalid remediation name %q: %s", name, strings.Join(errs, "; "))
			}
		}

		// Validate every existing target before any mutation. In particular, the
		// suite check prevents ClusterBaseline patch permission from becoming a
		// deputy that can apply arbitrary ComplianceRemediations.
		// Drop race-deleted (NotFound) names so status only lists remediations we
		// will actually apply. If none remain, clear the one-shot annotation
		// instead of opening a fake batch that "succeeds" with no work.
		pools := map[string]bool{}
		keep := make([]string, 0, len(list))
		for _, name := range list {
			rem, err := r.getBatchRemediation(ctx, cb, name)
			if err != nil {
				return err
			}
			if rem == nil {
				continue
			}
			keep = append(keep, name)
			if p := poolFromRemediation(rem); p != "" {
				pools[p] = true
			}
		}
		if len(keep) == 0 {
			log.FromContext(ctx).Info("remediation batch skipped: no remediations found",
				"requested", list)
			if cb.Annotations != nil {
				if _, ok := cb.Annotations[batchApplyAnnotation]; ok {
					delete(cb.Annotations, batchApplyAnnotation)
					return r.Update(ctx, cb)
				}
			}
			return nil
		}
		list = keep
		poolList := slices.Sorted(maps.Keys(pools))
		startedAt, err := r.ensureBatchMetadata(ctx, cb, poolList)
		if err != nil {
			return err
		}
		owner := batchPauseOwner(cb)
		// Pause first so all apply-triggered MachineConfig renders coalesce.
		// On a mid-list failure, unpause what we already paused this attempt so
		// a permanent error cannot leave a subset of pools paused with no batch.
		logger := log.FromContext(ctx)
		var paused []string
		for _, p := range poolList {
			if err := r.setMCPPaused(ctx, p, true, owner); err != nil {
				resumeFailed := false
				for _, done := range paused {
					if rerr := r.setMCPPaused(ctx, done, false, owner); rerr != nil {
						logger.Error(rerr, "failed to resume MachineConfigPool after pause failure", "pool", done)
						resumeFailed = true
					}
				}
				// If unpause itself failed, record the batch so batchResumeGrace
				// can force resume instead of leaving pools paused forever while
				// apply/pause keeps failing and status.remediationBatch stays nil.
				if resumeFailed {
					cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
						Phase: "Applying", Pools: append([]string(nil), paused...), Remediations: list, StartedAt: startedAt, PauseOwner: owner,
					}
				}
				return err
			}
			paused = append(paused, p)
		}
		for _, name := range list {
			if err := r.applyOwnedRemediation(ctx, cb, name); err != nil {
				// Resume any paused pools so a failure never leaves them paused.
				resumeFailed := false
				for _, p := range poolList {
					if rerr := r.setMCPPaused(ctx, p, false, owner); rerr != nil {
						logger.Error(rerr, "failed to resume MachineConfigPool after batch apply error", "pool", p)
						resumeFailed = true
					}
				}
				if resumeFailed {
					cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
						Phase: "Applying", Pools: poolList, Remediations: list, StartedAt: startedAt, PauseOwner: owner,
					}
				}
				return err
			}
		}
		// Keep the annotation until resume. status.remediationBatch is written by
		// the end-of-reconcile Status().Update; if that fails, the annotation still
		// drives a restart rather than orphaning paused pools.
		cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
			Phase: "Applying", Pools: poolList, Remediations: list, StartedAt: startedAt, PauseOwner: owner,
		}
		// Info: MCP pause is operationally sensitive; on-call needs a clear
		// start marker in logs when investigating stuck paused pools.
		logger.Info("remediation batch started", "remediations", len(list), "pools", poolList)
		return nil
	}

	// Applying: resume when every listed remediation is Applied, or past grace.
	// NotFound/NoMatch: remediation or CRDs gone; skip (do not block resume forever).
	// Transient Get errors must not look like Applied (would unpause early), but
	// must not bypass batchResumeGrace either (pools must never stay paused forever).
	// Also track whether any remediation is still apply=true: if none are (the
	// user reverted them all), the batch is cancelled and we resume at once.
	applied := true
	anyApplying := false
	var getErr error
	for _, name := range batch.Remediations {
		rem := u(remediationGVK)
		if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}
			getErr = err
			applied = false
			continue
		}
		if s, _, err := unstructured.NestedString(rem.Object, "status", "applicationState"); err != nil {
			// Wrong-type status must not look like Applied (would unpause early).
			getErr = err
			applied = false
			continue
		} else if s != "Applied" {
			applied = false
		}
		if a, _, err := unstructured.NestedBool(rem.Object, "spec", "apply"); err != nil {
			// Corrupt apply must not cancel the batch (false negative on anyApplying).
			getErr = err
			applied = false
			continue
		} else if a {
			anyApplying = true
		}
	}
	// Cancelled only when we saw every remediation cleanly (no transient error hid
	// an apply=true one), so a flaky Get never triggers an early resume.
	cancelled := !anyApplying && getErr == nil
	pastGrace := batchPastGrace(batch.StartedAt, time.Now())
	if applied || pastGrace || cancelled {
		for _, p := range batch.Pools {
			if err := r.setMCPPaused(ctx, p, false, batch.PauseOwner); err != nil {
				return err
			}
		}
		// Clear one-shot annotation after pools are resumed. Pools are safe even
		// if this Update fails; the next reconcile retries the clear.
		metadataChanged := false
		if cb.Annotations != nil {
			if value, ok := cb.Annotations[batchApplyAnnotation]; ok &&
				slices.Equal(batchRemediationNames(value), uniqueSortedStrings(batch.Remediations)) {
				delete(cb.Annotations, batchApplyAnnotation)
				metadataChanged = true
			}
			if _, ok := cb.Annotations[batchStartedAtAnnotation]; ok {
				delete(cb.Annotations, batchStartedAtAnnotation)
				metadataChanged = true
			}
			if _, ok := cb.Annotations[batchPoolsAnnotation]; ok {
				delete(cb.Annotations, batchPoolsAnnotation)
				metadataChanged = true
			}
		}
		if metadataChanged {
			if err := r.Update(ctx, cb); err != nil {
				return err
			}
		}
		reason := "applied"
		if cancelled {
			reason = "cancelled"
		} else if pastGrace {
			reason = "grace"
		}
		log.FromContext(ctx).Info("remediation batch finished", "reason", reason, "pools", batch.Pools, "remediations", len(batch.Remediations))
		cb.Status.RemediationBatch = nil
		return nil
	}
	// Still waiting: surface a transient Get so the controller requeues, but
	// only before grace expires (after grace we already resumed above).
	if getErr != nil {
		return getErr
	}
	return nil
}

type completedSuiteRun struct {
	earliest time.Time
	latest   time.Time
}

// completedSuiteTimes returns the member-scan completion range only when the
// suite and every status entry are complete. ComplianceSuite is the transaction
// boundary for a ScanSettingBinding; recording an individual scan would snapshot
// a partial multi-scan run.
func completedSuiteTimes(suite *unstructured.Unstructured, now time.Time) (completedSuiteRun, bool) {
	phase, _, err := unstructured.NestedString(suite.Object, "status", "phase")
	if err != nil || phase != "DONE" {
		return completedSuiteRun{}, false
	}
	statuses, found, err := unstructured.NestedSlice(suite.Object, "status", "scanStatuses")
	if err != nil || !found || len(statuses) == 0 {
		return completedSuiteRun{}, false
	}
	var run completedSuiteRun
	for _, raw := range statuses {
		status, ok := raw.(map[string]any)
		if !ok {
			return completedSuiteRun{}, false
		}
		memberPhase, _, err := unstructured.NestedString(status, "phase")
		if err != nil || memberPhase != "DONE" {
			return completedSuiteRun{}, false
		}
		ts, _, err := unstructured.NestedString(status, "endTimestamp")
		if err != nil {
			return completedSuiteRun{}, false
		}
		completed, ok := parseScanEndTimestamp(ts, now)
		if !ok {
			return completedSuiteRun{}, false
		}
		if run.earliest.IsZero() || completed.Before(run.earliest) {
			run.earliest = completed
		}
		if completed.After(run.latest) {
			run.latest = completed
		}
	}
	return run, !run.latest.IsZero()
}

// recordHistory advances score history and scan-diff state when every owned
// suite has a completed run. weights may be nil (Flat mode, or unit tests that
// only care about overall history); per-profile rings then use pass/fail counts.
func (r *ClusterBaselineReconciler) recordHistory(
	ctx context.Context,
	cb *baselinev1alpha1.ClusterBaseline,
	s *int32,
	currentFails []string,
	weights *scoreWeights,
) error {
	suiteList := uList(suiteGVK)
	if err := r.List(ctx, suiteList, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	expectedSuites := ownedSuites(cb)
	if len(expectedSuites) == 0 {
		return nil
	}
	now := time.Now()
	var latest time.Time
	completedSuites := make(map[string]completedSuiteRun, len(expectedSuites))
	for i := range suiteList.Items {
		item := &suiteList.Items[i]
		if !expectedSuites[item.GetName()] {
			continue
		}
		completed, ok := completedSuiteTimes(item, now)
		if !ok {
			return nil
		}
		completedSuites[item.GetName()] = completed
		if completed.latest.After(latest) {
			latest = completed.latest
		}
	}
	// Wait until every selected binding has a completed suite. This prevents a
	// fast profile from advancing global history while another is still running.
	if len(completedSuites) != len(expectedSuites) || latest.IsZero() {
		return nil
	}
	last := metav1.NewTime(latest)
	if cb.Status.LastScanTime != nil && !last.After(cb.Status.LastScanTime.Time) {
		// Never rewind LastScanTime when the suite with the newest endTimestamp
		// is dropped (profile/tp removed). On equal end time:
		// - refresh the latest history score when late results change the rollup
		// - append a first history point when an earlier pass had score=nil
		//   (all MANUAL/INFO) and a countable score appears for the same scan
		if last.Equal(cb.Status.LastScanTime) {
			cb.Status.History = syncHistorySnapshot(cb.Status.History, last, s)
			syncProfileHistory(cb, last, weights)
			// Keep the baseline for the next scan current when CheckResults arrive
			// after endTimestamp, and correct this scan's diff against its retained
			// prior-scan baseline.
			if cb.Status.DiffBaseScanTime != nil && last.Equal(cb.Status.DiffBaseScanTime) {
				syncFailureDiff(cb, currentFails, cb.Status.DiffBaseFailures)
			}
			cb.Status.PreviousFailures = slices.Clone(currentFails)
		}
		return nil
	}
	if cb.Status.LastScanTime != nil {
		// A DONE suite may still represent the previous scheduled run while another
		// suite has already completed the next one. Advance only after every suite's
		// newest member scan is newer than the prior global snapshot.
		for _, completed := range completedSuites {
			if !completed.earliest.After(cb.Status.LastScanTime.Time) {
				return nil
			}
		}
	}
	hadPreviousScan := cb.Status.LastScanTime != nil
	cb.Status.LastScanTime = &last
	cb.Status.History = syncHistorySnapshot(cb.Status.History, last, s)
	// A new scan completed: compute regressions vs the previous scan's failures,
	// then snapshot the current failures for next time, and append a per-profile
	// history point so each benchmark can be trended.
	if hadPreviousScan {
		cb.Status.DiffBaseFailures = slices.Clone(cb.Status.PreviousFailures)
		cb.Status.DiffBaseScanTime = &last
		syncFailureDiff(cb, currentFails, cb.Status.DiffBaseFailures)
	} else {
		// There is no previous completed scan to compare against. Reporting every
		// initial failure as a regression is misleading and triggers a false alert.
		cb.Status.NewlyFailed = nil
		cb.Status.Fixed = nil
		cb.Status.DiffBaseFailures = nil
		cb.Status.DiffBaseScanTime = nil
	}
	cb.Status.PreviousFailures = slices.Clone(currentFails)
	syncProfileHistory(cb, last, weights)
	return nil
}

// ensureComplianceDashboard creates the score-trend dashboard as a ConfigMap in
// openshift-config-managed labeled console.openshift.io/dashboard, so the console
// renders it under Observe -> Dashboards (no Grafana). Data needs user-workload
// monitoring + the metrics ServiceMonitor; the dashboard renders regardless.
// Best-effort: a write failure here is logged, not Degrading, since the dashboard
// is cosmetic and must never block scanning or status.
func (r *ClusterBaselineReconciler) ensureComplianceDashboard(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: dashboardName, Namespace: dashboardNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels["console.openshift.io/dashboard"] = "true"
		cm.Labels["app.kubernetes.io/part-of"] = "baseline-security"
		cm.Data = map[string]string{"baseline-security-compliance.json": complianceDashboardJSON}
		// cb is cluster-scoped, so a namespaced dependent in another namespace is a
		// valid ownerref target; the ConfigMap is GCed when the CR is deleted.
		return controllerutil.SetControllerReference(cb, cm, r.Scheme)
	})
	if err != nil {
		// Error (not Info): best-effort cosmetic resource, but operators need to
		// see RBAC/namespace failures when the dashboard never appears.
		log.FromContext(ctx).Error(err, "compliance dashboard configmap not reconciled")
	}
}

func (r *ClusterBaselineReconciler) ensureConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.Console.ManagementState == baselinev1alpha1.Removed {
		return r.removeConsolePlugin(ctx, cb)
	}
	image := os.Getenv("RELATED_IMAGE_CONSOLE_PLUGIN")
	if image == "" {
		// Soft-fail: still reconcile scans/status; requeue will retry when env is fixed.
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ImageMissing", "RELATED_IMAGE_CONSOLE_PLUGIN not set")
		return nil
	}
	if err := createIfMissing(ctx, r.Client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: pluginNS}}); err != nil {
		return err
	}

	labels := map[string]string{"app": pluginName}

	// Service first so service-ca can mint the serving-cert Secret before pods start.
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = pluginName + "-cert"
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		// This is an internal console backend. Clear every field that can retain
		// external exposure or is invalid after reconciling a hand-edited
		// LoadBalancer/ExternalName Service back to ClusterIP.
		svc.Spec.ExternalIPs = nil
		svc.Spec.ExternalName = ""
		svc.Spec.LoadBalancerIP = ""
		svc.Spec.LoadBalancerSourceRanges = nil
		svc.Spec.LoadBalancerClass = nil
		svc.Spec.AllocateLoadBalancerNodePorts = nil
		svc.Spec.ExternalTrafficPolicy = ""
		svc.Spec.HealthCheckNodePort = 0
		svc.Spec.PublishNotReadyAddresses = false
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name: "https", Port: 9443, TargetPort: intstr.FromInt32(9443), Protocol: corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(cb, svc, r.Scheme)
	}); err != nil {
		return err
	}

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		// Mutate owned fields only; leave selector immutable after create.
		if dep.Spec.Selector == nil {
			dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		}
		dep.Spec.Replicas = ptr.To(pluginReplicas)
		// maxUnavailable=1 makes DeploymentAvailable True at 1/2 ready, matching
		// pluginReadyMin=1: a single drained node must not false-Degrade the plugin.
		dep.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
			},
		}
		if dep.Spec.Template.Labels == nil {
			dep.Spec.Template.Labels = map[string]string{}
		}
		for k, v := range labels {
			dep.Spec.Template.Labels[k] = v
		}
		dep.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: ptr.To(true),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		}
		dep.Spec.Template.Spec.Affinity = preferredHostnameAntiAffinity(labels)
		applyPluginContainer(&dep.Spec.Template.Spec, image)
		return controllerutil.SetControllerReference(cb, dep, r.Scheme)
	}); err != nil {
		return err
	}

	cp := u(consolePluginGVK)
	cp.SetName(pluginName)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cp, func() error {
		cp.Object["spec"] = map[string]any{
			"displayName": "Baseline Security",
			"backend": map[string]any{
				"type": "Service",
				"service": map[string]any{
					"name": pluginName, "namespace": pluginNS, "port": int64(9443), "basePath": "/",
				},
			},
		}
		return controllerutil.SetControllerReference(cb, cp, r.Scheme)
	}); err != nil {
		if meta.IsNoMatchError(err) {
			// Console capability disabled: no ConsolePlugin CRD on the cluster.
			setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ConsoleMissing",
				"console CRDs not available (Console capability disabled)")
			return nil
		}
		return err
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			return err
		}
		plugins, _, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if err != nil {
			return fmt.Errorf("reading console plugins: %w", err)
		}
		if slices.Contains(plugins, pluginName) {
			return nil
		}
		if err := unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins"); err != nil {
			return fmt.Errorf("registering console plugin: %w", err)
		}
		return r.Update(ctx, console)
	}); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			// Soft-fail: still deploy plugin objects; registration retries later.
			setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ConsoleMissing",
				"consoles.operator.openshift.io/cluster not available")
			return nil
		}
		return err
	}

	// Re-read Deployment status so Ready is not claimed before pods are up.
	// Use pluginReadyMin (not full pluginReplicas) so a partial HA outage still
	// reports Deployed once the plugin can serve traffic.
	if err := r.Get(ctx, types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		return err
	}
	if dep.Status.ReadyReplicas < pluginReadyMin {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s has %d ready replicas (want >= %d of %d)",
				pluginNS, pluginName, dep.Status.ReadyReplicas, pluginReadyMin, pluginReplicas)
		if pluginDeploymentUnavailable(dep) {
			reason = "Unavailable"
			msg = fmt.Sprintf("Deployment %s/%s has no ready pods for >5m", pluginNS, pluginName)
		}
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, reason, msg)
		return nil
	}
	if !deploymentAvailable(dep) {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s ready pods present but Available is not True", pluginNS, pluginName)
		// Ready pods with Available=False past grace (e.g. progress deadline)
		// must not Progress forever.
		if deploymentAvailableFalsePastGrace(dep) {
			reason = "Unavailable"
			msg = fmt.Sprintf("Deployment %s/%s Available=False for >5m", pluginNS, pluginName)
		}
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, reason, msg)
		return nil
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	return nil
}

// deploymentAvailable is true when the Deployment Available condition is True.
// Missing condition is treated as not yet available.
func deploymentAvailable(dep *appsv1.Deployment) bool {
	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentAvailable {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// deploymentAvailableFalsePastGrace is true when Available has been False longer
// than pluginUnavailableGrace (distinct from zero-ready; ready pods may exist).
func deploymentAvailableFalsePastGrace(dep *appsv1.Deployment) bool {
	for _, c := range dep.Status.Conditions {
		if c.Type != appsv1.DeploymentAvailable || c.Status != corev1.ConditionFalse {
			continue
		}
		return !c.LastTransitionTime.IsZero() && time.Since(c.LastTransitionTime.Time) > pluginUnavailableGrace
	}
	return false
}

// pluginUnavailableGrace is how long the plugin Deployment may be unavailable
// before it is reported as Degraded rather than merely progressing.
const pluginUnavailableGrace = 5 * time.Minute

// pluginDeploymentUnavailable is true when the Deployment has been continuously
// below pluginReadyMin ready replicas longer than pluginUnavailableGrace.
// Prefer the Available condition's LastTransitionTime so a brief ReadyReplicas
// dip on an old Deployment is not treated as a permanent failure.
func pluginDeploymentUnavailable(dep *appsv1.Deployment) bool {
	if dep.Status.ReadyReplicas >= pluginReadyMin {
		return false
	}
	timeout := pluginUnavailableGrace
	for _, c := range dep.Status.Conditions {
		if c.Type != appsv1.DeploymentAvailable {
			continue
		}
		if c.LastTransitionTime.IsZero() {
			break
		}
		// Available False: time since it went down. Available True with zero
		// ready pods is pathological; still time-box from the last transition
		// so we do not Progress forever.
		return time.Since(c.LastTransitionTime.Time) > timeout
	}
	// No Available condition yet (brand-new object): use creation time.
	return !dep.CreationTimestamp.IsZero() && time.Since(dep.CreationTimestamp.Time) > timeout
}

// applyPluginContainer sets the plugin container, volume mounts, and volumes on the pod spec.
func applyPluginContainer(pod *corev1.PodSpec, image string) {
	// nginx serves static files; it never talks to the API server.
	pod.AutomountServiceAccountToken = ptr.To(false)
	pod.ServiceAccountName = "default"
	pod.HostNetwork = false
	pod.HostPID = false
	pod.HostIPC = false
	pod.ShareProcessNamespace = nil
	pod.EphemeralContainers = nil
	pod.NodeName = ""
	pod.NodeSelector = nil
	pod.Tolerations = nil
	pod.TopologySpreadConstraints = nil
	pod.RuntimeClassName = nil
	pod.PriorityClassName = ""
	pod.Priority = nil
	pod.PreemptionPolicy = ptr.To(corev1.PreemptLowerPriority)
	pod.ActiveDeadlineSeconds = nil
	pod.ReadinessGates = nil
	pod.HostAliases = nil
	pod.Hostname = ""
	pod.Subdomain = ""
	pod.SetHostnameAsFQDN = ptr.To(false)
	pod.OS = nil
	pod.SchedulingGates = nil
	pod.ResourceClaims = nil
	pod.Resources = nil
	pod.Overhead = nil
	pod.HostnameOverride = nil
	pod.WorkloadRef = nil
	pod.DNSConfig = nil
	pod.EnableServiceLinks = ptr.To(false)
	pod.DNSPolicy = corev1.DNSClusterFirst
	pod.RestartPolicy = corev1.RestartPolicyAlways
	pod.SchedulerName = corev1.DefaultSchedulerName
	pod.TerminationGracePeriodSeconds = ptr.To(int64(30))
	pullPolicy := corev1.PullIfNotPresent
	imageLeaf := image[strings.LastIndex(image, "/")+1:]
	if !strings.Contains(imageLeaf, ":") || strings.HasSuffix(imageLeaf, ":latest") {
		pullPolicy = corev1.PullAlways
	}
	container := corev1.Container{
		Name:            pluginName,
		Image:           image,
		ImagePullPolicy: pullPolicy,
		Ports:           []corev1.ContainerPort{{Name: "https", ContainerPort: 9443, Protocol: corev1.ProtocolTCP}},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			RunAsNonRoot:             ptr.To(true),
			// nginx pid/logs/temp use /tmp (emptyDir); rootfs stays immutable.
			ReadOnlyRootFilesystem: ptr.To(true),
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			// Static asset server; bound usage so a runaway cannot starve the node.
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		// TCP only: the serving cert may be absent at first start, so HTTP
		// probes would fail closed until service-ca mints the Secret.
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			InitialDelaySeconds: 5,
			TimeoutSeconds:      1,
			PeriodSeconds:       10,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			InitialDelaySeconds: 15,
			TimeoutSeconds:      1,
			PeriodSeconds:       20,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		TerminationMessagePath:   corev1.TerminationMessagePathDefault,
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "serving-cert", MountPath: "/var/serving-cert", ReadOnly: true},
			// Writable scratch for pid file and nginx temp paths (read-only rootfs).
			{Name: "tmp", MountPath: "/tmp"},
		},
	}
	// The Deployment is fully owned. Replacing the lists removes injected or
	// hand-added sidecars/init containers that would otherwise run unreviewed in
	// the plugin pod and survive every reconcile.
	pod.Containers = []corev1.Container{container}
	pod.InitContainers = nil

	// 0400: only the nginx UID can read the private key (default is 0644).
	const certMode int32 = 0o400
	// Bound /tmp so a compromised nginx process cannot fill the node disk.
	tmpLimit := resource.MustParse("32Mi")
	pod.Volumes = []corev1.Volume{
		{
			Name: "serving-cert",
			VolumeSource: corev1.VolumeSource{
				// Optional until service-ca mints the Secret.
				Secret: &corev1.SecretVolumeSource{
					SecretName:  pluginName + "-cert",
					Optional:    ptr.To(true),
					DefaultMode: ptr.To(certMode),
				},
			},
		},
		{
			Name: "tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &tmpLimit},
			},
		},
	}
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&baselinev1alpha1.ClusterBaseline{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("clusterbaseline").
		Build(r)
	if err != nil {
		return err
	}
	// Event-driven watches on the compliance CRs so a finished scan or a
	// remediation reaching Applied reconciles at once instead of up to a poll
	// interval later. The Compliance Operator is installed after startup, so its
	// CRDs may be absent initially; add the watches lazily once they exist. The
	// poll requeue in Reconcile keeps everything working until then and if the
	// informers ever fail, so this is strictly additive.
	return mgr.Add(&lazyComplianceWatch{
		ctrl:   c,
		cache:  mgr.GetCache(),
		mapper: mgr.GetRESTMapper(),
		gvks:   []schema.GroupVersionKind{suiteGVK, scanGVK, remediationGVK, checkResultGVK},
	})
}

// enqueueSingleton maps any compliance-CR event to a reconcile of the
// ClusterBaseline singleton, coalesced by the workqueue.
func enqueueSingleton(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() != complianceNamespace {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "cluster"}}}
}

// lazyComplianceWatch adds informers for the compliance CRs once their CRDs
// exist, retrying until then, so the manager starts cleanly before the
// Compliance Operator is installed.
type lazyComplianceWatch struct {
	ctrl   controller.Controller
	cache  cache.Cache
	mapper meta.RESTMapper
	gvks   []schema.GroupVersionKind
}

// Run on the leader only: the watches feed the same reconcile the leader owns.
func (l *lazyComplianceWatch) NeedLeaderElection() bool { return true }

func (l *lazyComplianceWatch) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("lazy-compliance-watch")
	pending := append([]schema.GroupVersionKind(nil), l.gvks...)
	for {
		var still []schema.GroupVersionKind
		for _, gvk := range pending {
			// RESTMapping fails with NoMatch until the CRD is registered; the
			// mapper is dynamic and refreshes, so a later attempt succeeds.
			if _, err := l.mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
				still = append(still, gvk)
				continue
			}
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)
			src := source.Kind(l.cache, client.Object(obj),
				handler.EnqueueRequestsFromMapFunc(enqueueSingleton))
			if err := l.ctrl.Watch(src); err != nil {
				logger.V(1).Info("watch not established yet", "kind", gvk.Kind, "error", err)
				still = append(still, gvk)
				continue
			}
			logger.Info("watching compliance resource", "kind", gvk.Kind)
		}
		if len(still) == 0 {
			return nil
		}
		pending = still
		// NewTimer (not time.After): stop on ctx cancel so a shutdown mid-wait
		// does not leave a 30s timer holding the Runnable goroutine's stack.
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
