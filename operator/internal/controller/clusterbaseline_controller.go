package controller

import (
	"context"
	"fmt"
	"os"
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
	bindingNamePrefix   = "baseline-"
	suiteLabel          = "compliance.openshift.io/suite"
	scanNameLabel       = "compliance.openshift.io/scan-name"
)

// Compliance Operator and OLM resources are accessed unstructured: importing
// their Go APIs would pull both dependency trees into this module for four
// object shapes we only create/read. Revisit if the surface grows.
var (
	subscriptionGVK  = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}
	csvGVK           = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "ClusterServiceVersion"}
	scanSettingGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSetting"}
	bindingGVK       = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSettingBinding"}
	checkResultGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceCheckResult"}
	scanGVK          = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceScan"}
	consolePluginGVK = schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"}
	consoleGVK       = schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Console"}
)

// ClusterBaselineReconciler reconciles the ClusterBaseline singleton.
type ClusterBaselineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=scansettings;scansettingbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=compliancecheckresults;compliancesuites;compliancescans,verbs=get;list;watch
// +kubebuilder:rbac:groups=operators.coreos.com,resources=subscriptions;operatorgroups,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces;services;configmaps,verbs=get;list;watch;create;update;patch
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
		// The update event retriggers reconciliation with a fresh object.
		return ctrl.Result{}, r.Update(ctx, cb)
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
	// Compliance CRDs do not exist until the Compliance Operator is installed;
	// a manager-start watch on a missing CRD fails. Poll for check results and
	// Own in-scheme plugin objects (Deployment/Service/ConfigMap) instead.
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// bindingName returns the ScanSettingBinding name for a profile key.
// The Compliance Operator names the ComplianceSuite after the binding, and
// labels check results with compliance.openshift.io/suite=<binding name>.
func bindingName(key baselinev1alpha1.ProfileKey) string {
	return bindingNamePrefix + string(key)
}

// ownedSuites is the set of suite names this CR owns (one per selected profile).
func ownedSuites(cb *baselinev1alpha1.ClusterBaseline) map[string]bool {
	s := make(map[string]bool, len(cb.Spec.Profiles))
	for _, key := range cb.Spec.Profiles {
		s[bindingName(key)] = true
	}
	return s
}

// selectedProfileNames is the set of Compliance Operator Profile names for the
// selected profile keys.
func selectedProfileNames(cb *baselinev1alpha1.ClusterBaseline) map[string]bool {
	names := map[string]bool{}
	for _, key := range cb.Spec.Profiles {
		for _, p := range key.ProfileNames() {
			names[p] = true
		}
	}
	return names
}

// matchesAnyProfile reports whether name equals a profile name or is a
// role-suffixed variant of one (ocp4-cis-node -> ocp4-cis-node-master).
// name comes from cluster object names/labels, i.e. untrusted input.
func matchesAnyProfile(name string, profiles map[string]bool) bool {
	for p := range profiles {
		if name == p || strings.HasPrefix(name, p+"-") {
			return true
		}
	}
	return false
}

// longestProfileMatch maps a scan name (untrusted label value) to the profile
// key of the longest matching profile name, so ocp4-cis does not swallow
// ocp4-cis-node-* scans.
func longestProfileMatch(profileToKey map[string]baselinev1alpha1.ProfileKey, scan string) (baselinev1alpha1.ProfileKey, bool) {
	best := ""
	for p := range profileToKey {
		if (scan == p || strings.HasPrefix(scan, p+"-")) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return "", false
	}
	return profileToKey[best], true
}

// ensureComplianceOperator creates namespace + OperatorGroup + Subscription
// for the Compliance Operator unless it is already installed or installation
// is disabled. Deleting ClusterBaseline does not uninstall the Compliance
// Operator (shared cluster resource; leave it for other consumers).
func (r *ClusterBaselineReconciler) ensureComplianceOperator(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.InstallComplianceOperator != nil && !*cb.Spec.InstallComplianceOperator {
		return nil
	}

	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if err == nil {
		return r.setComplianceOperatorReady(ctx, cb, sub)
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: complianceNamespace}}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	og := &unstructured.Unstructured{}
	og.SetGroupVersionKind(schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"})
	og.SetName("compliance-operator")
	og.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedStringSlice(og.Object, []string{complianceNamespace}, "spec", "targetNamespaces")
	if err := r.Create(ctx, og); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	sub = &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	source := cb.Spec.ComplianceCatalogSource
	if source == "" {
		source = "redhat-operators"
	}
	sub.Object["spec"] = map[string]any{
		"name":            "compliance-operator",
		"channel":         "stable",
		"source":          source,
		"sourceNamespace": "openshift-marketplace",
	}
	if err := r.Create(ctx, sub); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ComplianceOperatorReady", Status: metav1.ConditionFalse, Reason: "Installing",
		Message: "Subscription created; waiting for CSV to succeed",
	})
	return nil
}

