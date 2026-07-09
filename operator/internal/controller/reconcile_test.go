package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func newCB(profiles ...baselinev1alpha1.ProfileKey) *baselinev1alpha1.ClusterBaseline {
	return &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", UID: "test-uid"},
		Spec:       baselinev1alpha1.ClusterBaselineSpec{Profiles: profiles, Schedule: "0 1 * * *"},
	}
}

func consoleCluster(plugins ...string) *unstructured.Unstructured {
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleGVK)
	console.SetName("cluster")
	_ = unstructured.SetNestedStringSlice(console.Object, plugins, "spec", "plugins")
	return console
}

func TestReconcileAddsFinalizerAndRequeues(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Finalizers) != 1 || got.Finalizers[0] != finalizerName {
		t.Fatalf("finalizers = %v", got.Finalizers)
	}
}

func TestReconcileDeletionDeregistersAndRemovesFinalizer(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	cb.Finalizers = []string{finalizerName}
	now := metav1.Now()
	cb.DeletionTimestamp = &now
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, consoleCluster(pluginName, "other")).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	console := consoleCluster()
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, console); err != nil {
		t.Fatal(err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if len(plugins) != 1 || plugins[0] != "other" {
		t.Fatalf("console plugins = %v, want [other]", plugins)
	}
	// Finalizer removed: fake client garbage-collects the object.
	err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, &baselinev1alpha1.ClusterBaseline{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("ClusterBaseline still present: err=%v", err)
	}
}

func TestEnsureComplianceOperatorCreatesSubscription(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	cb.Spec.ComplianceCatalogSource = "my-catalog"
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub); err != nil {
		t.Fatal(err)
	}
	source, _, _ := unstructured.NestedString(sub.Object, "spec", "source")
	if source != "my-catalog" {
		t.Fatalf("subscription source = %q", source)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Name: complianceNamespace}, &corev1.Namespace{}); err != nil {
		t.Fatal("namespace not created:", err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "Installing" {
		t.Fatalf("condition = %+v", c)
	}
}

func TestEnsureComplianceOperatorOptOut(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	no := false
	cb.Spec.InstallComplianceOperator = &no
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("subscription should not exist, err=%v", err)
	}
}

func TestEnsureScanConfigCreatesAndPrunes(t *testing.T) {
	scheme := testScheme(t)
	scheme.AddKnownTypeWithName(scanSettingGVK, &unstructured.Unstructured{})
	bindingList := &unstructured.UnstructuredList{}
	bindingList.SetGroupVersionKind(bindingGVK.GroupVersion().WithKind(bindingGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(bindingGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(bindingList.GroupVersionKind(), bindingList)

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	yes := true
	cb.Spec.Remediation.AutoApply = &yes
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	ss := &unstructured.Unstructured{}
	ss.SetGroupVersionKind(scanSettingGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: scanSettingName}, ss); err != nil {
		t.Fatal(err)
	}
	if auto, _, _ := unstructured.NestedBool(ss.Object, "autoApplyRemediations"); !auto {
		t.Fatal("autoApplyRemediations not set")
	}
	if schedule, _, _ := unstructured.NestedString(ss.Object, "schedule"); schedule != "0 1 * * *" {
		t.Fatalf("schedule = %q", schedule)
	}

	binding := &unstructured.Unstructured{}
	binding.SetGroupVersionKind(bindingGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-cis"}, binding); err != nil {
		t.Fatal(err)
	}

	// Switch profiles; the old owned binding is pruned, foreign ones survive.
	foreign := &unstructured.Unstructured{}
	foreign.SetGroupVersionKind(bindingGVK)
	foreign.SetName("someone-elses")
	foreign.SetNamespace(complianceNamespace)
	if err := r.Create(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	cb.Spec.Profiles = []baselinev1alpha1.ProfileKey{"e8"}
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-cis"}, binding)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("baseline-cis should be pruned, err=%v", err)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-e8"}, binding); err != nil {
		t.Fatal("baseline-e8 missing:", err)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "someone-elses"}, binding); err != nil {
		t.Fatal("foreign binding must survive pruning:", err)
	}
}

func TestEnsureConsolePlugin(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(consoleCluster("other")).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")

	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "")
	if err := r.ensureConsolePlugin(context.Background(), cb); err == nil {
		t.Fatal("want error when RELATED_IMAGE_CONSOLE_PLUGIN unset")
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Reason != "ImageMissing" {
		t.Fatalf("condition = %+v", c)
	}

	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:1")
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		t.Fatal(err)
	}
	if img := dep.Spec.Template.Spec.Containers[0].Image; img != "example.test/plugin:1" {
		t.Fatalf("image = %q", img)
	}
	svc := &corev1.Service{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] != pluginName+"-cert" {
		t.Fatal("serving-cert annotation missing")
	}
	console := consoleCluster()
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, console); err != nil {
		t.Fatal(err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if len(plugins) != 2 || plugins[1] != pluginName {
		t.Fatalf("console plugins = %v", plugins)
	}
	// Idempotent: second run must not duplicate the registration.
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, console)
	plugins, _, _ = unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if len(plugins) != 2 {
		t.Fatalf("duplicate registration: %v", plugins)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v", c)
	}
}

func TestDeregisterConsolePluginNoop(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(consoleCluster("other")).Build(),
		Scheme: scheme,
	}
	if err := r.deregisterConsolePlugin(context.Background()); err != nil {
		t.Fatal(err)
	}
	console := consoleCluster()
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, console); err != nil {
		t.Fatal(err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if len(plugins) != 1 || plugins[0] != "other" {
		t.Fatalf("plugins = %v, want [other] untouched", plugins)
	}
}
