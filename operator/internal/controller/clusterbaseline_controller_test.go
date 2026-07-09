package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := baselinev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(checkResultGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(list.GroupVersionKind(), list)
	scanList := &unstructured.UnstructuredList{}
	scanList.SetGroupVersionKind(scanGVK.GroupVersion().WithKind(scanGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(scanGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(scanList.GroupVersionKind(), scanList)
	csvList := &unstructured.UnstructuredList{}
	csvList.SetGroupVersionKind(csvGVK.GroupVersion().WithKind(csvGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(csvGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(csvList.GroupVersionKind(), csvList)
	scheme.AddKnownTypeWithName(subscriptionGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(operatorGroupGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(consolePluginGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(consoleGVK, &unstructured.Unstructured{})
	return scheme
}

func checkResult(name, suite, status string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(checkResultGVK)
	u.SetName(name)
	u.SetNamespace(complianceNamespace)
	if suite != "" {
		u.SetLabels(map[string]string{suiteLabel: suite})
	}
	u.Object["status"] = status
	return u
}

func TestAggregateStatus(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("a", "baseline-cis", "PASS"),
			checkResult("b", "baseline-cis", "PASS"),
			checkResult("c", "baseline-cis", "FAIL"),
			checkResult("d", "baseline-cis", "MANUAL"),
			checkResult("err", "baseline-cis", "ERROR"),
			checkResult("na", "baseline-cis", "NOT-APPLICABLE"),
			checkResult("e", "other-suite", "FAIL"),
			checkResult("f", "baseline-stig", "FAIL"),
			checkResult("g", "", "FAIL"),
		).Build(),
		Scheme: scheme,
	}

	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if cb.Status.Score == nil || *cb.Status.Score != 66 {
		t.Fatalf("score = %v, want 66", cb.Status.Score)
	}
	p := cb.Status.Profiles[0]
	if p.Pass != 2 || p.Fail != 1 || p.Manual != 1 || p.Error != 1 || p.NotApplicable != 1 {
		t.Fatalf("profile counts = %+v", p)
	}
}

func TestAggregateStatusClearsStaleScore(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	prev := int32(90)
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec:   baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{Score: &prev},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("score = %v, want nil after empty results", *cb.Status.Score)
	}
}

func TestRecordHistoryRing(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC).Format(time.RFC3339)
	scan := &unstructured.Unstructured{}
	scan.SetGroupVersionKind(scanGVK)
	scan.SetName("ocp4-cis")
	scan.SetNamespace(complianceNamespace)
	scan.SetLabels(map[string]string{suiteLabel: "baseline-cis"})
	_ = unstructured.SetNestedField(scan.Object, end, "status", "endTimestamp")

	foreign := &unstructured.Unstructured{}
	foreign.SetGroupVersionKind(scanGVK)
	foreign.SetName("other")
	foreign.SetNamespace(complianceNamespace)
	foreign.SetLabels(map[string]string{suiteLabel: "someone-else"})
	_ = unstructured.SetNestedField(foreign.Object, time.Now().UTC().Format(time.RFC3339), "status", "endTimestamp")

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(scan, foreign).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	for i := 0; i < 30; i++ {
		cb.Status.History = append(cb.Status.History, baselinev1alpha1.ScoreSnapshot{
			Time:  metav1.NewTime(time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)),
			Score: int32(i),
		})
	}
	r.recordHistory(context.Background(), cb, ptr.To(int32(77)))
	if len(cb.Status.History) != 30 {
		t.Fatalf("history len = %d, want 30", len(cb.Status.History))
	}
	if cb.Status.History[29].Score != 77 {
		t.Fatalf("newest score = %d, want 77", cb.Status.History[29].Score)
	}
	if cb.Status.LastScanTime == nil {
		t.Fatal("LastScanTime not set")
	}
	// Must equal the owned scan's endTimestamp; the foreign scan is later but excluded.
	if !cb.Status.LastScanTime.Time.Equal(time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("LastScanTime = %v, foreign scan leaked into history", cb.Status.LastScanTime)
	}
	before := len(cb.Status.History)
	r.recordHistory(context.Background(), cb, ptr.To(int32(88)))
	if len(cb.Status.History) != before {
		t.Fatalf("duplicate history append: len %d", len(cb.Status.History))
	}
}

func TestRecordHistoryNoOwnedScans(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	r.recordHistory(context.Background(), cb, ptr.To(int32(50)))
	if cb.Status.LastScanTime != nil || len(cb.Status.History) != 0 {
		t.Fatalf("expected no history, got last=%v hist=%v", cb.Status.LastScanTime, cb.Status.History)
	}
}

func TestSetComplianceOperatorReady(t *testing.T) {
	scheme := testScheme(t)
	csv := &unstructured.Unstructured{}
	csv.SetGroupVersionKind(csvGVK)
	csv.SetName("compliance-operator.v1.9.1")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")

	sub := &unstructured.Unstructured{}
	sub.SetGroupVersionKind(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.9.1", "status", "installedCSV")

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(csv, sub).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.setComplianceOperatorReady(context.Background(), cb, sub); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "CSVSucceeded" {
		t.Fatalf("condition = %+v, want True/CSVSucceeded", c)
	}
	if cb.Status.ComplianceOperatorVersion != "1.9.1" {
		t.Fatalf("version = %q", cb.Status.ComplianceOperatorVersion)
	}

	_ = unstructured.SetNestedField(csv.Object, "Failed", "status", "phase")
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(csv, sub).Build()
	cb = &baselinev1alpha1.ClusterBaseline{}
	if err := r.setComplianceOperatorReady(context.Background(), cb, sub); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "CSVNotReady" {
		t.Fatalf("condition = %+v, want False/CSVNotReady", c)
	}

	// Empty installedCSV.
	empty := &unstructured.Unstructured{}
	empty.SetGroupVersionKind(subscriptionGVK)
	empty.SetName("compliance-operator")
	empty.SetNamespace(complianceNamespace)
	cb = &baselinev1alpha1.ClusterBaseline{}
	if err := r.setComplianceOperatorReady(context.Background(), cb, empty); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Reason != "Installing" {
		t.Fatalf("%+v", c)
	}

	// CSV missing.
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v9.9.9", "status", "installedCSV")
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(sub).Build()
	cb = &baselinev1alpha1.ClusterBaseline{}
	if err := r.setComplianceOperatorReady(context.Background(), cb, sub); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "Installing" {
		t.Fatalf("%+v", c)
	}
}

func TestRemoveConsolePlugin(t *testing.T) {
	scheme := testScheme(t)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(consolePluginGVK)
	cp.SetName(pluginName)
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleGVK)
	console.SetName("cluster")
	_ = unstructured.SetNestedStringSlice(console.Object, []string{pluginName, "other"}, "spec", "plugins")

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep, svc, cp, console).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.removeConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "Disabled" {
		t.Fatalf("ConsolePluginReady = %+v", c)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(consoleGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "plugins")
	if len(plugins) != 1 || plugins[0] != "other" {
		t.Fatalf("plugins = %v, want [other]", plugins)
	}
}

func TestDeregisterConsolePluginRemoves(t *testing.T) {
	scheme := testScheme(t)
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleGVK)
	console.SetName("cluster")
	_ = unstructured.SetNestedStringSlice(console.Object, []string{"other", pluginName}, "spec", "plugins")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(console).Build(),
		Scheme: scheme,
	}
	if err := r.deregisterConsolePlugin(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(consoleGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "plugins")
	if len(plugins) != 1 || plugins[0] != "other" {
		t.Fatalf("%v", plugins)
	}
}

func TestDeregisterConsolePluginMissingConsole(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	if err := r.deregisterConsolePlugin(context.Background()); err != nil {
		t.Fatal(err)
	}
}
