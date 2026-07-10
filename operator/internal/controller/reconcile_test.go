package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
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
	cb.Spec.InstallComplianceOperator = baselinev1alpha1.InstallManual
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("subscription should not exist, err=%v", err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "NotInstalled" {
		t.Fatalf("Manual without CO must set NotInstalled, got %+v", c)
	}
}

func TestEnsureComplianceOperatorManualStillChecksExisting(t *testing.T) {
	scheme := testScheme(t)
	sub := u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.0.0", "status", "installedCSV")
	csv := u(csvGVK)
	csv.SetName("compliance-operator.v1.0.0")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(sub, csv).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	cb.Spec.InstallComplianceOperator = baselinev1alpha1.InstallManual
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Manual with installed CO must be Ready, got %+v", c)
	}
}

// TestEnsureComplianceOperatorSyncsCatalogSource: createIfMissing only writes
// the Subscription once; a later change to spec.complianceCatalogSource must
// still update spec.source (OKD / disconnected catalog moves).
func TestEnsureComplianceOperatorSyncsCatalogSource(t *testing.T) {
	scheme := testScheme(t)
	sub := u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": "redhat-operators", "sourceNamespace": "openshift-marketplace",
	}
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.0.0", "status", "installedCSV")
	csv := u(csvGVK)
	csv.SetName("compliance-operator.v1.0.0")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(sub, csv).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	cb.Spec.ComplianceCatalogSource = "community-operators"
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	got := u(subscriptionGVK)
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: complianceNamespace, Name: "compliance-operator",
	}, got); err != nil {
		t.Fatal(err)
	}
	source, _, _ := unstructured.NestedString(got.Object, "spec", "source")
	if source != "community-operators" {
		t.Fatalf("subscription source = %q, want community-operators", source)
	}
}

// Manual install must not rewrite a pre-existing Subscription's catalog source.
func TestEnsureComplianceOperatorManualDoesNotRewriteSource(t *testing.T) {
	scheme := testScheme(t)
	sub := u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": "my-private-catalog", "sourceNamespace": "openshift-marketplace",
	}
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.0.0", "status", "installedCSV")
	csv := u(csvGVK)
	csv.SetName("compliance-operator.v1.0.0")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(sub, csv).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	cb.Spec.InstallComplianceOperator = baselinev1alpha1.InstallManual
	cb.Spec.ComplianceCatalogSource = "community-operators"
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	got := u(subscriptionGVK)
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: complianceNamespace, Name: "compliance-operator",
	}, got); err != nil {
		t.Fatal(err)
	}
	source, _, _ := unstructured.NestedString(got.Object, "spec", "source")
	if source != "my-private-catalog" {
		t.Fatalf("Manual must leave source alone, got %q", source)
	}
}

func TestDesiredComplianceCatalogSource(t *testing.T) {
	if got := desiredComplianceCatalogSource(&baselinev1alpha1.ClusterBaseline{}); got != "redhat-operators" {
		t.Fatalf("default = %q", got)
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{ComplianceCatalogSource: "okd-operators"},
	}
	if got := desiredComplianceCatalogSource(cb); got != "okd-operators" {
		t.Fatalf("override = %q", got)
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
	cb.Spec.Remediation.Apply = baselinev1alpha1.RemediationApplyAutomatic
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
	// Manual (and empty) map to false for both auto flags.
	cb.Spec.Remediation.Apply = baselinev1alpha1.RemediationApplyManual
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: scanSettingName}, ss); err != nil {
		t.Fatal(err)
	}
	if auto, _, _ := unstructured.NestedBool(ss.Object, "autoApplyRemediations"); auto {
		t.Fatal("autoApplyRemediations should be false for Manual")
	}
	if auto, _, _ := unstructured.NestedBool(ss.Object, "autoUpdateRemediations"); auto {
		t.Fatal("autoUpdateRemediations should be false for Manual")
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

	cb.Spec.Schedule = "not a cron"
	// Bad cron must Degrade but still apply profile changes.
	cb.Spec.Profiles = []baselinev1alpha1.ProfileKey{"cis"}
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "InvalidSchedule" {
		t.Fatalf("ScanConfigured = %+v, want False/InvalidSchedule", c)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-cis"}, binding); err != nil {
		t.Fatal("invalid schedule must still create bindings:", err)
	}
	// Last-good schedule on ScanSetting is preserved (not overwritten with garbage).
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: scanSettingName}, ss); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(ss.Object, "schedule"); got != "0 1 * * *" {
		t.Fatalf("schedule overwritten on invalid cron: %q", got)
	}
}

