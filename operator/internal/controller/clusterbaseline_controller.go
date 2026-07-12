package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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
	// historyMax aliases the API constant so ring clamps stay CRD-aligned.
	historyMax = baselinev1alpha1.HistoryMax
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
// OperatorGroups need update/patch so targetNamespaces stays scoped to
// openshift-compliance after create (empty OG installs CO cluster-wide).
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorgroups,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// ConfigMaps: the console score-trend dashboard in openshift-config-managed.
// Get/CreateOrUpdate only (no informer watch); list/watch not required.
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// Plugin HA: keep one ready pod during voluntary disruptions (drain/upgrade).
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=get;list;watch;update;patch

func (r *ClusterBaselineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, req.NamespacedName, cb); err != nil {
		// CR gone: clear gauges so score/fail/batch alerts cannot stick on a
		// deleted posture until the process restarts.
		if client.IgnoreNotFound(err) == nil {
			logger.Info("ClusterBaseline gone; cleared published metrics", "name", req.Name)
			clearPublishedMetrics()
		} else {
			// API/timeout failures: CRT also logs the reconcile error, but without
			// the object name or that metrics were intentionally left unchanged.
			logger.Error(err, "get ClusterBaseline failed", "name", req.Name)
		}
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
			// Finalizer gone: object is about to GC. Clear gauges now; a later
			// NotFound reconcile may never run if nothing re-enqueues.
			logger.Info("finalizer removed; cleared published metrics", "name", cb.Name)
			clearPublishedMetrics()
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(cb, finalizerName) {
		if err := r.Update(ctx, cb); err != nil {
			logger.Error(err, "add finalizer failed", "name", cb.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // update requeues
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
	// Publish metrics only after Status().Update succeeds so concurrent annotation
	// patches (console waiver/schedule/batch) that conflict the status write cannot
	// leave gauges ahead of the CR for a full requeue interval.
	sanitizeStatusForUpdate(cb)
	setRollupConditions(cb)
	if err := r.Status().Update(ctx, cb); err != nil {
		logger.Error(err, "status update failed", "name", cb.Name)
		return ctrl.Result{}, err
	}
	publishMetrics(cb)
	// Posture fields for incident reconstruction without scraping metrics/CR.
	// Degraded at Info (default level): rollup failures (ScanStorage, InstallStalled,
	// InvalidSchedule, plugin) succeed reconcile and would otherwise be silent until
	// the 15m ClusterBaselineDegraded alert. Healthy success stays V(1) to avoid 1m noise.
	var score any
	if cb.Status.Score != nil {
		score = *cb.Status.Score
	}
	failN, errN, inconsistentN := 0, 0, 0
	for _, p := range cb.Status.Profiles {
		// ResultCounts is embedded (inline); use promoted fields (staticcheck QF1008).
		failN += int(p.Fail)
		errN += int(p.Error)
		inconsistentN += int(p.Inconsistent)
	}
	for _, tp := range cb.Status.TailoredProfiles {
		failN += int(tp.Fail)
		errN += int(tp.Error)
		inconsistentN += int(tp.Inconsistent)
	}
	keysAndValues := []any{
		"name", cb.Name,
		"score", score,
		"fail", failN,
		"error", errN,
		"inconsistent", inconsistentN,
		"newlyFailed", len(cb.Status.NewlyFailed),
		"available", condTrue(cb, "Available"),
		"degraded", condTrue(cb, "Degraded"),
		"batchActive", cb.Status.RemediationBatch != nil,
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c != nil && c.Status == metav1.ConditionTrue {
		logger.Info("reconciled with Degraded condition",
			append(keysAndValues, "reason", c.Reason, "message", c.Message)...)
	} else {
		logger.V(1).Info("reconciled", keysAndValues...)
	}
	return ctrl.Result{RequeueAfter: requeueAfter(cb)}, nil
}

// condTrue is true when the named status condition is present and True.
func condTrue(cb *baselinev1alpha1.ClusterBaseline, typ string) bool {
	c := meta.FindStatusCondition(cb.Status.Conditions, typ)
	return c != nil && c.Status == metav1.ConditionTrue
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
				// Info (not V(1)): CRDs are present so this is not install lag.
				// Default production log level would hide a permanent Watch
				// failure (RBAC, cache) and leave only the 1m poll as safety net.
				logger.Info("watch not established yet; will retry", "kind", gvk.Kind, "error", err)
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
