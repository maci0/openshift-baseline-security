package controller

import (
	"context"
	"fmt"
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
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

const (
	complianceNamespace = "openshift-compliance"
	scanSettingName     = "baseline"
	finalizerName       = "baselinesecurity.io/cleanup"
	pluginName          = "baseline-security-console-plugin"
	pluginNS            = "openshift-baseline-security"
	suiteLabel          = "compliance.openshift.io/suite"
	historyMax          = 30
	pluginReplicas      = int32(2)
)

// Foreign CRs are unstructured so we do not import their Go API modules.
var (
	subscriptionGVK  = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}
	csvGVK           = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "ClusterServiceVersion"}
	scanSettingGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSetting"}
	bindingGVK       = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSettingBinding"}
	checkResultGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceCheckResult"}
	scanGVK          = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceScan"}
	consolePluginGVK = schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"}
	consoleGVK       = schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Console"}
	operatorGroupGVK = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"}
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
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=compliancecheckresults;compliancescans,verbs=get;list;watch
// +kubebuilder:rbac:groups=operators.coreos.com,resources=subscriptions;operatorgroups,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces;services,verbs=get;list;watch;create;update;patch
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
		if err := r.deregisterConsolePlugin(ctx); err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.RemoveFinalizer(cb, finalizerName) {
			return ctrl.Result{}, r.Update(ctx, cb)
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(cb, finalizerName) {
		return ctrl.Result{}, r.Update(ctx, cb) // update requeues
	}

	if err := r.ensureComplianceOperator(ctx, cb); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring compliance operator: %w", err)
	}
	if err := r.ensureScanConfig(ctx, cb); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring scan config: %w", err)
	}
	if err := r.ensureConsolePlugin(ctx, cb); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring console plugin: %w", err)
	}
	if err := r.aggregateStatus(ctx, cb); err != nil {
		return ctrl.Result{}, fmt.Errorf("aggregating status: %w", err)
	}
	if err := r.checkScanStorage(ctx, cb); err != nil {
		return ctrl.Result{}, fmt.Errorf("checking scan storage: %w", err)
	}
	// OpenShift-style rollup conditions (Available / Progressing / Degraded).
	// Degraded is set by checkScanStorage; Available/Progressing summarize readiness.
	setRollupConditions(cb)
	if err := r.Status().Update(ctx, cb); err != nil {
		return ctrl.Result{}, err
	}
	logger.V(1).Info("reconciled", "score", cb.Status.Score)
	// Poll while CRDs may be absent; requeue faster while still installing.
	requeue := time.Minute
	if progressing := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); progressing != nil && progressing.Status == metav1.ConditionTrue {
		requeue = 15 * time.Second
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

func bindingName(key baselinev1alpha1.ProfileKey) string { return "baseline-" + string(key) }

func ownedSuites(cb *baselinev1alpha1.ClusterBaseline) map[string]bool {
	s := make(map[string]bool, len(cb.Spec.Profiles))
	for _, key := range cb.Spec.Profiles {
		s[bindingName(key)] = true
	}
	return s
}

// matchesAnyProfile: name equals a profile or is a role-suffixed variant
// (ocp4-cis-node -> ocp4-cis-node-master). name is untrusted cluster data.
func matchesAnyProfile(name string, profiles map[string]bool) bool {
	for p := range profiles {
		if name == p || strings.HasPrefix(name, p+"-") {
			return true
		}
	}
	return false
}

// profileKeyFromSuite inverts bindingName ("baseline-<key>").
// Requires a non-empty key after the prefix so "baseline-" alone is rejected.
func profileKeyFromSuite(suite string) (baselinev1alpha1.ProfileKey, bool) {
	p, ok := strings.CutPrefix(suite, "baseline-")
	if !ok || p == "" {
		return "", false
	}
	return baselinev1alpha1.ProfileKey(p), true
}

// score is pass/(pass+fail)*100, or nil when there are no countable results.
func score(pass, fail int32) *int32 {
	if pass < 0 || fail < 0 || pass+fail == 0 {
		return nil
	}
	s := pass * 100 / (pass + fail)
	return &s
}

// withoutPlugin returns plugins without name (copy; does not mutate input).
func withoutPlugin(plugins []string, name string) []string {
	return slices.DeleteFunc(slices.Clone(plugins), func(p string) bool { return p == name })
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

// appendHistoryRing appends a snapshot and keeps at most max entries (oldest first).
// The returned slice does not alias the input backing array after truncation.
func appendHistoryRing(hist []baselinev1alpha1.ScoreSnapshot, t metav1.Time, s int32, max int) []baselinev1alpha1.ScoreSnapshot {
	hist = append(hist, baselinev1alpha1.ScoreSnapshot{Time: t, Score: s})
	if max > 0 && len(hist) > max {
		hist = append([]baselinev1alpha1.ScoreSnapshot(nil), hist[len(hist)-max:]...)
	}
	return hist
}

func setCond(cb *baselinev1alpha1.ClusterBaseline, typ string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: cb.Generation,
	})
}