// TestEnsureScanConfigInvalidCronFirstCreate covers the first-create path: with
// no pre-existing ScanSetting there is no last-good schedule, so an invalid cron
// must fall back to the operator default rather than leave CO with an empty one.
func TestEnsureScanConfigInvalidCronFirstCreate(t *testing.T) {
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
	cb.Spec.Schedule = "not a cron"
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	ss := &unstructured.Unstructured{}
	ss.SetGroupVersionKind(scanSettingGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: scanSettingName}, ss); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(ss.Object, "schedule"); got != "0 1 * * *" {
		t.Fatalf("first-create invalid cron schedule = %q, want the default fallback", got)
	}
}

func TestEnsureConsolePlugin(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(consoleCluster("other")).
			WithStatusSubresource(&appsv1.Deployment{}).
			Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")

	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "")
	// Soft-fail: missing image must not block reconcile of scans/status.
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Reason != "ImageMissing" || c.Status != metav1.ConditionFalse {
		t.Fatalf("condition = %+v", c)
	}

	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:1")
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	svc := &corev1.Service{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] != pluginName+"-cert" {
		t.Fatal("serving-cert annotation missing")
	}
	dep := &appsv1.Deployment{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		t.Fatal(err)
	}
	if img := dep.Spec.Template.Spec.Containers[0].Image; img != "example.test/plugin:1" {
		t.Fatalf("image = %q", img)
	}
	// maxUnavailable=1 keeps DeploymentAvailable True at 1/2 so a single drained
	// node does not false-Degrade the plugin (matches pluginReadyMin=1).
	ru := dep.Spec.Strategy.RollingUpdate
	if ru == nil || ru.MaxUnavailable == nil || ru.MaxUnavailable.IntValue() != 1 {
		t.Fatalf("rolling update MaxUnavailable = %v, want 1", ru)
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatal("pod SecurityContext.RunAsNonRoot required")
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("pod SeccompProfile RuntimeDefault required")
	}
	vol := dep.Spec.Template.Spec.Volumes[0].Secret
	if vol == nil || vol.Optional == nil || !*vol.Optional {
		t.Fatal("serving-cert volume must be optional until service-ca mints the Secret")
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
	// Pods not ready yet.
	c = meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "WaitingForPods" {
		t.Fatalf("condition = %+v, want WaitingForPods", c)
	}
	// Partial HA (1 of 2 ready) is enough to serve; require Available=True.
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Status.ReadyReplicas = pluginReadyMin
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:   appsv1.DeploymentAvailable,
		Status: corev1.ConditionTrue,
	}}
	if err := r.Status().Update(context.Background(), dep); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "Deployed" {
		t.Fatalf("partial ready with Available must be Deployed, got %+v", c)
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

func TestEnsureConsolePluginDisabled(t *testing.T) {
	scheme := testScheme(t)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(dep, consoleCluster(pluginName, "other")).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	cb.Spec.Console.ManagementState = baselinev1alpha1.Removed
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, &appsv1.Deployment{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("deployment should be gone, err=%v", err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Reason != "Disabled" {
		t.Fatalf("%+v", c)
	}
}

func TestReconcileNotFound(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %+v", res)
	}
}

func TestEnsureComplianceOperatorAlreadyInstalled(t *testing.T) {
	scheme := testScheme(t)
	csv := &unstructured.Unstructured{}
	csv.SetGroupVersionKind(csvGVK)
	csv.SetName("compliance-operator.v1.0.0")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.0.0", "status", "installedCSV")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(csv, sub).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("%+v", c)
	}
}

func TestEnsureComplianceOperatorAdoptsExistingCSV(t *testing.T) {
	scheme := testScheme(t)
	csv := &unstructured.Unstructured{}
	csv.SetGroupVersionKind(csvGVK)
	csv.SetName("compliance-operator.v2.3.4")
	csv.SetNamespace("custom-compliance")
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(csv).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "CSVSucceeded" {
		t.Fatalf("ComplianceOperatorReady = %+v, want True/CSVSucceeded", c)
	}
	if cb.Status.ComplianceOperatorVersion != "2.3.4" {
		t.Fatalf("version = %q, want 2.3.4", cb.Status.ComplianceOperatorVersion)
	}
	sub := u(subscriptionGVK)
	err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("adopting an existing CSV should not create a Subscription, err=%v", err)
	}
}

