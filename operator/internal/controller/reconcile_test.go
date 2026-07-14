package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
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
	resetMetrics(t)
	// Stale gauges must clear when the finalizer drops (NotFound may never requeue).
	stale := &baselinev1alpha1.ClusterBaseline{}
	stale.Status.Score = ptr.To(int32(55))
	stale.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Fail: 2}},
	}
	publishMetrics(stale)

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
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score after delete = %v, want -1", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks after delete: %d series remain", got)
	}
}

// TestReconcileDeletionResumesBatchPools: deleting the CR mid-batch must unpause
// MachineConfigPools recorded in status.remediationBatch before the finalizer
// drops (otherwise pools stay paused with no operator left to resume them).
func TestReconcileDeletionResumesBatchPools(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	cb.Finalizers = []string{finalizerName}
	now := metav1.Now()
	cb.DeletionTimestamp = &now
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase: "Applying", Pools: []string{"worker"}, Remediations: []string{"rem1"},
		StartedAt: now,
	}
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, pool, consoleCluster(pluginName)).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	got := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
		t.Fatal("worker pool must be unpaused on ClusterBaseline deletion")
	}
}

// TestReconcileOwnedBatchBeforeCOFailure: applyRemediationBatch runs before
// ensureComplianceOperator so a hard CO API error cannot leave a new batch
// unstarted (or an in-flight batch unserviced) while MCPs are/should be managed.
func TestReconcileOwnedBatchBeforeCOFailure(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	cb := newCB("cis")
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	// RELATED_IMAGE so plugin ensure does not soft-fail first for other reasons.
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:test")
	boom := apierrors.NewServiceUnavailable("subscription apiserver blip")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok && u.GroupVersionKind() == subscriptionGVK {
						return boom
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	err := r.reconcileOwned(context.Background(), cb)
	if err == nil {
		t.Fatal("expected CO ensure to fail after batch")
	}
	// Batch must have run first: pool paused and status.remediationBatch set.
	if cb.Status.RemediationBatch == nil || cb.Status.RemediationBatch.Phase != "Applying" {
		t.Fatalf("batch must start before CO failure: %+v (err=%v)", cb.Status.RemediationBatch, err)
	}
	gotPool := machineConfigPool("worker")
	_ = r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool)
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); !paused {
		t.Fatal("worker pool must be paused even when CO ensure fails later")
	}
}

// TestApplyRemediationBatchGuardrails: the batch-apply annotation is an
// untrusted trust boundary (a client with ClusterBaseline patch permission
// controls it). Two guardrails must reject before any MCP is paused or any
// ComplianceRemediation is mutated: more than batchMaxRemediations names, and
// any name that is not a DNS-1123 subdomain. Both leave status.remediationBatch
// unset and clear the one-shot annotation so a hostile value cannot
// sticky-Degrade every reconcile (mirrors too-many-pools).
func TestApplyRemediationBatchGuardrails(t *testing.T) {
	scheme := testScheme(t)
	names := make([]string, batchMaxRemediations+1)
	for i := range names {
		names[i] = fmt.Sprintf("rem-%d", i)
	}
	cases := []struct {
		name string
		anno string
	}{
		{"over the maximum", strings.Join(names, ",")},
		{"non-DNS-1123 uppercase", "Bad_Name"},
		{"non-DNS-1123 double dot", "rem..1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cb := newCB("cis")
			cb.SetAnnotations(map[string]string{batchApplyAnnotation: c.anno})
			r := &ClusterBaselineReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
					WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
				Scheme: scheme,
			}
			if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
				t.Fatalf("guardrail must skip without error (sticky Degrade), got %v", err)
			}
			if cb.Status.RemediationBatch != nil {
				t.Fatalf("no batch must open on rejection, got %+v", cb.Status.RemediationBatch)
			}
			got := &baselinev1alpha1.ClusterBaseline{}
			if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
				t.Fatal(err)
			}
			if got.Annotations[batchApplyAnnotation] != "" {
				t.Fatal("rejected batch-apply annotation not cleared (would sticky-Degrade)")
			}
		})
	}
}

// TestResumeBatchPoolsOnDeleteFromAnnotation: when status.remediationBatch is
// missing but the batch-apply annotation remains, rediscover pools from remediations.
func TestResumeBatchPoolsOnDeleteFromAnnotation(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: batchPauseOwner(cb)})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).Build(),
		Scheme: scheme,
	}
	if err := r.resumeBatchPoolsOnDelete(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	got := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
		t.Fatal("pool must resume from annotation rediscovery on delete")
	}
}