// setComplianceOperatorReady sets the condition from Subscription installedCSV
// and the CSV phase (Succeeded required for True).
func (r *ClusterBaselineReconciler) setComplianceOperatorReady(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, sub *unstructured.Unstructured) error {
	csvName, _, _ := unstructured.NestedString(sub.Object, "status", "installedCSV")
	if csvName == "" {
		meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
			Type: "ComplianceOperatorReady", Status: metav1.ConditionFalse, Reason: "Installing",
			Message: "Subscription present but installedCSV is empty",
		})
		return nil
	}
	cb.Status.ComplianceOperatorVersion = strings.TrimPrefix(csvName, "compliance-operator.v")

	csv := &unstructured.Unstructured{}
	csv.SetGroupVersionKind(csvGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: csvName}, csv); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
				Type: "ComplianceOperatorReady", Status: metav1.ConditionFalse, Reason: "Installing",
				Message: fmt.Sprintf("waiting for CSV %s", csvName),
			})
			return nil
		}
		return err
	}
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
	if phase == "Succeeded" {
		meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
			Type: "ComplianceOperatorReady", Status: metav1.ConditionTrue, Reason: "CSVSucceeded",
			Message: fmt.Sprintf("CSV %s is Succeeded", csvName),
		})
		return nil
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ComplianceOperatorReady", Status: metav1.ConditionFalse, Reason: "CSVNotReady",
		Message: fmt.Sprintf("CSV %s phase=%s", csvName, phase),
	})
	return nil
}

// ensureScanConfig owns one ScanSetting and one ScanSettingBinding per
// selected profile key.
func (r *ClusterBaselineReconciler) ensureScanConfig(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	ss := &unstructured.Unstructured{}
	ss.SetGroupVersionKind(scanSettingGVK)
	ss.SetName(scanSettingName)
	ss.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		autoApply := cb.Spec.Remediation.AutoApply != nil && *cb.Spec.Remediation.AutoApply
		ss.Object["schedule"] = cb.Spec.Schedule
		ss.Object["roles"] = []any{"worker", "master"}
		ss.Object["rawResultStorage"] = map[string]any{"size": "1Gi", "rotation": int64(3)}
		ss.Object["autoApplyRemediations"] = autoApply
		ss.Object["autoUpdateRemediations"] = autoApply
		return controllerutil.SetControllerReference(cb, ss, r.Scheme)
	})
	if err != nil {
		// The ScanSetting CRD is absent until the Compliance Operator is up;
		// tolerate and let the next reconcile retry.
		if meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	for _, key := range cb.Spec.Profiles {
		binding := &unstructured.Unstructured{}
		binding.SetGroupVersionKind(bindingGVK)
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
	// Prune bindings for deselected profiles (all ours carry the baseline- prefix
	// and our owner reference).
	bindings := &unstructured.UnstructuredList{}
	bindings.SetGroupVersionKind(bindingGVK.GroupVersion().WithKind(bindingGVK.Kind + "List"))
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
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ScanConfigured", Status: metav1.ConditionTrue, Reason: "BindingsCreated",
	})
	return nil
}

// checkScanStorage surfaces the silent-hang failure mode where scan PVCs stay
// Pending because the cluster has no default StorageClass: scans never start
// and nothing in the Compliance Operator reports an error.
// Only PVCs whose names match selected profile scans (or role-suffixed variants)
// are considered, so unrelated CO users do not flip our Degraded condition.
func (r *ClusterBaselineReconciler) checkScanStorage(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(complianceNamespace)); err != nil {
		return err
	}
	profileNames := selectedProfileNames(cb)

	var pending []string
	for _, pvc := range pvcs.Items {
		if !matchesAnyProfile(pvc.Name, profileNames) {
			continue
		}
		if pvc.Status.Phase == corev1.ClaimPending && time.Since(pvc.CreationTimestamp.Time) > 2*time.Minute {
			pending = append(pending, pvc.Name)
		}
	}
	if len(pending) > 0 {
		meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
			Type:   "Degraded",
			Status: metav1.ConditionTrue,
			Reason: "ScanStoragePending",
			Message: fmt.Sprintf("PVC(s) %s/%s have been Pending for over 2 minutes; "+
				"compliance scans cannot store results. Ensure the cluster has a default "+
				"StorageClass (or configure ScanSetting rawResultStorage accordingly).",
				complianceNamespace, strings.Join(pending, ", ")),
		})
		return nil
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "Degraded", Status: metav1.ConditionFalse, Reason: "AsExpected",
	})
	return nil
}