// conditionProgressing is true for non-terminal False detail reasons that mean
// work is still in flight (not permanent admin action like Manual NotInstalled).
func conditionProgressing(c *metav1.Condition) bool {
	if c == nil || c.Status != metav1.ConditionFalse {
		return false
	}
	switch c.Reason {
	case "Installing", "CSVNotReady", "ImageMissing", "WaitingForPods", "CRDsMissing", "ConsoleMissing":
		return true
	default:
		return false
	}
}

// setRollupConditions sets Available and Progressing from detail conditions.
func setRollupConditions(cb *baselinev1alpha1.ClusterBaseline) {
	co := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	scan := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	plugin := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")

	coReady := co != nil && co.Status == metav1.ConditionTrue
	scanOK := scan != nil && scan.Status == metav1.ConditionTrue
	progressing := conditionProgressing(co) || conditionProgressing(scan) || conditionProgressing(plugin)

	if progressing {
		setCond(cb, "Progressing", metav1.ConditionTrue, "Reconciling", "installing or configuring dependencies")
	} else {
		setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	}
	if coReady && scanOK {
		setCond(cb, "Available", metav1.ConditionTrue, "AsExpected", "compliance operator ready and scans configured")
	} else {
		setCond(cb, "Available", metav1.ConditionFalse, "NotReady", "waiting for compliance operator and scan configuration")
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
	_ = unstructured.SetNestedStringSlice(og.Object, []string{complianceNamespace}, "spec", "targetNamespaces")
	if err := createIfMissing(ctx, r.Client, og); err != nil {
		return err
	}

	source := cb.Spec.ComplianceCatalogSource
	if source == "" {
		source = "redhat-operators"
	}
	sub = u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": source, "sourceNamespace": "openshift-marketplace",
	}
	if err := createIfMissing(ctx, r.Client, sub); err != nil {
		return err
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV")
	return nil
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
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
	if phase == "Succeeded" {
		cb.Status.ComplianceOperatorVersion = strings.TrimPrefix(csvName, "compliance-operator.v")
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionTrue, "CSVSucceeded", "")
		return nil
	}
	// Keep version empty until Succeeded so the UI does not show a green-looking
	// version string while the CSV is still Installing/Failed.
	cb.Status.ComplianceOperatorVersion = ""
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", "phase="+phase)
	return nil
}

func (r *ClusterBaselineReconciler) ensureScanConfig(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	ss := u(scanSettingGVK)
	ss.SetName(scanSettingName)
	ss.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		autoApply := cb.Spec.Remediation.Apply == baselinev1alpha1.RemediationApplyAutomatic
		schedule := cb.Spec.Schedule
		if schedule == "" {
			schedule = "0 1 * * *"
		}
		ss.Object["schedule"] = schedule
		ss.Object["roles"] = []any{"worker", "master"}
		ss.Object["rawResultStorage"] = map[string]any{"size": "1Gi", "rotation": int64(3)}
		ss.Object["autoApplyRemediations"] = autoApply
		ss.Object["autoUpdateRemediations"] = autoApply
		return controllerutil.SetControllerReference(cb, ss, r.Scheme)
	})
	if err != nil {
		if meta.IsNoMatchError(err) {
			setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing",
				"compliance.openshift.io CRDs not installed")
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

	bindings := uList(bindingGVK)
	if err := r.List(ctx, bindings, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing",
				"compliance.openshift.io CRDs not installed")
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
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	return nil
}