func TestFindComplianceOperatorCSVChoosesNewestSucceeded(t *testing.T) {
	scheme := testScheme(t)
	oldSucceeded := u(csvGVK)
	oldSucceeded.SetName("compliance-operator.v1.9.0")
	oldSucceeded.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(oldSucceeded.Object, "Succeeded", "status", "phase")
	newSucceeded := u(csvGVK)
	newSucceeded.SetName("compliance-operator.v1.10.0")
	newSucceeded.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(newSucceeded.Object, "Succeeded", "status", "phase")
	newerInstalling := u(csvGVK)
	newerInstalling.SetName("compliance-operator.v1.11.0")
	newerInstalling.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(newerInstalling.Object, "Installing", "status", "phase")
	// Stale higher Succeeded in another namespace must not win over live NS.
	staleOtherNS := u(csvGVK)
	staleOtherNS.SetName("compliance-operator.v1.99.0")
	staleOtherNS.SetNamespace("openshift-operators")
	_ = unstructured.SetNestedField(staleOtherNS.Object, "Succeeded", "status", "phase")
	foreign := u(csvGVK)
	foreign.SetName("other-operator.v9.9.9")
	foreign.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(foreign.Object, "Succeeded", "status", "phase")

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(oldSucceeded, newerInstalling, foreign, newSucceeded, staleOtherNS).
			Build(),
		Scheme: scheme,
	}
	got, err := r.findComplianceOperatorCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.GetName() != "compliance-operator.v1.10.0" || got.GetNamespace() != complianceNamespace {
		t.Fatalf("selected CSV = %v/%v, want %s/compliance-operator.v1.10.0",
			got.GetNamespace(), got.GetName(), complianceNamespace)
	}
}

func TestFindComplianceOperatorCSVFallsBackToNewestNonSucceeded(t *testing.T) {
	scheme := testScheme(t)
	older := u(csvGVK)
	older.SetName("compliance-operator.v1.9.0")
	older.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(older.Object, "Installing", "status", "phase")
	newer := u(csvGVK)
	newer.SetName("compliance-operator.v1.10.0")
	newer.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(newer.Object, "Pending", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(older, newer).Build(),
		Scheme: scheme,
	}
	got, err := r.findComplianceOperatorCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.GetName() != "compliance-operator.v1.10.0" {
		t.Fatalf("selected CSV = %v, want compliance-operator.v1.10.0", got)
	}
}

func TestFindComplianceOperatorCSVFallsBackOutsideComplianceNS(t *testing.T) {
	scheme := testScheme(t)
	// Manual install only in openshift-operators (no CSV in openshift-compliance).
	csv := u(csvGVK)
	csv.SetName("compliance-operator.v1.10.0")
	csv.SetNamespace("openshift-operators")
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(csv).Build(),
		Scheme: scheme,
	}
	got, err := r.findComplianceOperatorCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.GetName() != "compliance-operator.v1.10.0" {
		t.Fatalf("selected CSV = %v, want openshift-operators fallback", got)
	}
}