// deregisterConsolePlugin removes the plugin from the console operator config.
// Owned objects (Deployment, Service, ConsolePlugin) die via owner references;
// the console config entry is the one thing garbage collection can't clean.
func (r *ClusterBaselineReconciler) deregisterConsolePlugin(ctx context.Context) error {
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return client.IgnoreNotFound(err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	kept := plugins[:0]
	for _, p := range plugins {
		if p != pluginName {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(plugins) {
		return nil
	}
	_ = unstructured.SetNestedStringSlice(console.Object, kept, "spec", "plugins")
	return r.Update(ctx, console)
}

// removeConsolePlugin tears down plugin objects and deregisters from the
// console operator (used when spec.console.enabled is false).
func (r *ClusterBaselineReconciler) removeConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	// Cluster-scoped ConsolePlugin (ownerRef may still GC it; delete explicitly).
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(consolePluginGVK)
	cp.SetName(pluginName)
	if err := r.Delete(ctx, cp); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	// Namespaced objects: Deployment, Service, ConfigMap.
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: pluginName + "-nginx", Namespace: pluginNS}},
	} {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := r.deregisterConsolePlugin(ctx); err != nil {
		return err
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ConsolePluginReady", Status: metav1.ConditionFalse, Reason: "Disabled",
		Message: "spec.console.enabled is false",
	})
	return nil
}