// checkScanStorage flags Degraded when owned scan PVCs stay Pending (no default SC).
func (r *ClusterBaselineReconciler) checkScanStorage(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(complianceNamespace)); err != nil {
		// Namespace may not exist yet while CO is installing.
		if apierrors.IsNotFound(err) {
			setCond(cb, "Degraded", metav1.ConditionFalse, "AsExpected", "")
			return nil
		}
		return err
	}
	profiles := map[string]bool{}
	for _, key := range cb.Spec.Profiles {
		for _, p := range key.ProfileNames() {
			profiles[p] = true
		}
	}
	var pending []string
	for _, pvc := range pvcs.Items {
		if matchesAnyProfile(pvc.Name, profiles) &&
			pvc.Status.Phase == corev1.ClaimPending &&
			time.Since(pvc.CreationTimestamp.Time) > 2*time.Minute {
			pending = append(pending, pvc.Name)
		}
	}
	if len(pending) > 0 {
		setCond(cb, "Degraded", metav1.ConditionTrue, "ScanStoragePending",
			fmt.Sprintf("PVC(s) %s/%s Pending >2m; need a default StorageClass",
				complianceNamespace, strings.Join(pending, ", ")))
		return nil
	}
	setCond(cb, "Degraded", metav1.ConditionFalse, "AsExpected", "")
	return nil
}

// deregisterConsolePlugin drops our entry from consoles.operator.openshift.io/cluster.
// Owned Deployment/Service/ConsolePlugin are GCed via owner refs on CR delete.
func (r *ClusterBaselineReconciler) deregisterConsolePlugin(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			return client.IgnoreNotFound(err)
		}
		plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		kept := withoutPlugin(plugins, pluginName)
		if len(kept) == len(plugins) {
			return nil
		}
		_ = unstructured.SetNestedStringSlice(console.Object, kept, "spec", "plugins")
		return r.Update(ctx, console)
	})
}

// removeConsolePlugin tears down plugin objects when managementState is Removed.
func (r *ClusterBaselineReconciler) removeConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	cp := u(consolePluginGVK)
	cp.SetName(pluginName)
	if err := client.IgnoreNotFound(r.Delete(ctx, cp)); err != nil {
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
			cb.Status.LastScanTime = nil
			cb.Status.History = nil
			return nil
		}
		return err
	}

	byProfile := map[baselinev1alpha1.ProfileKey]*baselinev1alpha1.ProfileStatus{}
	for _, key := range cb.Spec.Profiles {
		byProfile[key] = &baselinev1alpha1.ProfileStatus{Key: key, ProfileNames: key.ProfileNames()}
	}

	var pass, fail int32
	for _, item := range list.Items {
		key, ok := profileKeyFromSuite(item.GetLabels()[suiteLabel])
		ps := byProfile[key]
		if !ok || ps == nil {
			continue
		}
		status, _, _ := unstructured.NestedString(item.Object, "status")
		switch status {
		case "PASS":
			ps.Pass++
			pass++
		case "FAIL":
			ps.Fail++
			fail++
		case "MANUAL":
			ps.Manual++
		case "ERROR":
			ps.Error++
		case "NOT-APPLICABLE":
			ps.NotApplicable++
		}
	}

	cb.Status.Profiles = cb.Status.Profiles[:0]
	for _, key := range cb.Spec.Profiles {
		cb.Status.Profiles = append(cb.Status.Profiles, *byProfile[key])
	}
	if s := score(pass, fail); s != nil {
		cb.Status.Score = s
		r.recordHistory(ctx, cb, *s)
	} else {
		cb.Status.Score = nil
	}
	return nil
}

