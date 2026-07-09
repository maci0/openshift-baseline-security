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
	if err := r.Status().Update(ctx, cb); err != nil {
		return ctrl.Result{}, err
	}
	logger.V(1).Info("reconciled", "score", cb.Status.Score)
	// ponytail: poll compliance CRs (CRDs absent until CO installs). Owns plugin objects.
	return ctrl.Result{RequeueAfter: time.Minute}, nil
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

// withoutPlugin drops name from plugins (may reuse the input slice backing).
func withoutPlugin(plugins []string, name string) []string {
	kept := plugins[:0]
	for _, p := range plugins {
		if p != name {
			kept = append(kept, p)
		}
	}
	return kept
}

// appendHistoryRing appends a snapshot and keeps at most max entries (oldest first).
func appendHistoryRing(hist []baselinev1alpha1.ScoreSnapshot, t metav1.Time, s int32, max int) []baselinev1alpha1.ScoreSnapshot {
	hist = append(hist, baselinev1alpha1.ScoreSnapshot{Time: t, Score: s})
	if max > 0 && len(hist) > max {
		hist = hist[len(hist)-max:]
	}
	return hist
}

func setCond(cb *baselinev1alpha1.ClusterBaseline, typ string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{Type: typ, Status: status, Reason: reason, Message: msg})
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
	if cb.Spec.InstallComplianceOperator == baselinev1alpha1.InstallManual {
		return nil
	}

	sub := u(subscriptionGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if err == nil {
		return r.setComplianceOperatorReady(ctx, cb, sub)
	}
	if !apierrors.IsNotFound(err) {
		return err
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
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "installedCSV empty")
		return nil
	}
	cb.Status.ComplianceOperatorVersion = strings.TrimPrefix(csvName, "compliance-operator.v")

	csv := u(csvGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: csvName}, csv); err != nil {
		if apierrors.IsNotFound(err) {
			setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV "+csvName)
			return nil
		}
		return err
	}
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
	if phase == "Succeeded" {
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionTrue, "CSVSucceeded", "")
		return nil
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", "phase="+phase)
	return nil
}

func (r *ClusterBaselineReconciler) ensureScanConfig(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	ss := u(scanSettingGVK)
	ss.SetName(scanSettingName)
	ss.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		autoApply := cb.Spec.Remediation.Apply == baselinev1alpha1.RemediationApplyAutomatic
		ss.Object["schedule"] = cb.Spec.Schedule
		ss.Object["roles"] = []any{"worker", "master"}
		ss.Object["rawResultStorage"] = map[string]any{"size": "1Gi", "rotation": int64(3)}
		ss.Object["autoApplyRemediations"] = autoApply
		ss.Object["autoUpdateRemediations"] = autoApply
		return controllerutil.SetControllerReference(cb, ss, r.Scheme)
	})
	if err != nil {
		if meta.IsNoMatchError(err) {
			return nil // CO CRDs not installed yet
		}
		return err
	}

	for _, key := range cb.Spec.Profiles {
		binding := u(bindingGVK)
		binding.SetName(bindingName(key))
		binding.SetNamespace(complianceNamespace)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
			profiles := make([]any, 0, len(key.ProfileNames()))
			for _, p := range key.ProfileNames() {
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
	if err := r.List(ctx, bindings, client.InNamespace(complianceNamespace)); err != nil && !meta.IsNoMatchError(err) {
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
}

// removeConsolePlugin tears down plugin objects when spec.console.enabled=false.
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
		svc.Spec.Ports = []corev1.ServicePort{{Port: 9443, TargetPort: intstr.FromInt32(9443)}}
		return controllerutil.SetControllerReference(cb, svc, r.Scheme)
	}); err != nil {
		return err
	}

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec = appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  pluginName,
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: 9443}},
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
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
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
					}},
					Volumes: []corev1.Volume{{
						Name: "serving-cert",
						VolumeSource: corev1.VolumeSource{
							// Optional until service-ca writes the Secret (avoids CreateContainerConfigError).
							Secret: &corev1.SecretVolumeSource{
								SecretName: pluginName + "-cert",
								Optional:   ptr.To(true),
							},
						},
					}},
				},
			},
		}
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

	console := u(consoleGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return err
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if !slices.Contains(plugins, pluginName) {
		_ = unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins")
		if err := r.Update(ctx, console); err != nil {
			return err
		}
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	return nil
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&baselinev1alpha1.ClusterBaseline{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("clusterbaseline").
		Complete(r)
}