// TestResumeBatchPoolsOnDeleteSkipsInvalidRemediationNames: the delete-time pool
// recovery path must not wedge the CR's finalizer on a malformed batch-apply
// annotation. A real apiserver returns 400 (not 404) for a Get with a non-DNS-1123
// name, so the recovery path skips such names before the Get and still resumes
// from the valid remediation.
func TestResumeBatchPoolsOnDeleteSkipsInvalidRemediationNames(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis")
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "Bad_Name,rem1"})
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: batchPauseOwner(cb)})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					// The guard must skip "Bad_Name" before ever calling Get; if it
					// does not, this 400 propagates and wedges the finalizer.
					if key.Name == "Bad_Name" {
						return apierrors.NewBadRequest("Invalid value: \"Bad_Name\"")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.resumeBatchPoolsOnDelete(context.Background(), cb); err != nil {
		t.Fatalf("invalid remediation name must be skipped, not wedge deletion: %v", err)
	}
	got := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
		t.Fatal("pool must still resume from the valid remediation")
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

// A differently-named OperatorGroup already owning openshift-compliance (user
// pre-staged the namespace) must be deferred to, not duplicated: a second OG
// makes OLM reject the namespace (MultipleOperatorGroupsFound) and wedges the
// install. We leave the user OG untouched (write RBAC is scoped to our own name).
func TestEnsureComplianceOperatorDefersToExistingOperatorGroup(t *testing.T) {
	scheme := testScheme(t)
	foreign := u(operatorGroupGVK)
	foreign.SetName("user-og")
	foreign.SetNamespace(complianceNamespace)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(foreign).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis")
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	ogs := uList(operatorGroupGVK)
	if err := r.List(context.Background(), ogs, client.InNamespace(complianceNamespace)); err != nil {
		t.Fatal(err)
	}
	if len(ogs.Items) != 1 {
		t.Fatalf("OperatorGroup count = %d, want 1 (no duplicate created)", len(ogs.Items))
	}
	if n := ogs.Items[0].GetName(); n != "user-og" {
		t.Fatalf("OG name = %q, want the pre-existing user-og left in place", n)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"},
		u(operatorGroupGVK)); !apierrors.IsNotFound(err) {
		t.Fatalf("no compliance-operator OG should be created alongside user-og, err=%v", err)
	}
	// Install still proceeds through the user's OG: the Subscription is created.
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"},
		u(subscriptionGVK)); err != nil {
		t.Fatalf("Subscription should be created through the existing OG: %v", err)
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

// TestEnsureComplianceOperatorGroupScopesTargetNamespaces: createIfMissing left
// a pre-existing empty OperatorGroup untouched (cluster-wide CO install). The
// ensure path must rewrite targetNamespaces to openshift-compliance.
func TestEnsureComplianceOperatorGroupScopesTargetNamespaces(t *testing.T) {
	scheme := testScheme(t)
	og := u(operatorGroupGVK)
	og.SetName("compliance-operator")
	og.SetNamespace(complianceNamespace)
	// Empty / missing targetNamespaces is the hazard we fix.
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(og).Build(),
		Scheme: scheme,
	}
	if err := r.ensureComplianceOperatorGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := u(operatorGroupGVK)
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: complianceNamespace, Name: "compliance-operator",
	}, got); err != nil {
		t.Fatal(err)
	}
	ns, _, err := unstructured.NestedStringSlice(got.Object, "spec", "targetNamespaces")
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns[0] != complianceNamespace {
		t.Fatalf("targetNamespaces = %v, want [%s]", ns, complianceNamespace)
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

// A transient error on the redhat-operators CatalogSource check must NOT flap an
// OKD Subscription off community-operators: detection is then unconfident, so the
// sync path leaves the working source untouched (a confident reconcile corrects
// any real drift).
func TestEnsureComplianceOperatorTransientCatalogErrorDoesNotFlapSource(t *testing.T) {
	scheme := testScheme(t)
	sub := u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": "community-operators", "sourceNamespace": "openshift-marketplace",
	}
	_ = unstructured.SetNestedField(sub.Object, "compliance-operator.v1.0.0", "status", "installedCSV")
	csv := u(csvGVK)
	csv.SetName("compliance-operator.v1.0.0")
	csv.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(csv.Object, "Succeeded", "status", "phase")
	community := u(catalogSourceGVK)
	community.SetName("community-operators")
	community.SetNamespace("openshift-marketplace")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(sub, csv, community).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					// Only the redhat-operators CatalogSource check blips; the real
					// community-operators source and everything else read normally.
					if key.Name == "redhat-operators" {
						return apierrors.NewServiceUnavailable("apiserver blip")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	cb := newCB("cis") // no explicit spec.complianceCatalogSource: auto-detect
	if err := r.ensureComplianceOperator(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	got := u(subscriptionGVK)
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: complianceNamespace, Name: "compliance-operator",
	}, got); err != nil {
		t.Fatal(err)
	}
	if source, _, _ := unstructured.NestedString(got.Object, "spec", "source"); source != "community-operators" {
		t.Fatalf("subscription source flapped to %q on a transient redhat-operators error, want community-operators preserved", source)
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

// TestEnsureScanConfigScanningDisabled: clearing all profiles (and tailored
// profiles) prunes the owned bindings and reports ScanConfigured=True with
// reason ScanningDisabled, not an error or Degraded.
func TestEnsureScanConfigScanningDisabled(t *testing.T) {
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
	// Create a cis binding, then clear every profile.
	cb := newCB("cis")
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	cb.Spec.Profiles = nil
	cb.Spec.TailoredProfiles = nil
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ScanningDisabled" {
		t.Fatalf("ScanConfigured = %+v, want True/ScanningDisabled", c)
	}
	binding := &unstructured.Unstructured{}
	binding.SetGroupVersionKind(bindingGVK)
	err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-cis"}, binding)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("baseline-cis binding should be pruned when scanning is disabled, err=%v", err)
	}

	// A leftover invalid schedule must not Degrade a disabled baseline: the cron
	// never fires when nothing is scheduled. ScanningDisabled must win over
	// InvalidSchedule.
	cb.Spec.Schedule = "not-a-cron"
	if err := r.ensureScanConfig(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ScanningDisabled" {
		t.Fatalf("disabled + invalid schedule: ScanConfigured = %+v, want True/ScanningDisabled", c)
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
	lbClass := "example.test/external"
	allocateNodePorts := true
	hostileService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pluginName,
			Namespace: pluginNS,
			// Foreign annotations must be wiped (only serving-cert remains).
			Annotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname":         "evil.example",
				"service.beta.kubernetes.io/aws-load-balancer-type": "external",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:                          corev1.ServiceTypeLoadBalancer,
			ExternalIPs:                   []string{"203.0.113.10"},
			LoadBalancerIP:                "203.0.113.11",
			LoadBalancerSourceRanges:      []string{"0.0.0.0/0"},
			LoadBalancerClass:             &lbClass,
			AllocateLoadBalancerNodePorts: &allocateNodePorts,
			ExternalTrafficPolicy:         corev1.ServiceExternalTrafficPolicyLocal,
			HealthCheckNodePort:           32000,
			PublishNotReadyAddresses:      true,
		},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(consoleCluster("other"), hostileService).
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

	// Whitespace-only is the same as unset (avoids a Deployment with image " ").
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "  \t ")
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Reason != "ImageMissing" {
		t.Fatalf("whitespace image condition = %+v", c)
	}

	// Garbage refs must not create a Deployment (ImageInvalid, soft-fail).
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "not a valid image!!!")
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c = meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Reason != "ImageInvalid" || c.Status != metav1.ConditionFalse {
		t.Fatalf("invalid image condition = %+v", c)
	}
	depBad := &appsv1.Deployment{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, depBad); err == nil {
		t.Fatal("invalid RELATED_IMAGE must not create plugin Deployment")
	}

	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "  example.test/plugin:1\n")
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	depPad := &appsv1.Deployment{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, depPad); err != nil {
		t.Fatal(err)
	}
	if img := depPad.Spec.Template.Spec.Containers[0].Image; img != "example.test/plugin:1" {
		t.Fatalf("trimmed image = %q", img)
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
	if len(svc.Annotations) != 1 {
		t.Fatalf("plugin Service annotations = %v, want only serving-cert", svc.Annotations)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("plugin Service type = %q, want ClusterIP", svc.Spec.Type)
	}
	if len(svc.Spec.ExternalIPs) != 0 || svc.Spec.LoadBalancerIP != "" ||
		len(svc.Spec.LoadBalancerSourceRanges) != 0 || svc.Spec.LoadBalancerClass != nil ||
		svc.Spec.AllocateLoadBalancerNodePorts != nil || svc.Spec.ExternalTrafficPolicy != "" ||
		svc.Spec.HealthCheckNodePort != 0 || svc.Spec.PublishNotReadyAddresses {
		t.Fatalf("plugin Service retained external exposure fields: %+v", svc.Spec)
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
	pdb := &policyv1.PodDisruptionBudget{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, pdb); err != nil {
		t.Fatalf("plugin PDB: %v", err)
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != int(pluginReadyMin) {
		t.Fatalf("plugin PDB MinAvailable = %v, want %d", pdb.Spec.MinAvailable, pluginReadyMin)
	}
	if pdb.Spec.Selector == nil || pdb.Spec.Selector.MatchLabels["app"] != pluginName {
		t.Fatalf("plugin PDB selector = %+v", pdb.Spec.Selector)
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatal("pod SecurityContext.RunAsNonRoot required")
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("pod SeccompProfile RuntimeDefault required")
	}
	var certVol *corev1.SecretVolumeSource
	var haveTmp bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		switch v.Name {
		case "serving-cert":
			certVol = v.Secret
		case "tmp":
			haveTmp = v.EmptyDir != nil && v.EmptyDir.SizeLimit != nil &&
				v.EmptyDir.SizeLimit.Value() == 32*1024*1024
		}
	}
	if certVol == nil || certVol.Optional == nil || !*certVol.Optional {
		t.Fatal("serving-cert volume must be optional until service-ca mints the Secret")
	}
	// 0440: root-owned secret volume, group-readable so the non-root nginx (in the
	// SCC-injected fsGroup) can read the serving-cert key.
	if certVol.DefaultMode == nil || *certVol.DefaultMode != 0o440 {
		t.Fatalf("serving-cert DefaultMode = %v, want 0440", certVol.DefaultMode)
	}
	if !haveTmp {
		t.Fatal("tmp emptyDir with 32Mi SizeLimit required for read-only rootfs")
	}
	csc := dep.Spec.Template.Spec.Containers[0].SecurityContext
	if csc == nil || csc.ReadOnlyRootFilesystem == nil || !*csc.ReadOnlyRootFilesystem {
		t.Fatal("plugin container ReadOnlyRootFilesystem required")
	}
	if csc.SeccompProfile == nil || csc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("plugin container SeccompProfile RuntimeDefault required")
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
	pdb := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(dep, pdb, consoleCluster(pluginName, "other")).Build(),
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
	err = r.Get(context.Background(), types.NamespacedName{Namespace: pluginNS, Name: pluginName}, &policyv1.PodDisruptionBudget{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("plugin PDB should be gone, err=%v", err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	if c == nil || c.Reason != "Disabled" {
		t.Fatalf("%+v", c)
	}
}

func TestReconcileNotFound(t *testing.T) {
	scheme := testScheme(t)
	// Seed stale posture so NotFound must clear gauges (alerts must not stick).
	resetMetrics(t)
	stale := &baselinev1alpha1.ClusterBaseline{}
	stale.Status.Score = ptr.To(int32(10))
	stale.Status.Profiles = []baselinev1alpha1.ProfileStatus{
		{Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Fail: 9}},
	}
	publishMetrics(stale)

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
	// Re-enqueue at the heartbeat interval so clearPublishedMetrics keeps the
	// freshness timestamp ticking; an absent object emits no watch event, so
	// without this the timestamp freezes and ComplianceStatusStale false-pages.
	if res.RequeueAfter != goneHeartbeat {
		t.Fatalf("requeue = %v, want %v (freshness heartbeat)", res.RequeueAfter, goneHeartbeat)
	}
	if got := testutil.ToFloat64(complianceScore); got != -1 {
		t.Fatalf("score after NotFound = %v, want -1", got)
	}
	if got := testutil.CollectAndCount(complianceChecks); got != 0 {
		t.Fatalf("checks after NotFound: %d series remain", got)
	}
	// The heartbeat's purpose: observed timestamp stays fresh (not the 0 seed),
	// so the stale-status alert cannot fire while the operator is healthy.
	if got := testutil.ToFloat64(statusObservedTimestamp); got <= 0 {
		t.Fatalf("observed timestamp after NotFound = %v, want fresh (>0)", got)
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