// aggregateStatus recomputes per-profile counts and the overall score from
// ComplianceCheckResults belonging to our ScanSettingBindings (suite label).
func (r *ClusterBaselineReconciler) aggregateStatus(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	if err := r.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	suites := ownedSuites(cb)
	byProfile := map[baselinev1alpha1.ProfileKey]*baselinev1alpha1.ProfileStatus{}
	profileToKey := map[string]baselinev1alpha1.ProfileKey{}
	for _, key := range cb.Spec.Profiles {
		byProfile[key] = &baselinev1alpha1.ProfileStatus{Key: key, ProfileNames: key.ProfileNames()}
		for _, p := range key.ProfileNames() {
			profileToKey[p] = key
		}
	}
	var pass, fail int32
	for _, item := range list.Items {
		// Prefer suite label (binding name) so foreign scans of the same profile
		// are excluded. Fall back to scan-name matching only when the suite label
		// is one of ours (label always present on modern CO; empty suite skips).
		suite := item.GetLabels()[suiteLabel]
		if suite == "" || !suites[suite] {
			continue
		}
		scan := item.GetLabels()[scanNameLabel]
		key, ok := longestProfileMatch(profileToKey, scan)
		if !ok {
			continue
		}
		status, _, _ := unstructured.NestedString(item.Object, "status")
		ps := byProfile[key]
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
	if pass+fail > 0 {
		score := pass * 100 / (pass + fail)
		cb.Status.Score = &score
		r.recordHistory(ctx, cb, score)
	} else {
		// No countable results for selected profiles: clear stale score.
		cb.Status.Score = nil
	}
	return nil
}

// recordHistory sets lastScanTime from the newest owned scan endTimestamp and
// appends a score snapshot once per completed scan run (30-entry ring, oldest first).
func (r *ClusterBaselineReconciler) recordHistory(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, score int32) {
	scans := &unstructured.UnstructuredList{}
	scans.SetGroupVersionKind(scanGVK.GroupVersion().WithKind(scanGVK.Kind + "List"))
	if err := r.List(ctx, scans, client.InNamespace(complianceNamespace)); err != nil {
		return
	}
	suites := ownedSuites(cb)
	profileNames := selectedProfileNames(cb)
	var latest time.Time
	for _, s := range scans.Items {
		// ComplianceScan is labeled with the suite (binding) name when present;
		// fall back to profile-name matching when the label is absent.
		if suite := s.GetLabels()[suiteLabel]; suite != "" {
			if !suites[suite] {
				continue
			}
		} else if !matchesAnyProfile(s.GetName(), profileNames) {
			continue
		}
		ts, _, _ := unstructured.NestedString(s.Object, "status", "endTimestamp")
		if t, err := time.Parse(time.RFC3339, ts); err == nil && t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return
	}
	last := metav1.NewTime(latest)
	if cb.Status.LastScanTime != nil && cb.Status.LastScanTime.Equal(&last) {
		return // same scan run already recorded
	}
	cb.Status.LastScanTime = &last
	cb.Status.History = append(cb.Status.History, baselinev1alpha1.ScoreSnapshot{Time: last, Score: score})
	if len(cb.Status.History) > 30 {
		cb.Status.History = cb.Status.History[len(cb.Status.History)-30:]
	}
}

// ensureConsolePlugin deploys the plugin web server and registers the
// ConsolePlugin with the console operator. When disabled, tears down prior deploy.
func (r *ClusterBaselineReconciler) ensureConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.Console.Enabled != nil && !*cb.Spec.Console.Enabled {
		return r.removeConsolePlugin(ctx, cb)
	}
	image := os.Getenv("RELATED_IMAGE_CONSOLE_PLUGIN")
	if image == "" {
		meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
			Type: "ConsolePluginReady", Status: metav1.ConditionFalse, Reason: "ImageMissing",
			Message: "RELATED_IMAGE_CONSOLE_PLUGIN not set",
		})
		return fmt.Errorf("RELATED_IMAGE_CONSOLE_PLUGIN not set")
	}

	// Namespace must exist for OLM AllNamespaces installs (operator often runs
	// in openshift-operators; plugin lives in openshift-baseline-security).
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: pluginNS}}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	// The console only talks HTTPS to plugin backends: nginx terminates TLS
	// with the service-serving certificate on 9443.
	const nginxConf = `error_log /dev/stdout info;
events {}
http {
  access_log /dev/stdout;
  include /etc/nginx/mime.types;
  default_type application/octet-stream;
  keepalive_timeout 65;
  server {
    listen 9443 ssl;
    listen [::]:9443 ssl;
    ssl_certificate /var/serving-cert/tls.crt;
    ssl_certificate_key /var/serving-cert/tls.key;
    root /usr/share/nginx/html;
  }
}
`
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: pluginName + "-nginx", Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{"nginx.conf": nginxConf}
		return controllerutil.SetControllerReference(cb, cm, r.Scheme)
	}); err != nil {
		return err
	}

	labels := map[string]string{"app": pluginName}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec = appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(2)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Name:    pluginName,
						Image:   image,
						Ports:   []corev1.ContainerPort{{Name: "https", ContainerPort: 9443}},
						Command: []string{"nginx", "-c", "/etc/nginx-plugin/nginx.conf", "-g", "daemon off;"},
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
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
						// HTTPS only; readiness after serving-cert is mounted.
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       10,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
							},
							InitialDelaySeconds: 15,
							PeriodSeconds:       20,
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "serving-cert", MountPath: "/var/serving-cert", ReadOnly: true},
							{Name: "nginx-conf", MountPath: "/etc/nginx-plugin", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "serving-cert", VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: pluginName + "-cert"},
						}},
						{Name: "nginx-conf", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: pluginName + "-nginx"},
							},
						}},
					},
				},
			},
		}
		return controllerutil.SetControllerReference(cb, dep, r.Scheme)
	}); err != nil {
		return err
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = pluginName + "-cert"
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{Name: "https", Port: 9443, TargetPort: intstr.FromInt32(9443)}}
		return controllerutil.SetControllerReference(cb, svc, r.Scheme)
	}); err != nil {
		return err
	}

	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(consolePluginGVK)
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

	// Register with the console operator config; idempotent append.
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return err
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	found := false
	for _, p := range plugins {
		if p == pluginName {
			found = true
			break
		}
	}
	if !found {
		_ = unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins")
		if err := r.Update(ctx, console); err != nil {
			return err
		}
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ConsolePluginReady", Status: metav1.ConditionTrue, Reason: "Deployed",
		Message: "plugin Deployment, Service, and ConsolePlugin registered",
	})
	return nil
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&baselinev1alpha1.ClusterBaseline{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Named("clusterbaseline").
		Complete(r)
}