func TestFindComplianceOperatorCSVRemoteSucceededBeatsLocalFailed(t *testing.T) {
	scheme := testScheme(t)
	// Local remnant Failed must not hide a healthy Succeeded CSV elsewhere
	// (previous "prefer local NS for any phase" ranking caused this).
	localFailed := u(csvGVK)
	localFailed.SetName("compliance-operator.v1.9.0")
	localFailed.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(localFailed.Object, "Failed", "status", "phase")
	remoteOK := u(csvGVK)
	remoteOK.SetName("compliance-operator.v1.10.0")
	remoteOK.SetNamespace("openshift-operators")
	_ = unstructured.SetNestedField(remoteOK.Object, "Succeeded", "status", "phase")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(localFailed, remoteOK).Build(),
		Scheme: scheme,
	}
	got, err := r.findComplianceOperatorCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.GetName() != "compliance-operator.v1.10.0" || got.GetNamespace() != "openshift-operators" {
		t.Fatalf("selected CSV = %v/%v, want openshift-operators/compliance-operator.v1.10.0",
			got.GetNamespace(), got.GetName())
	}
}

func TestCompareComplianceCSVVersion(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want int
	}{
		{"compliance-operator.v1.10.0", "compliance-operator.v1.9.9", 1},
		{"compliance-operator.v1.10.0", "compliance-operator.v1.10.0-rc.1", 1},
		{"compliance-operator.v1.10.0-rc.2", "compliance-operator.v1.10.0-rc.1", 1},
		{"compliance-operator.v1.10.0-alpha", "compliance-operator.v1.10.0-beta", -1},
		{"compliance-operator.v1.10.0-alpha.10", "compliance-operator.v1.10.0-alpha.2", 1},
		{"compliance-operator.v1.10.0+build.2", "compliance-operator.v1.10.0+build.1", 1},
		{"not-compliance.v9.9.9", "compliance-operator.v1.0.0", -1},
	}
	for _, tc := range cases {
		got := compareComplianceCSVVersion(tc.a, tc.b)
		switch {
		case tc.want > 0 && got <= 0:
			t.Fatalf("compareComplianceCSVVersion(%q, %q) = %d, want >0", tc.a, tc.b, got)
		case tc.want < 0 && got >= 0:
			t.Fatalf("compareComplianceCSVVersion(%q, %q) = %d, want <0", tc.a, tc.b, got)
		case tc.want == 0 && got != 0:
			t.Fatalf("compareComplianceCSVVersion(%q, %q) = %d, want 0", tc.a, tc.b, got)
		}
	}
}

func TestFindComplianceOperatorCSVPrefersReleaseOverPrerelease(t *testing.T) {
	scheme := testScheme(t)
	release := u(csvGVK)
	release.SetName("compliance-operator.v1.10.0")
	release.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(release.Object, "Succeeded", "status", "phase")
	rc := u(csvGVK)
	rc.SetName("compliance-operator.v1.10.0-rc.1")
	rc.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(rc.Object, "Succeeded", "status", "phase")

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(rc, release).Build(),
		Scheme: scheme,
	}
	got, err := r.findComplianceOperatorCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.GetName() != "compliance-operator.v1.10.0" {
		t.Fatalf("selected CSV = %v, want compliance-operator.v1.10.0", got)
	}
}

