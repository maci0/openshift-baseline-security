package controller

import (
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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
)

// Compliance Operator and OLM resources are accessed unstructured: importing
// their Go APIs would pull both dependency trees into this module for four
// object shapes we only create/read. Revisit if the surface grows.
var (
	subscriptionGVK = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}
	scanSettingGVK  = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSetting"}
	bindingGVK      = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSettingBinding"}
	checkResultGVK  = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceCheckResult"}
)

// ClusterBaselineReconciler reconciles the ClusterBaseline singleton.
type ClusterBaselineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=scansettings;scansettingbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=compliancecheckresults;compliancesuites,verbs=get;list;watch
// +kubebuilder:rbac:groups=operators.coreos.com,resources=subscriptions;operatorgroups,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces;services,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=get;list;watch;update;patch

func (r *ClusterBaselineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, req.NamespacedName, cb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
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

	if err := r.Status().Update(ctx, cb); err != nil {
		return ctrl.Result{}, err
	}
	logger.V(1).Info("reconciled", "score", cb.Status.Score)
	return ctrl.Result{}, nil
}

// ensureComplianceOperator creates namespace + OperatorGroup + Subscription
// for the Compliance Operator unless it is already installed or installation
// is disabled.
func (r *ClusterBaselineReconciler) ensureComplianceOperator(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.InstallComplianceOperator != nil && !*cb.Spec.InstallComplianceOperator {
		return nil
	}

	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if err == nil {
		meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
			Type: "ComplianceOperatorReady", Status: metav1.ConditionTrue, Reason: "Subscribed",
		})
		return nil
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
	sub.Object["spec"] = map[string]any{
		"name":            "compliance-operator",
		"channel":         "stable",
		"source":          cb.Spec.ComplianceCatalogSource,
		"sourceNamespace": "openshift-marketplace",
	}
	if err := r.Create(ctx, sub); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ComplianceOperatorReady", Status: metav1.ConditionFalse, Reason: "Installing",
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
		ss.Object["schedule"] = cb.Spec.Schedule
		ss.Object["roles"] = []any{"worker", "master"}
		ss.Object["rawResultStorage"] = map[string]any{"size": "1Gi", "rotation": int64(3)}
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
		binding.SetName(fmt.Sprintf("baseline-%s", key))
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
	// ponytail: bindings for deselected profiles are not garbage-collected yet;
	// add owned-binding pruning when profile toggling ships in the UI.
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type: "ScanConfigured", Status: metav1.ConditionTrue, Reason: "BindingsCreated",
	})
	return nil
}

// aggregateStatus recomputes per-profile counts and the overall score from
// ComplianceCheckResults.
func (r *ClusterBaselineReconciler) aggregateStatus(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	if err := r.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	byProfile := map[baselinev1alpha1.ProfileKey]*baselinev1alpha1.ProfileStatus{}
	scanToKey := map[string]baselinev1alpha1.ProfileKey{}
	for _, key := range cb.Spec.Profiles {
		byProfile[key] = &baselinev1alpha1.ProfileStatus{Key: key, ProfileNames: key.ProfileNames()}
		for _, p := range key.ProfileNames() {
			scanToKey[p] = key
		}
	}

	var pass, fail int32
	for _, item := range list.Items {
		scan := item.GetLabels()["compliance.openshift.io/scan-name"]
		key, ok := scanToKey[scan]
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
	}
	return nil
}

// ensureConsolePlugin deploys the plugin web server and registers the
// ConsolePlugin with the console operator.
func (r *ClusterBaselineReconciler) ensureConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.Console.Enabled != nil && !*cb.Spec.Console.Enabled {
		return nil
	}
	const (
		pluginName = "baseline-security-console-plugin"
		pluginNS   = "openshift-baseline-security"
	)
	image := os.Getenv("RELATED_IMAGE_CONSOLE_PLUGIN")
	if image == "" {
		return fmt.Errorf("RELATED_IMAGE_CONSOLE_PLUGIN not set")
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
					Containers: []corev1.Container{{
						Name:  pluginName,
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
					}},
				},
			},
		}
		return controllerutil.SetControllerReference(cb, dep, r.Scheme)
	}); err != nil {
		return err
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8080)}}
		return controllerutil.SetControllerReference(cb, svc, r.Scheme)
	}); err != nil {
		return err
	}

	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"})
	cp.SetName(pluginName)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cp, func() error {
		cp.Object["spec"] = map[string]any{
			"displayName": "Baseline Security",
			"backend": map[string]any{
				"type": "Service",
				"service": map[string]any{
					"name": pluginName, "namespace": pluginNS, "port": int64(8080), "basePath": "/",
				},
			},
		}
		return nil
	}); err != nil {
		return err
	}

	// Register with the console operator config; idempotent append.
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Console"})
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return err
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	for _, p := range plugins {
		if p == pluginName {
			return nil
		}
	}
	_ = unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins")
	return r.Update(ctx, console)
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&baselinev1alpha1.ClusterBaseline{}).
		Named("clusterbaseline").
		Complete(r)
}