func (r *ClusterBaselineReconciler) recordHistory(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, s int32) {
	scans := uList(scanGVK)
	if err := r.List(ctx, scans, client.InNamespace(complianceNamespace)); err != nil {
		return
	}
	suites := ownedSuites(cb)
	var latest time.Time
	for _, item := range scans.Items {
		if suite := item.GetLabels()[suiteLabel]; suite == "" || !suites[suite] {
			continue
		}
		ts, _, _ := unstructured.NestedString(item.Object, "status", "endTimestamp")
		if t, err := time.Parse(time.RFC3339, ts); err == nil && t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return
	}
	last := metav1.NewTime(latest)
	if cb.Status.LastScanTime != nil && cb.Status.LastScanTime.Equal(&last) {
		return
	}
	cb.Status.LastScanTime = &last
	cb.Status.History = appendHistoryRing(cb.Status.History, last, s, historyMax)
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
		return err
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			return err
		}
		plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if slices.Contains(plugins, pluginName) {
			return nil
		}
		_ = unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins")
		return r.Update(ctx, console)
	}); err != nil {
		if apierrors.IsNotFound(err) {
			// Soft-fail: still deploy plugin objects; registration retries later.
			setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ConsoleMissing",
				"consoles.operator.openshift.io/cluster not found")
			return nil
		}
		return err
	}

	// Re-read Deployment status so Ready is not claimed before pods are up.
	// Use the managed replica count (not cached Spec.Replicas) to avoid a
	// false Deployed when the informer has not observed our CreateOrUpdate yet.
	if err := r.Get(ctx, types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		return err
	}
	if dep.Status.ReadyReplicas < pluginReplicas {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s has %d/%d ready replicas", pluginNS, pluginName, dep.Status.ReadyReplicas, pluginReplicas)
		if pluginDeploymentUnavailable(dep, 5*time.Minute) {
			reason = "Unavailable"
			msg = fmt.Sprintf("Deployment %s/%s has no ready pods for >5m", pluginNS, pluginName)
		}
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, reason, msg)
		return nil
	}
	if !deploymentAvailable(dep) {
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s ready pods present but Available is not True", pluginNS, pluginName))
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

// pluginDeploymentUnavailable is true when the Deployment has been continuously
// unavailable longer than timeout. Prefer the Available condition's
// LastTransitionTime so a brief ReadyReplicas dip on an old Deployment is not
// treated as a permanent failure.
func pluginDeploymentUnavailable(dep *appsv1.Deployment, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	for _, c := range dep.Status.Conditions {
		if c.Type != appsv1.DeploymentAvailable {
			continue
		}
		if c.Status == corev1.ConditionFalse {
			return !c.LastTransitionTime.IsZero() && time.Since(c.LastTransitionTime.Time) > timeout
		}
		// Available True/Unknown with ReadyReplicas==0 is transient; not a failure.
		return false
	}
	// No Available condition yet (brand-new object): use creation time.
	return !dep.CreationTimestamp.IsZero() && time.Since(dep.CreationTimestamp.Time) > timeout
}

// applyPluginContainer sets the plugin container, volume mounts, and volumes on the pod spec.
func applyPluginContainer(pod *corev1.PodSpec, image string) {
	container := corev1.Container{
		Name:  pluginName,
		Image: image,
		Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 9443, Protocol: corev1.ProtocolTCP}},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			RunAsNonRoot:             ptr.To(true),
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			InitialDelaySeconds: 5,
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "serving-cert", MountPath: "/var/serving-cert", ReadOnly: true},
		},
	}
	found := false
	for i := range pod.Containers {
		if pod.Containers[i].Name == pluginName {
			pod.Containers[i] = container
			found = true
			break
		}
	}
	if !found {
		pod.Containers = append(pod.Containers, container)
	}

	vol := corev1.Volume{
		Name: "serving-cert",
		VolumeSource: corev1.VolumeSource{
			// Optional until service-ca mints the Secret.
			Secret: &corev1.SecretVolumeSource{
				SecretName: pluginName + "-cert",
				Optional:   ptr.To(true),
			},
		},
	}
	volFound := false
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == "serving-cert" {
			pod.Volumes[i] = vol
			volFound = true
			break
		}
	}
	if !volFound {
		pod.Volumes = append(pod.Volumes, vol)
	}
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&baselinev1alpha1.ClusterBaseline{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("clusterbaseline").
		Complete(r)
}