// Full happy path: finalizer present, CO subscription installed with a
// Succeeded CSV, console present, plugin image set. Reconcile must persist
// status (score, conditions incl. rollups) and schedule a requeue.
func TestReconcileHappyPath(t *testing.T) {
	scheme := testScheme(t)
	scheme.AddKnownTypeWithName(scanSettingGVK, &unstructured.Unstructured{})
	bindingList := uList(bindingGVK)
	scheme.AddKnownTypeWithName(bindingGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(bindingList.GroupVersionKind(), bindingList)

	cb := newCB("cis")
	cb.Finalizers = []string{finalizerName}

	sub := u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.9.1", "status", "installedCSV")
	csv := u(csvGVK)
	csv.SetName("compliance-operator.v1.9.1")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, sub, csv, consoleCluster("other"),
				checkResult("a", "baseline-cis", "PASS"),
				checkResult("b", "baseline-cis", "FAIL")).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:1")

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected periodic requeue, got %+v", res)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Score == nil || *got.Status.Score != 50 {
		t.Fatalf("persisted score = %v, want 50", got.Status.Score)
	}
	for _, typ := range []string{"Available", "Progressing", "Degraded", "ComplianceOperatorReady", "ScanConfigured", "ScanStorageReady"} {
		if meta.FindStatusCondition(got.Status.Conditions, typ) == nil {
			t.Fatalf("condition %s not persisted", typ)
		}
	}
	if c := meta.FindStatusCondition(got.Status.Conditions, "Available"); c.Status != metav1.ConditionTrue {
		t.Fatalf("Available = %+v", c)
	}
}

// Compliance CRDs absent (Compliance Operator not yet installed): the
// NoKindMatch tolerance paths must let Reconcile finish and persist status.
// The fake client fabricates unknown kinds, so interceptors return the
// NoKindMatchError a real API server produces for missing CRDs.
func TestReconcileWithoutComplianceCRDs(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	cb.Finalizers = []string{finalizerName}
	noMatch := func(gvk schema.GroupVersionKind) error {
		if gvk.Group == "compliance.openshift.io" {
			return &meta.NoKindMatchError{GroupKind: gvk.GroupKind()}
		}
		return nil
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, consoleCluster()).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := noMatch(obj.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if err := noMatch(list.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.List(ctx, list, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if err := noMatch(obj.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.Create(ctx, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:1")

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(got.Status.Conditions, "ScanConfigured")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "CRDsMissing" {
		t.Fatalf("ScanConfigured = %+v, want False/CRDsMissing", c)
	}
	if p := meta.FindStatusCondition(got.Status.Conditions, "Progressing"); p == nil || p.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing = %+v, want True while CRDs missing", p)
	}
}

// TestReconcileManualNoComplianceCRDs is the genuine steady state a Console- and
// CO-less cluster settles into with InstallComplianceOperator=Manual: nothing is
// installing, so Progressing must be False (no 15s poll storm), Available False,
// and Degraded False (a not-yet-installed dependency is not a failure).
func TestReconcileManualNoComplianceCRDs(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	cb.Spec.InstallComplianceOperator = baselinev1alpha1.InstallManual
	cb.Finalizers = []string{finalizerName}
	noMatch := func(gvk schema.GroupVersionKind) error {
		// Both the compliance CRDs and the Console capability are absent.
		if gvk.Group == "compliance.openshift.io" || gvk.Group == "console.openshift.io" ||
			gvk.Group == "operator.openshift.io" {
			return &meta.NoKindMatchError{GroupKind: gvk.GroupKind()}
		}
		return nil
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := noMatch(obj.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if err := noMatch(list.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:1")

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		typ    string
		status metav1.ConditionStatus
	}{
		{"Progressing", metav1.ConditionFalse},
		{"Available", metav1.ConditionFalse},
		{"Degraded", metav1.ConditionFalse},
	} {
		c := meta.FindStatusCondition(got.Status.Conditions, want.typ)
		if c == nil || c.Status != want.status {
			t.Fatalf("%s = %+v, want %s", want.typ, c, want.status)
		}
	}
	// Steady state must poll at the slow cadence, not 15s.
	if res.RequeueAfter < time.Minute {
		t.Fatalf("RequeueAfter = %v, want the 1m steady cadence (no poll storm)", res.RequeueAfter)
	}
}
