package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
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
	suiteList := &unstructured.UnstructuredList{}
	suiteList.SetGroupVersionKind(suiteGVK.GroupVersion().WithKind(suiteGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(suiteGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(suiteList.GroupVersionKind(), suiteList)
	csvList := &unstructured.UnstructuredList{}
	csvList.SetGroupVersionKind(csvGVK.GroupVersion().WithKind(csvGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(csvGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(csvList.GroupVersionKind(), csvList)
	scheme.AddKnownTypeWithName(subscriptionGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(catalogSourceGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(operatorGroupGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(consolePluginGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(consoleGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(remediationGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(mcpGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(infrastructureGVK, &unstructured.Unstructured{})
	return scheme
}

// nodeRemediation builds an owned ComplianceRemediation whose rendered object
// is a MachineConfig for the given pool role.
func nodeRemediation(name, pool string) *unstructured.Unstructured {
	rem := &unstructured.Unstructured{}
	rem.SetGroupVersionKind(remediationGVK)
	rem.SetName(name)
	rem.SetNamespace(complianceNamespace)
	rem.SetLabels(map[string]string{suiteLabel: "baseline-cis"})
	_ = unstructured.SetNestedMap(rem.Object, map[string]any{
		"kind": "MachineConfig",
		"metadata": map[string]any{
			"labels": map[string]any{"machineconfiguration.openshift.io/role": pool},
		},
	}, "spec", "current", "object")
	return rem
}

func completedSuite(name string, ends ...time.Time) *unstructured.Unstructured {
	suite := &unstructured.Unstructured{}
	suite.SetGroupVersionKind(suiteGVK)
	suite.SetName(name)
	suite.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedField(suite.Object, "DONE", "status", "phase")
	statuses := make([]any, 0, len(ends))
	for i, end := range ends {
		statuses = append(statuses, map[string]any{
			"name":         fmt.Sprintf("scan-%d", i),
			"phase":        "DONE",
			"endTimestamp": end.UTC().Format(time.RFC3339Nano),
		})
	}
	_ = unstructured.SetNestedSlice(suite.Object, statuses, "status", "scanStatuses")
	return suite
}

// TestPoolFromRemediation: prefer the MachineConfig role label, fall back to the
// scan-name label ("<profile>-node-<pool>") when the CO leaves the role empty,
// and return "" for a non-node remediation.
// TestEnqueueSingleton: owned/unknown suite labels map to the ClusterBaseline
// singleton "cluster"; foreign suite labels and namespaces do not.
func TestEnqueueSingleton(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetNamespace(complianceNamespace)
	reqs := enqueueSingleton(context.Background(), obj)
	if len(reqs) != 1 || reqs[0].Name != "cluster" || reqs[0].Namespace != "" {
		t.Fatalf("enqueueSingleton = %+v, want one request Name=cluster", reqs)
	}
	// Owned suite label still enqueues.
	obj.SetLabels(map[string]string{suiteLabel: "baseline-cis"})
	if got := enqueueSingleton(context.Background(), obj); len(got) != 1 || got[0].Name != "cluster" {
		t.Fatalf("owned suite enqueued = %+v, want Name=cluster", got)
	}
	// Foreign suite label (other binding in the same namespace) is dropped so
	// multi-tenant CO churn does not force CCR list walks every event.
	obj.SetLabels(map[string]string{suiteLabel: "someone-elses-suite"})
	if got := enqueueSingleton(context.Background(), obj); len(got) != 0 {
		t.Fatalf("foreign suite enqueued singleton: %+v", got)
	}
	obj.SetLabels(nil)
	obj.SetNamespace("unrelated")
	if got := enqueueSingleton(context.Background(), obj); len(got) != 0 {
		t.Fatalf("foreign namespace enqueued singleton: %+v", got)
	}
}

func TestPoolFromRemediation(t *testing.T) {
	// Role label present -> use it.
	if got := poolFromRemediation(nodeRemediation("r", "worker")); got != "worker" {
		t.Fatalf("role-labeled: got %q, want worker", got)
	}
	// Role label empty, but the scan-name label carries the pool.
	rem := nodeRemediation("r", "")
	rem.SetLabels(map[string]string{"compliance.openshift.io/scan-name": "ocp4-pci-dss-node-master"})
	if got := poolFromRemediation(rem); got != "master" {
		t.Fatalf("scan-name fallback: got %q, want master", got)
	}
	// Tailored profile names may themselves contain "-node-"; the final
	// delimiter identifies the actual pool suffix.
	rem.SetLabels(map[string]string{
		suiteLabel: "baseline-cis", "compliance.openshift.io/scan-name": "custom-node-profile-node-worker",
	})
	if got := poolFromRemediation(rem); got != "worker" {
		t.Fatalf("last scan-name delimiter: got %q, want worker", got)
	}
	// Platform remediation (non-MachineConfig kind, no "-node-" scan) -> no pool.
	pl := &unstructured.Unstructured{}
	pl.SetGroupVersionKind(remediationGVK)
	_ = unstructured.SetNestedMap(pl.Object, map[string]any{"kind": "APIServer"}, "spec", "current", "object")
	pl.SetLabels(map[string]string{"compliance.openshift.io/scan-name": "ocp4-cis-api-server"})
	if got := poolFromRemediation(pl); got != "" {
		t.Fatalf("platform remediation: got %q, want empty", got)
	}
	// Non-MachineConfig node remediation (e.g. a KubeletConfig rendered by a node
	// scan) reboots the pool via the MCO, so the kind must NOT short-circuit the
	// scan-name fallback -> pool derived from "…-node-<pool>".
	kc := &unstructured.Unstructured{}
	kc.SetGroupVersionKind(remediationGVK)
	_ = unstructured.SetNestedMap(kc.Object, map[string]any{"kind": "KubeletConfig"}, "spec", "current", "object")
	kc.SetLabels(map[string]string{"compliance.openshift.io/scan-name": "ocp4-cis-node-worker"})
	if got := poolFromRemediation(kc); got != "worker" {
		t.Fatalf("KubeletConfig node remediation: got %q, want worker", got)
	}
	// Partial MachineConfig (no kind yet) still uses scan-name for the pool so
	// batch apply does not skip MCP pause.
	partial := &unstructured.Unstructured{}
	partial.SetGroupVersionKind(remediationGVK)
	_ = unstructured.SetNestedMap(partial.Object, map[string]any{}, "spec", "current", "object")
	partial.SetLabels(map[string]string{"compliance.openshift.io/scan-name": "ocp4-cis-node-worker"})
	if got := poolFromRemediation(partial); got != "worker" {
		t.Fatalf("empty kind scan-name fallback: got %q, want worker", got)
	}
	// Hostile / non-DNS1123 pool names from untrusted labels must not be used.
	badRole := nodeRemediation("r", "Not_A_Valid/Pool")
	if got := poolFromRemediation(badRole); got != "" {
		t.Fatalf("invalid role label: got %q, want empty", got)
	}
	badScan := nodeRemediation("r", "")
	badScan.SetLabels(map[string]string{"compliance.openshift.io/scan-name": "ocp4-cis-node-UPPER"})
	if got := poolFromRemediation(badScan); got != "" {
		t.Fatalf("invalid scan-name pool: got %q, want empty", got)
	}
}

// TestResolveCatalogSource: explicit spec wins; otherwise auto-detect OCP
// (redhat-operators) vs OKD (community-operators), defaulting when neither exists.
func TestResolveCatalogSource(t *testing.T) {
	scheme := testScheme(t)
	catalog := func(name string) *unstructured.Unstructured {
		cs := &unstructured.Unstructured{}
		cs.SetGroupVersionKind(catalogSourceGVK)
		cs.SetName(name)
		cs.SetNamespace("openshift-marketplace")
		return cs
	}
	ctx := context.Background()
	cases := []struct {
		name     string
		spec     string
		catalogs []string
		want     string
	}{
		{"explicit override", "my-catalog", []string{"redhat-operators"}, "my-catalog"},
		{"explicit override trimmed", "  my-catalog  ", []string{"redhat-operators"}, "my-catalog"},
		{"OCP: redhat-operators present", "", []string{"redhat-operators", "community-operators"}, "redhat-operators"},
		{"OKD: only community-operators", "", []string{"community-operators"}, "community-operators"},
		{"whitespace-only spec auto-detects OKD", "   ", []string{"community-operators"}, "community-operators"},
		{"whitespace-only spec, neither: default", "\t\n", nil, "redhat-operators"},
		{"neither: default", "", nil, "redhat-operators"},
		// The 0.5.6 CRD persisted its redhat-operators default into every spec;
		// it must be treated as unset so an upgraded OKD cluster still detects
		// community-operators instead of pinning a nonexistent catalog.
		{"persisted 0.5.6 default on OKD auto-detects", "redhat-operators", []string{"community-operators"}, "community-operators"},
		{"persisted 0.5.6 default on OCP keeps redhat-operators", "redhat-operators", []string{"redhat-operators", "community-operators"}, "redhat-operators"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			objs := []client.Object{}
			for _, n := range c.catalogs {
				objs = append(objs, catalog(n))
			}
			r := &ClusterBaselineReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
				Scheme: scheme,
			}
			cb := &baselinev1alpha1.ClusterBaseline{Spec: baselinev1alpha1.ClusterBaselineSpec{ComplianceCatalogSource: c.spec}}
			if got, _ := r.resolveCatalogSource(ctx, cb); got != c.want {
				t.Fatalf("resolveCatalogSource = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCatalogSourcePresent pins the safety in catalogSourcePresent: only
// NotFound / NoMatch mean a definite "catalog absent"; any other error
// (transient, forbidden) reads as PRESENT but NOT definite, so detection keeps
// its priority-ordered choice on a blip yet a writing caller (the Subscription
// sync) can decline to act on that guess.
func TestCatalogSourcePresent(t *testing.T) {
	scheme := testScheme(t)
	ctx := context.Background()
	noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "operators.coreos.com", Kind: "CatalogSource"}}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					switch key.Name {
					case "transient-cs":
						return apierrors.NewServiceUnavailable("apiserver blip")
					case "nomatch-cs":
						return noMatch
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if present, definite := r.catalogSourcePresent(ctx, "transient-cs"); !present || definite {
		t.Fatalf("transient error: got (present=%v, definite=%v), want (true, false)", present, definite)
	}
	if present, definite := r.catalogSourcePresent(ctx, "nomatch-cs"); present || !definite {
		t.Fatalf("NoMatch: got (present=%v, definite=%v), want (false, true)", present, definite)
	}
	if present, definite := r.catalogSourcePresent(ctx, "no-such-cs"); present || !definite {
		t.Fatalf("NotFound: got (present=%v, definite=%v), want (false, true)", present, definite)
	}
}

func machineConfigPool(name string) *unstructured.Unstructured {
	mcp := &unstructured.Unstructured{}
	mcp.SetGroupVersionKind(mcpGVK)
	mcp.SetName(name)
	return mcp
}

func newBatchCB() *baselinev1alpha1.ClusterBaseline {
	return &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis"},
		},
	}
}

// TestRemediationBatch: the annotation triggers pause + apply, then a later
// reconcile resumes once the remediation is Applied.
func TestRemediationBatch(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()

	// Phase 1: pause + apply. Annotation must stay until resume so a failed
	// Status().Update cannot leave pools paused with no batch to recover.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil || cb.Status.RemediationBatch.Phase != "Applying" {
		t.Fatalf("batch not started: %+v", cb.Status.RemediationBatch)
	}
	if cb.Annotations[batchApplyAnnotation] == "" {
		t.Fatal("annotation must remain until pools are resumed")
	}
	gotPool := machineConfigPool("worker")
	_ = r.Get(ctx, types.NamespacedName{Name: "worker"}, gotPool)
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); !paused {
		t.Fatal("worker pool not paused")
	}
	gotRem := &unstructured.Unstructured{}
	gotRem.SetGroupVersionKind(remediationGVK)
	_ = r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem)
	if apply, _, _ := unstructured.NestedBool(gotRem.Object, "spec", "apply"); !apply {
		t.Fatal("rem1 not set to apply")
	}

	// Phase 2: not Applied yet -> pool stays paused; annotation still held.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("batch resumed before Applied")
	}
	if cb.Annotations[batchApplyAnnotation] == "" {
		t.Fatal("annotation cleared before resume")
	}

	// Mark Applied -> resume and clear annotation.
	_ = unstructured.SetNestedField(gotRem.Object, "Applied", "status", "applicationState")
	_ = r.Update(ctx, gotRem)
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatalf("batch not cleared after Applied: %+v", cb.Status.RemediationBatch)
	}
	// Re-read from API: annotation clear is a meta Update.
	gotCB := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, gotCB); err != nil {
		t.Fatal(err)
	}
	if gotCB.Annotations[batchApplyAnnotation] != "" {
		t.Fatal("annotation not cleared after resume")
	}
	if err := r.Get(ctx, types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("worker pool not resumed")
	}
}

// TestRemediationBatchTooManyPoolsRejected: a batch spanning more distinct
// MachineConfigPools than status.remediationBatch.pools can hold (CRD MaxItems)
// must be refused before any pool is paused and the one-shot request dropped, so
// the oversized Pools list cannot fail Status().Update and freeze the reconcile.
func TestRemediationBatchTooManyPoolsRejected(t *testing.T) {
	scheme := testScheme(t)
	objs := []client.Object{}
	names := make([]string, 0, batchMaxPools+1)
	for i := 0; i <= batchMaxPools; i++ { // batchMaxPools+1 distinct pools
		pool := fmt.Sprintf("pool%d", i)
		remName := fmt.Sprintf("rem%d", i)
		objs = append(objs, nodeRemediation(remName, pool), machineConfigPool(pool))
		names = append(names, remName)
	}
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: strings.Join(names, ",")})
	objs = append(objs, cb)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(objs...).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatalf("oversized batch must not start: %+v", cb.Status.RemediationBatch)
	}
	gotCB := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, gotCB); err != nil {
		t.Fatal(err)
	}
	if gotCB.Annotations[batchApplyAnnotation] != "" {
		t.Fatal("oversized batch-apply annotation not cleared (would retry-freeze)")
	}
	for i := 0; i <= batchMaxPools; i++ {
		name := fmt.Sprintf("pool%d", i)
		p := machineConfigPool(name)
		_ = r.Get(ctx, types.NamespacedName{Name: name}, p)
		if paused, _, _ := unstructured.NestedBool(p.Object, "spec", "paused"); paused {
			t.Fatalf("%s paused despite rejected batch", name)
		}
	}
}

// TestRemediationBatchAllMissingClearsAnnotation: a batch of only NotFound
// remediations must not open a fake Applying batch; clear the one-shot request.
func TestRemediationBatchAllMissingClearsAnnotation(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "gone1,gone2"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatalf("fake batch opened for missing remediations: %+v", cb.Status.RemediationBatch)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[batchApplyAnnotation] != "" {
		t.Fatalf("annotation not cleared: %v", got.Annotations)
	}
}

// TestRemediationBatchPartialMissingFinishClears: a batch that drops a NotFound
// target at open must still clear its request annotation when it finishes.
// Regression: the open path rewrites the one-shot request to the surviving set
// so the finish-path exact match fires; otherwise the annotation would persist,
// reopening the batch every reconcile and thrashing MachineConfigPool pause.
func TestRemediationBatchPartialMissingFinishClears(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1,gone"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()

	// Open: "gone" is dropped; batch lists only rem1 and the request annotation
	// is normalized to the surviving set so finish can match it.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("partial batch did not open")
	}
	if got := cb.Annotations[batchApplyAnnotation]; got != "rem1" {
		t.Fatalf("request annotation not normalized to surviving set: %q", got)
	}

	// Mark Applied, then finish: pool resumes and annotation clears (no reopen).
	gotRem := &unstructured.Unstructured{}
	gotRem.SetGroupVersionKind(remediationGVK)
	_ = r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem)
	_ = unstructured.SetNestedField(gotRem.Object, "Applied", "status", "applicationState")
	_ = r.Update(ctx, gotRem)
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatalf("batch not cleared after Applied: %+v", cb.Status.RemediationBatch)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[batchApplyAnnotation] != "" {
		t.Fatalf("request annotation not cleared after finish (would reopen batch): %v", got.Annotations)
	}
}

// TestRemediationBatchNoMatchPropagates: CRDs absent must not look like every
// remediation was NotFound (which would clear the request without retry).
func TestRemediationBatchNoMatchPropagates(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	noMatch := &meta.NoKindMatchError{
		GroupKind: schema.GroupKind{Group: remediationGVK.Group, Kind: remediationGVK.Kind},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok && u.GroupVersionKind() == remediationGVK {
						return noMatch
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err == nil {
		t.Fatal("NoMatch must propagate so the batch request is retried")
	}
	if cb.Annotations[batchApplyAnnotation] == "" {
		t.Fatal("annotation must remain when CRDs are missing")
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatal("batch must not start when CRDs are missing")
	}
}

func TestRemediationBatchPreservesAdministratorPausedPool(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	gotRem := u(remediationGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem); err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(gotRem.Object, "Applied", "status", "applicationState")
	if err := r.Update(ctx, gotRem); err != nil {
		t.Fatal(err)
	}
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(ctx, types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); !paused {
		t.Fatal("batch unpaused a pool that was already paused by an administrator")
	}
	if gotPool.GetAnnotations()[batchPauseOwnerAnnotation] != "" {
		t.Fatal("operator claimed ownership of an administrator pause")
	}
}

func TestRemediationBatchRejectsForeignAndBlockedTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*unstructured.Unstructured)
	}{
		{"foreign suite", func(rem *unstructured.Unstructured) {
			rem.SetLabels(map[string]string{suiteLabel: "someone-elses-suite"})
		}},
		{"missing dependencies", func(rem *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(rem.Object, "MissingDependencies", "status", "applicationState")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := testScheme(t)
			rem := nodeRemediation("rem1", "worker")
			tc.mutate(rem)
			pool := machineConfigPool("worker")
			cb := newBatchCB()
			cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
			r := &ClusterBaselineReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem, pool).
					WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
				Scheme: scheme,
			}
			// All-permanent-reject: skip targets, clear one-shot annotation, no
			// sticky Degrade, no pause, no apply (empty-keep path).
			if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
				t.Fatalf("permanent reject must not sticky-error: %v", err)
			}
			if cb.Status.RemediationBatch != nil {
				t.Fatalf("unsafe batch must not open: %+v", cb.Status.RemediationBatch)
			}
			gotCB := &baselinev1alpha1.ClusterBaseline{}
			if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, gotCB); err != nil {
				t.Fatal(err)
			}
			if gotCB.Annotations[batchApplyAnnotation] != "" {
				t.Fatal("rejected batch-apply annotation not cleared (would sticky-Degrade)")
			}
			gotPool := machineConfigPool("worker")
			if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool); err != nil {
				t.Fatal(err)
			}
			if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
				t.Fatal("pool was paused for an empty/rejected batch")
			}
			gotRem := u(remediationGVK)
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem); err != nil {
				t.Fatal(err)
			}
			if apply, _, _ := unstructured.NestedBool(gotRem.Object, "spec", "apply"); apply {
				t.Fatal("unsafe remediation was applied through operator permissions")
			}
		})
	}
}

// TestRemediationBatchSkipsPermanentRejectsKeepsValid: a mixed list with one
// foreign (or blocked) name must still open a batch for the owned, applyable
// remediations. Aborting the whole request on the first permanent reject would
// drop valid work and was a sticky-Degrade risk class when only rejects remain.
func TestRemediationBatchSkipsPermanentRejectsKeepsValid(t *testing.T) {
	scheme := testScheme(t)
	good := nodeRemediation("rem-good", "worker")
	foreign := nodeRemediation("rem-foreign", "worker")
	foreign.SetLabels(map[string]string{suiteLabel: "someone-elses-suite"})
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-good,rem-foreign"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, good, foreign, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("batch must open for the valid remediation")
	}
	if got := cb.Status.RemediationBatch.Remediations; len(got) != 1 || got[0] != "rem-good" {
		t.Fatalf("batch remediations = %v, want [rem-good] only", got)
	}
	gotGood := u(remediationGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem-good"}, gotGood); err != nil {
		t.Fatal(err)
	}
	if apply, _, _ := unstructured.NestedBool(gotGood.Object, "spec", "apply"); !apply {
		t.Fatal("valid remediation must be applied")
	}
	gotForeign := u(remediationGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem-foreign"}, gotForeign); err != nil {
		t.Fatal(err)
	}
	if apply, _, _ := unstructured.NestedBool(gotForeign.Object, "spec", "apply"); apply {
		t.Fatal("foreign remediation must not be applied")
	}
}

// TestRemediationBatchSkipsCorruptApplicationState: wrong-type applicationState
// is a permanent corrupt status. Must not sticky-Degrade; clear when alone.
func TestRemediationBatchSkipsCorruptApplicationState(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	// NestedString fails on non-string status.applicationState.
	_ = unstructured.SetNestedField(rem.Object, int64(1), "status", "applicationState")
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatalf("corrupt status must not sticky-error: %v", err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatal("batch must not open for corrupt-only request")
	}
	gotCB := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, gotCB); err != nil {
		t.Fatal(err)
	}
	if gotCB.Annotations[batchApplyAnnotation] != "" {
		t.Fatal("annotation not cleared for all-corrupt permanent reject")
	}
}

// TestRemediationBatchSkipsCorruptSpecApply: wrong-type spec.apply is a permanent
// start reject (same class as corrupt applicationState). Alone clears annotation;
// mixed with a valid rem keeps only the valid name in the batch.
func TestRemediationBatchSkipsCorruptSpecApply(t *testing.T) {
	scheme := testScheme(t)
	// Alone: empty-keep clears annotation, no sticky Degrade, no pause.
	t.Run("alone", func(t *testing.T) {
		rem := nodeRemediation("rem-bad", "worker")
		_ = unstructured.SetNestedField(rem.Object, "yes", "spec", "apply") // not bool
		pool := machineConfigPool("worker")
		cb := newBatchCB()
		cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-bad"})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem, pool).
				WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
			Scheme: scheme,
		}
		if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
			t.Fatalf("corrupt apply must not sticky-error: %v", err)
		}
		if cb.Status.RemediationBatch != nil {
			t.Fatal("batch must not open for corrupt-apply-only request")
		}
		gotCB := &baselinev1alpha1.ClusterBaseline{}
		if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, gotCB); err != nil {
			t.Fatal(err)
		}
		if gotCB.Annotations[batchApplyAnnotation] != "" {
			t.Fatal("annotation not cleared for all-corrupt-apply reject")
		}
		gotPool := machineConfigPool("worker")
		_ = r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool)
		if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
			t.Fatal("pool must not pause for corrupt-apply-only batch")
		}
	})
	// Mixed: valid rem opens batch; corrupt apply sibling is not listed.
	t.Run("mixed", func(t *testing.T) {
		good := nodeRemediation("rem-good", "worker")
		bad := nodeRemediation("rem-bad", "worker")
		_ = unstructured.SetNestedField(bad.Object, "yes", "spec", "apply")
		pool := machineConfigPool("worker")
		cb := newBatchCB()
		cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-good,rem-bad"})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(cb, good, bad, pool).
				WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
			Scheme: scheme,
		}
		if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
			t.Fatal(err)
		}
		if cb.Status.RemediationBatch == nil {
			t.Fatal("batch must open for the valid remediation")
		}
		if got := cb.Status.RemediationBatch.Remediations; len(got) != 1 || got[0] != "rem-good" {
			t.Fatalf("batch remediations = %v, want [rem-good] only", got)
		}
	})
}

// TestRemediationBatchWaitTreatsCorruptApplyAsDone: a listed rem with unreadable
// spec.apply must not set wait getErr (sticky Degrade until grace) when siblings
// are finished. Cancel/finish proceeds without waiting 10m.
func TestRemediationBatchWaitTreatsCorruptApplyAsDone(t *testing.T) {
	scheme := testScheme(t)
	// In-flight batch: one rem never applying (apply=false), one with corrupt apply.
	// Wait should cancel immediately (no anyApplying, no getErr).
	good := nodeRemediation("rem-good", "worker")
	_ = unstructured.SetNestedField(good.Object, false, "spec", "apply")
	_ = unstructured.SetNestedField(good.Object, "Pending", "status", "applicationState")
	corrupt := nodeRemediation("rem-corrupt", "worker")
	_ = unstructured.SetNestedField(corrupt.Object, "yes", "spec", "apply")
	_ = unstructured.SetNestedField(corrupt.Object, "Pending", "status", "applicationState")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	cb := newBatchCB()
	owner := batchPauseOwner(cb)
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: owner})
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase:        baselinev1alpha1.RemediationBatchPhaseApplying,
		Pools:        []string{"worker"},
		Remediations: []string{"rem-good", "rem-corrupt"},
		StartedAt:    metav1.Now(),
		PauseOwner:   owner,
	}
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-good,rem-corrupt"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, good, corrupt, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatalf("wait must not sticky-error on corrupt apply: %v", err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatal("batch must finish (cancel) when no rem is apply=true")
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("pool must resume after cancel with corrupt sibling treated as done")
	}
}

// TestSetMCPPausedSkipsCorruptPausedField: wrong-type MCP spec.paused must not
// fail setMCPPaused (would sticky-Degrade batch start with annotation kept).
func TestSetMCPPausedSkipsCorruptPausedField(t *testing.T) {
	scheme := testScheme(t)
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, "yes", "spec", "paused") // not bool
	cb := newBatchCB()
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
		Scheme: scheme,
	}
	if err := r.setMCPPaused(context.Background(), "worker", true, batchPauseOwner(cb)); err != nil {
		t.Fatalf("corrupt paused must skip, not error: %v", err)
	}
	// Batch start with a node rem targeting that pool must not sticky-error.
	rem := nodeRemediation("rem1", "worker")
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	r2 := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r2.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatalf("batch start with corrupt MCP paused must not sticky-error: %v", err)
	}
}

// TestSetMCPPausedForeignOwnerDoesNotSticky: a pause marker from another owner
// (recreated CR UID, hand-edit) must not error and sticky-Degrade batch start.
// We take over the marker so finish-path resume can unpause (skipping resume when
// marker != owner would leave the pool paused forever).
func TestSetMCPPausedForeignOwnerDoesNotSticky(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	owner := batchPauseOwner(cb)

	t.Run("paused under foreign owner", func(t *testing.T) {
		pool := machineConfigPool("worker")
		_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
		pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "old-uid-from-previous-cr"})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
			Scheme: scheme,
		}
		if err := r.setMCPPaused(context.Background(), "worker", true, owner); err != nil {
			t.Fatalf("foreign owner while paused must not error: %v", err)
		}
		got := machineConfigPool("worker")
		if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
			t.Fatal(err)
		}
		if got.GetAnnotations()[batchPauseOwnerAnnotation] != owner {
			t.Fatalf("must take over foreign pause owner, got %q want %q",
				got.GetAnnotations()[batchPauseOwnerAnnotation], owner)
		}
		if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); !paused {
			t.Fatal("pool must stay paused after takeover")
		}
	})

	t.Run("stale marker unpaused", func(t *testing.T) {
		pool := machineConfigPool("worker")
		_ = unstructured.SetNestedField(pool.Object, false, "spec", "paused")
		pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "old-uid-from-previous-cr"})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
			Scheme: scheme,
		}
		if err := r.setMCPPaused(context.Background(), "worker", true, owner); err != nil {
			t.Fatalf("stale marker must claim, not error: %v", err)
		}
		got := machineConfigPool("worker")
		if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
			t.Fatal(err)
		}
		if got.GetAnnotations()[batchPauseOwnerAnnotation] != owner {
			t.Fatalf("pause owner = %q, want %q", got.GetAnnotations()[batchPauseOwnerAnnotation], owner)
		}
		if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); !paused {
			t.Fatal("pool must be paused after reclaim")
		}
	})

	// Full batch start path: foreign marker must not sticky-error; finish can resume.
	t.Run("batch start and finish with foreign paused marker", func(t *testing.T) {
		rem := nodeRemediation("rem1", "worker")
		pool := machineConfigPool("worker")
		_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
		pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "stale-other-owner"})
		cb2 := newBatchCB()
		cb2.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(cb2, rem, pool).
				WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
			Scheme: scheme,
		}
		ctx := context.Background()
		if err := r.applyRemediationBatch(ctx, cb2); err != nil {
			t.Fatalf("batch start must not sticky-error on foreign pause owner: %v", err)
		}
		if cb2.Status.RemediationBatch == nil {
			t.Fatal("batch must open when foreign pause is already held")
		}
		// Finish: mark rem Applied, wait path resumes because we took over marker.
		gotRem := u(remediationGVK)
		if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem); err != nil {
			t.Fatal(err)
		}
		_ = unstructured.SetNestedField(gotRem.Object, "Applied", "status", "applicationState")
		if err := r.Update(ctx, gotRem); err != nil {
			t.Fatal(err)
		}
		if err := r.applyRemediationBatch(ctx, cb2); err != nil {
			t.Fatal(err)
		}
		if cb2.Status.RemediationBatch != nil {
			t.Fatal("batch must finish after Applied")
		}
		gotPool := machineConfigPool("worker")
		if err := r.Get(ctx, types.NamespacedName{Name: "worker"}, gotPool); err != nil {
			t.Fatal(err)
		}
		if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
			t.Fatal("pool must resume after batch finish (takeover enables resume)")
		}
	})
}

// TestApplyOwnedRemediationSkipsPermanentReject: post-pause apply path must not
// sticky-error when a target races to foreign/MissingDeps/corrupt after validation.
func TestApplyOwnedRemediationSkipsPermanentReject(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	rem.SetLabels(map[string]string{suiteLabel: "someone-elses-suite"})
	cb := newBatchCB()
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	suites := ownedSuites(cb)
	if err := r.applyOwnedRemediation(context.Background(), cb, "rem1", suites); err != nil {
		t.Fatalf("permanent reject at apply must skip, not error: %v", err)
	}
	got := u(remediationGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, got); err != nil {
		t.Fatal(err)
	}
	if apply, _, _ := unstructured.NestedBool(got.Object, "spec", "apply"); apply {
		t.Fatal("foreign remediation must not be applied")
	}
}

func TestRemediationBatchDeduplicatesNamesAndPreservesQueuedRequest(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1, rem1"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.RemediationBatch.Remediations; len(got) != 1 || got[0] != "rem1" {
		t.Fatalf("batch remediations = %v, want deduplicated [rem1]", got)
	}
	// A second request arriving during this batch must not be deleted when the
	// first completes; it will start on the next reconcile. Persist via the
	// API (not only the in-memory map): finish re-Gets before clearing so a
	// concurrent console patch is visible.
	latest := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, types.NamespacedName{Name: cb.Name}, latest); err != nil {
		t.Fatal(err)
	}
	ann := maps.Clone(latest.GetAnnotations())
	if ann == nil {
		ann = map[string]string{}
	}
	ann[batchApplyAnnotation] = "rem2"
	latest.SetAnnotations(ann)
	if err := r.Update(ctx, latest); err != nil {
		t.Fatal(err)
	}
	cb.SetAnnotations(ann)
	cb.SetResourceVersion(latest.GetResourceVersion())
	gotRem := u(remediationGVK)
	_ = r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem)
	_ = unstructured.SetNestedField(gotRem.Object, "Applied", "status", "applicationState")
	_ = r.Update(ctx, gotRem)
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Annotations[batchApplyAnnotation] != "rem2" {
		t.Fatalf("queued batch annotation was lost: %v", cb.Annotations)
	}
}

// TestRemediationBatchCancelResumes: reverting every remediation to apply=false
// mid-batch cancels it, so the pool resumes at once (not only after the grace).
func TestRemediationBatchCancelResumes(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()

	// Phase 1: pause + apply.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("batch not started")
	}

	// User reverts the remediation to apply=false (not Applied).
	gotRem := &unstructured.Unstructured{}
	gotRem.SetGroupVersionKind(remediationGVK)
	_ = r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "rem1"}, gotRem)
	_ = unstructured.SetNestedField(gotRem.Object, false, "spec", "apply")
	_ = r.Update(ctx, gotRem)

	// Next reconcile: cancelled -> resume without waiting out the grace.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatalf("batch not cleared after cancel: %+v", cb.Status.RemediationBatch)
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(ctx, types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("worker pool left paused after cancel")
	}
}

func TestBatchPastGrace(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	grace := 10 * time.Minute
	if !batchPastGrace(metav1.Time{}, now) {
		t.Fatal("zero StartedAt must be past grace (corrupt status safety valve)")
	}
	// Modest future skew (NTP / handoff) must keep the pause live.
	if batchPastGrace(metav1.NewTime(now.Add(time.Second)), now) {
		t.Fatal("1s-ahead StartedAt must not force resume")
	}
	if batchPastGrace(metav1.NewTime(now.Add(grace)), now) {
		// Equal to now+grace is not After(now+grace); still within bound.
		t.Fatal("StartedAt == now+grace must not force resume via far-future path")
	}
	// Far future (beyond grace) is corrupt garbage, same class as zero.
	if !batchPastGrace(metav1.NewTime(now.Add(grace+time.Second)), now) {
		t.Fatal("far-future StartedAt (beyond grace) must be past grace")
	}
	if !batchPastGrace(metav1.NewTime(now.Add(time.Hour)), now) {
		t.Fatal("hour-future StartedAt must be past grace")
	}
	if batchPastGrace(metav1.NewTime(now.Add(-time.Minute)), now) {
		t.Fatal("fresh start must not be past grace")
	}
	if !batchPastGrace(metav1.NewTime(now.Add(-grace-time.Second)), now) {
		t.Fatal("elapsed grace must be past")
	}
}

// TestRemediationBatchZeroStartedAtResumes: hand-edited / corrupt batch with
// zero StartedAt must not disable the grace valve forever.
func TestRemediationBatchZeroStartedAtResumes(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase: "Applying", Pools: []string{"worker"}, Remediations: []string{"rem1"},
		// StartedAt zero: old bug would never pastGrace.
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatal("zero StartedAt must force resume and clear batch")
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("pool must resume when StartedAt is zero")
	}
}

// TestRemediationBatchPauseFailureRollsBack: if a later pool fails to pause,
// pools already paused this attempt must be unpaused so nothing is stuck
// without a status.remediationBatch.
func TestRemediationBatchPauseFailureRollsBack(t *testing.T) {
	scheme := testScheme(t)
	remW := nodeRemediation("rem-w", "worker")
	remM := nodeRemediation("rem-m", "master")
	// Distinct names so both remediations can exist; pools worker then master
	// (sorted). Fail pause on master after worker succeeds.
	poolW := machineConfigPool("worker")
	poolM := machineConfigPool("master")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-w,rem-m"})
	pauseCalls := 0
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, remW, remM, poolW, poolM).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					u, ok := obj.(*unstructured.Unstructured)
					if ok && u.GroupVersionKind() == mcpGVK {
						pauseCalls++
						// Sorted poolList is master, worker. Fail the second pause
						// so the first must be rolled back.
						if u.GetName() == "worker" {
							return apierrors.NewServiceUnavailable("pause worker failed")
						}
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	err := r.applyRemediationBatch(context.Background(), cb)
	if err == nil {
		t.Fatal("expected pause failure")
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatal("batch must not start when pause fails")
	}
	// master is first alphabetically; it was paused then rolled back on worker failure.
	gotM := machineConfigPool("master")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "master"}, gotM); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotM.Object, "spec", "paused"); paused {
		t.Fatal("master must be unpaused after rollback")
	}
	if pauseCalls < 2 {
		t.Fatalf("expected pause + rollback patches, got %d", pauseCalls)
	}
}

// TestRemediationBatchApplyFailureResumeFailsRecordsBatch: when apply fails and
// the best-effort unpause also fails, status.remediationBatch must be set so
// batchResumeGrace can force resume instead of leaving pools paused forever.
func TestRemediationBatchApplyFailureResumeFailsRecordsBatch(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	// Pause succeeds; apply (remediation patch) fails; unpause then fails.
	paused := false
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					u, ok := obj.(*unstructured.Unstructured)
					if !ok {
						return c.Patch(ctx, obj, patch, opts...)
					}
					switch u.GroupVersionKind() {
					case mcpGVK:
						wantPause, _, _ := unstructured.NestedBool(u.Object, "spec", "paused")
						if wantPause {
							paused = true
							return c.Patch(ctx, obj, patch, opts...)
						}
						// Unpause after apply failure.
						return apierrors.NewServiceUnavailable("resume worker failed")
					case remediationGVK:
						return apierrors.NewServiceUnavailable("apply rem1 failed")
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	err := r.applyRemediationBatch(context.Background(), cb)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if !paused {
		t.Fatal("pool should have been paused before apply failed")
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("batch status must be recorded when resume fails so grace can unpause")
	}
	if got := cb.Status.RemediationBatch.Pools; len(got) != 1 || got[0] != "worker" {
		t.Fatalf("batch pools = %v, want [worker]", got)
	}
}

// TestRemediationBatchApplyingGetErrorKeepsPaused: a transient Get failure while
// checking applicationState must not be treated as Applied (would unpause pools
// and clear the batch before remediations finish), as long as grace has not elapsed.
func TestRemediationBatchApplyingGetErrorKeepsPaused(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase: "Applying", Pools: []string{"worker"}, Remediations: []string{"rem1"},
		StartedAt: metav1.Now(),
	}
	boom := apierrors.NewServiceUnavailable("apiserver blip")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok && u.GroupVersionKind() == remediationGVK {
						return boom
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	err := r.applyRemediationBatch(context.Background(), cb)
	if err == nil {
		t.Fatal("transient Get error must surface before grace")
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("batch must not clear on Get error before grace")
	}
	if cb.Annotations[batchApplyAnnotation] == "" {
		t.Fatal("annotation must not clear on Get error before grace")
	}
	gotPool := machineConfigPool("worker")
	_ = r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool)
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); !paused {
		t.Fatal("pool must stay paused when Get fails before grace (not treat as Applied)")
	}
}

// TestRemediationBatchGetErrorPastGraceResumes: persistent Get failures must not
// bypass batchResumeGrace; after grace, pools resume even if status cannot be read.
func TestRemediationBatchGetErrorPastGraceResumes(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase: "Applying", Pools: []string{"worker"}, Remediations: []string{"rem1"},
		StartedAt: metav1.NewTime(time.Now().Add(-batchResumeGrace - time.Minute)),
	}
	boom := apierrors.NewServiceUnavailable("apiserver blip")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok && u.GroupVersionKind() == remediationGVK {
						return boom
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatalf("past grace must resume despite Get error, got %v", err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatal("batch must clear after grace resume")
	}
	gotCB := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, gotCB); err != nil {
		t.Fatal(err)
	}
	if gotCB.Annotations[batchApplyAnnotation] != "" {
		t.Fatal("annotation must clear after grace resume")
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("pool must resume after grace even when Get fails")
	}
}

// TestRemediationBatchEmptyAnnotationNoop: commas-only annotation must not open
// an empty status.remediationBatch.
func TestRemediationBatchEmptyAnnotationNoop(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: " , , "})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch != nil {
		t.Fatalf("empty CSV must not start batch: %+v", cb.Status.RemediationBatch)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Annotations[batchApplyAnnotation]; ok {
		t.Fatalf("empty batch annotation was not cleaned up: %v", got.Annotations)
	}
}

// TestRemediationBatchRestartsFromAnnotation: if status.remediationBatch was
// never persisted (Status().Update failed after pause), the kept annotation
// restarts the batch instead of leaving pools paused forever.
func TestRemediationBatchRestartsFromAnnotation(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	// Pool already paused as if a prior start succeeded but status was lost.
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "cluster"})
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	// No RemediationBatch in status: models lost status write.
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("annotation must restart batch when status was lost")
	}
	if cb.Annotations[batchStartedAtAnnotation] == "" || cb.Annotations[batchPoolsAnnotation] != "worker" {
		t.Fatalf("durable recovery metadata missing: %v", cb.Annotations)
	}
}

// TestRemediationBatchPartialReopenKeepsPreCrashPool: after a status-lost crash,
// re-opening the batch rebuilds pools from only the surviving remediations. A
// pool paused before the crash whose remediation vanished in the restart window
// must still be tracked (via the batch-pools annotation) so it is resumed, not
// left paused forever.
func TestRemediationBatchPartialReopenKeepsPreCrashPool(t *testing.T) {
	scheme := testScheme(t)
	// rem1 -> worker survives; rem2 -> master was deleted during the restart window
	// (not created here), but master was paused before the crash.
	rem1 := nodeRemediation("rem1", "worker")
	worker := machineConfigPool("worker")
	master := machineConfigPool("master")
	for _, p := range []*unstructured.Unstructured{worker, master} {
		_ = unstructured.SetNestedField(p.Object, true, "spec", "paused")
		p.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "cluster"})
	}
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{
		batchApplyAnnotation:     "rem1,rem2",
		batchPoolsAnnotation:     "master,worker", // both paused pre-crash
		batchStartedAtAnnotation: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano),
	})
	// No RemediationBatch in status: models the lost status write.
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem1, worker, master).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil {
		t.Fatal("batch not re-opened from annotation")
	}
	// master (rem2 gone) must survive in the tracked pool set, not just worker.
	got := cb.Status.RemediationBatch.Pools
	if !slices.Contains(got, "master") || !slices.Contains(got, "worker") {
		t.Fatalf("status.remediationBatch.pools = %v, want both master and worker (pre-crash pool must not drop)", got)
	}
	if cb.Annotations[batchPoolsAnnotation] != "master,worker" {
		t.Fatalf("batch-pools annotation = %q, want master,worker", cb.Annotations[batchPoolsAnnotation])
	}
}

// TestHistoryScoringModeStampDeferredToPersist: the in-memory stamp must not
// write the annotation to the API (it would then lead the history rings, which
// are only persisted by the trailing Status().Update). persistHistoryScoringMode
// is the durable write, called only after that update succeeds.
func TestHistoryScoringModeStampDeferredToPersist(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	weighted := string(baselinev1alpha1.ScoringSeverityWeighted)

	r.stampHistoryScoringMode(cb)
	if cb.Annotations[historyScoringModeAnn] != weighted {
		t.Fatalf("in-memory stamp not applied: %v", cb.Annotations)
	}
	server := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, server); err != nil {
		t.Fatal(err)
	}
	if server.Annotations[historyScoringModeAnn] != "" {
		t.Fatalf("in-memory stamp must not persist to the API, got %q", server.Annotations[historyScoringModeAnn])
	}

	if err := r.persistHistoryScoringMode(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, server); err != nil {
		t.Fatal(err)
	}
	if server.Annotations[historyScoringModeAnn] != weighted {
		t.Fatalf("persist did not write the annotation: %v", server.Annotations)
	}
}

// TestRemediationBatchEmptyKeepPreservesResubmit: when the batch's remediations
// are all gone, clearing the one-shot request must not delete a fresh console
// resubmit that landed after the reconcile read the annotation.
func TestRemediationBatchEmptyKeepPreservesResubmit(t *testing.T) {
	scheme := testScheme(t)
	// The client already holds the resubmitted request "remB".
	stored := newBatchCB()
	stored.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-b"})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(stored).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	// The reconcile read the earlier request "remA" (its target is now NotFound).
	stale := newBatchCB()
	stale.SetAnnotations(map[string]string{batchApplyAnnotation: "rem-a"})
	if err := r.applyRemediationBatch(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[batchApplyAnnotation] != "rem-b" {
		t.Fatalf("resubmit clobbered: batch-apply = %q, want rem-b", got.Annotations[batchApplyAnnotation])
	}
}

func TestRemediationBatchStatusFailureCannotResetGrace(t *testing.T) {
	scheme := testScheme(t)
	rem := nodeRemediation("rem1", "worker")
	_ = unstructured.SetNestedField(rem.Object, true, "spec", "apply")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "cluster"})
	started := time.Now().Add(-batchResumeGrace - time.Minute).UTC()
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{
		batchApplyAnnotation:     "rem1",
		batchStartedAtAnnotation: started.Format(time.RFC3339Nano),
		batchPoolsAnnotation:     "worker",
	})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, rem, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	// Models a previous end-of-reconcile status write failure: metadata + MCP
	// marker survived but status did not. Restart must reuse the old clock.
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.RemediationBatch == nil || !cb.Status.RemediationBatch.StartedAt.Time.Equal(started) {
		t.Fatalf("batch start time reset after status loss: %+v", cb.Status.RemediationBatch)
	}
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		t.Fatal(err)
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(ctx, types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("durable grace clock did not force pool resume")
	}
}

func TestRemediationBatchRemovedRequestRecoversWithoutStatus(t *testing.T) {
	scheme := testScheme(t)
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "cluster"})
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{
		batchStartedAtAnnotation: metav1.Now().Format(time.RFC3339Nano),
		batchPoolsAnnotation:     "worker",
	})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb, pool).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if err := r.applyRemediationBatch(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	gotPool := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, gotPool); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(gotPool.Object, "spec", "paused"); paused {
		t.Fatal("removed request left an ownership-marked pool paused")
	}
	if cb.Annotations[batchStartedAtAnnotation] != "" || cb.Annotations[batchPoolsAnnotation] != "" {
		t.Fatalf("orphan recovery metadata not cleared: %v", cb.Annotations)
	}
}

// Corrupt batch-started-at must fail closed (past epoch), not reset to "now"
// which would extend grace and leave MCPs paused another full window.
func TestEnsureBatchMetadataCorruptStartedAtFailClosed(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{
		batchStartedAtAnnotation: "not-a-timestamp",
	})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	started, err := r.ensureBatchMetadata(context.Background(), cb, []string{"worker"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !batchPastGrace(started, time.Now()) {
		t.Fatalf("corrupt started-at must be past grace, got %v", started)
	}
	if got := cb.Annotations[batchStartedAtAnnotation]; got == "not-a-timestamp" || got == "" {
		t.Fatalf("corrupt annotation not rewritten: %q", got)
	}
}

// TestEnsureComplianceDashboard: the operator creates the console-dashboard
// ConfigMap in openshift-config-managed, labeled console.openshift.io/dashboard,
// carrying the embedded Grafana-schema JSON and an owner ref for GC.
func TestEnsureComplianceDashboard(t *testing.T) {
	scheme := testScheme(t)
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.SetName("cluster")
	cb.SetUID("test-uid")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).Build(),
		Scheme: scheme,
	}
	ctx := context.Background()
	r.ensureComplianceDashboard(ctx, cb)
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: dashboardNS, Name: dashboardName}, cm); err != nil {
		t.Fatalf("dashboard ConfigMap not created: %v", err)
	}
	if cm.Labels["console.openshift.io/dashboard"] != "true" {
		t.Fatalf("missing dashboard label: %v", cm.Labels)
	}
	json := cm.Data["baseline-security-compliance.json"]
	if !strings.Contains(json, "baseline_security_compliance_score") {
		t.Fatalf("dashboard JSON missing score query: %q", json)
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != "test-uid" {
		t.Fatalf("missing/incorrect owner ref: %+v", cm.OwnerReferences)
	}
	// Idempotent: a second reconcile must not duplicate or mutate the CM.
	r.ensureComplianceDashboard(ctx, cb)
	list := &corev1.ConfigMapList{}
	if err := r.List(ctx, list, client.InNamespace(dashboardNS)); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("second call duplicated dashboard: %d ConfigMaps in %s", len(list.Items), dashboardNS)
	}
	if got := list.Items[0].Data["baseline-security-compliance.json"]; got != json {
		t.Fatalf("second call mutated dashboard JSON")
	}
}

func TestApplyPluginContainerRemovesUnownedPodPayloads(t *testing.T) {
	runtimeClass := "unreviewed-runtime"
	priority := int32(1000000)
	shareProcesses := true
	pod := &corev1.PodSpec{
		Containers:            []corev1.Container{{Name: "sidecar", Image: "unreviewed"}},
		InitContainers:        []corev1.Container{{Name: "init", Image: "unreviewed"}},
		Volumes:               []corev1.Volume{{Name: "host-data"}},
		ImagePullSecrets:      []corev1.LocalObjectReference{{Name: "stolen-pull-secret"}},
		HostNetwork:           true,
		HostPID:               true,
		HostIPC:               true,
		ShareProcessNamespace: &shareProcesses,
		NodeName:              "forced-node",
		NodeSelector:          map[string]string{"unreviewed": "true"},
		Tolerations:           []corev1.Toleration{{Key: "unreviewed"}},
		RuntimeClassName:      &runtimeClass,
		Priority:              &priority,
		ActiveDeadlineSeconds: ptr.To(int64(1)),
	}
	applyPluginContainer(pod, "example.test/plugin:1")
	if len(pod.Containers) != 1 || pod.Containers[0].Name != pluginName {
		t.Fatalf("containers = %+v, want only managed plugin", pod.Containers)
	}
	if len(pod.InitContainers) != 0 {
		t.Fatalf("unowned init containers survived reconcile: %+v", pod.InitContainers)
	}
	if len(pod.ImagePullSecrets) != 0 {
		t.Fatalf("imagePullSecrets survived reconcile: %+v", pod.ImagePullSecrets)
	}
	if len(pod.Volumes) != 2 {
		t.Fatalf("volumes = %+v, want serving-cert + tmp", pod.Volumes)
	}
	var haveCert, haveTmp bool
	for _, v := range pod.Volumes {
		switch v.Name {
		case "serving-cert":
			haveCert = true
			// 0440: group-read so the non-root nginx (member of the SCC-injected
			// fsGroup that owns the volume) can read the root-owned serving cert.
			if v.Secret == nil || v.Secret.DefaultMode == nil || *v.Secret.DefaultMode != 0o440 {
				t.Fatalf("serving-cert DefaultMode = %v, want 0440", v.Secret)
			}
		case "tmp":
			haveTmp = true
			if v.EmptyDir == nil {
				t.Fatalf("tmp volume must be emptyDir: %+v", v)
			}
			if v.EmptyDir.SizeLimit == nil || v.EmptyDir.SizeLimit.Value() != 32*1024*1024 {
				t.Fatalf("tmp SizeLimit = %v, want 32Mi", v.EmptyDir.SizeLimit)
			}
		default:
			t.Fatalf("unexpected volume %q", v.Name)
		}
	}
	if !haveCert || !haveTmp {
		t.Fatalf("volumes = %+v, want serving-cert + tmp", pod.Volumes)
	}
	sc := pod.Containers[0].SecurityContext
	if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Fatal("plugin container ReadOnlyRootFilesystem required")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Fatal("plugin container AllowPrivilegeEscalation must be false")
	}
	if sc.Privileged == nil || *sc.Privileged {
		t.Fatal("plugin container Privileged must be false")
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("plugin container SeccompProfile RuntimeDefault required")
	}
	if pod.HostNetwork || pod.HostPID || pod.HostIPC || pod.ShareProcessNamespace != nil ||
		pod.NodeName != "" || len(pod.NodeSelector) != 0 || len(pod.Tolerations) != 0 ||
		pod.RuntimeClassName != nil || pod.Priority != nil || pod.ActiveDeadlineSeconds != nil {
		t.Fatalf("unsafe pod state survived reconcile: %+v", pod)
	}
	if pod.ServiceAccountName != "default" || pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatalf("plugin service account settings = name %q automount %v", pod.ServiceAccountName, pod.AutomountServiceAccountToken)
	}
	if pod.Containers[0].ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("imagePullPolicy = %q, want IfNotPresent", pod.Containers[0].ImagePullPolicy)
	}
	if pod.Containers[0].TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
		t.Fatalf("TerminationMessagePolicy = %q, want FallbackToLogsOnError", pod.Containers[0].TerminationMessagePolicy)
	}
	// startupProbe owns cold start; liveness must not use InitialDelaySeconds alone.
	c0 := pod.Containers[0]
	if c0.StartupProbe == nil || c0.StartupProbe.TCPSocket == nil || c0.StartupProbe.FailureThreshold != 30 {
		t.Fatalf("StartupProbe = %+v, want TCP :9443 failureThreshold 30", c0.StartupProbe)
	}
	if c0.ReadinessProbe == nil {
		t.Fatal("ReadinessProbe required")
	}
	if c0.ReadinessProbe.InitialDelaySeconds != 0 {
		t.Fatalf("ReadinessProbe InitialDelaySeconds = %d, want 0 (startupProbe owns delay)", c0.ReadinessProbe.InitialDelaySeconds)
	}
	if c0.LivenessProbe == nil {
		t.Fatal("LivenessProbe required")
	}
	if c0.LivenessProbe.InitialDelaySeconds != 0 {
		t.Fatalf("LivenessProbe InitialDelaySeconds = %d, want 0 (startupProbe owns delay)", c0.LivenessProbe.InitialDelaySeconds)
	}
	applyPluginContainer(pod, "example.test/plugin:latest")
	if pod.Containers[0].ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("latest imagePullPolicy = %q, want Always", pod.Containers[0].ImagePullPolicy)
	}
}

// TestEffectiveInconsistentStatus: benign PASS+NA disagreements collapse; a real
// PASS/FAIL split stays INCONSISTENT.
func TestEffectiveInconsistentStatus(t *testing.T) {
	mk := func(src, mostCommon string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		ann := map[string]string{}
		if src != "" {
			ann[inconsistentSourceAnn] = src
		}
		if mostCommon != "" {
			ann[mostCommonStatusAnn] = mostCommon
		}
		u.SetAnnotations(ann)
		return u
	}
	cases := []struct {
		name, src, mostCommon, want string
	}{
		{"pass-vs-na", "cluster0-node0:PASS", "NOT-APPLICABLE", "PASS"},
		{"na-most-common-pass-source", "cluster0-node0:NOT-APPLICABLE", "PASS", "PASS"},
		// All nodes agree PASS: must not stay INCONSISTENT (uniform result).
		{"all-nodes-pass", "n0:PASS,n1:PASS,n2:PASS", "PASS", "PASS"},
		{"all-nodes-pass-no-mc", "n0:PASS,n1:PASS", "", "PASS"},
		{"all-na", "n0:NOT-APPLICABLE", "NOT-APPLICABLE", "NOT-APPLICABLE"},
		{"real-fail-split", "n0:FAIL", "PASS", "INCONSISTENT"},
		{"error-present", "n0:ERROR", "PASS", "INCONSISTENT"},
		{"unknown-with-pass", "n0:FUTURE-STATE", "PASS", "INCONSISTENT"},
		{"malformed-empty", "garbage,,:", "", "INCONSISTENT"},
		{"skip-only", "n0:SKIP", "SKIP", "NOT-APPLICABLE"},
		// No annotations at all: nothing to collapse, keep raw INCONSISTENT.
		{"empty-annotations", "", "", "INCONSISTENT"},
		// CO may emit mixed case / padded tokens; collapse is case-insensitive.
		{"case-and-space", " n0 : pass ", " not-applicable ", "PASS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveInconsistentStatus(mk(c.src, c.mostCommon)); got != c.want {
				t.Fatalf("effectiveInconsistentStatus(%q,%q) = %q, want %q", c.src, c.mostCommon, got, c.want)
			}
		})
	}
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

// TestAggregateStatusRawWaivedFailsClosed: WAIVED is the operator's SYNTHETIC
// status, assigned only to a FAIL with an active spec.waivers entry. A raw CCR
// claiming status WAIVED (tampered or a future CO status) must count as Error,
// not buy an accepted-risk slot; the console folds the same token to ERROR.
func TestAggregateStatusRawWaivedFailsClosed(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			completedSuite("baseline-cis", end),
			checkResult("p1", "baseline-cis", "PASS"),
			checkResult("forged", "baseline-cis", "WAIVED"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	p := cb.Status.Profiles[0]
	if p.Waived != 0 || p.Error != 1 {
		t.Fatalf("counts = %+v, want waived=0 error=1 (raw WAIVED must fail closed)", p)
	}
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
			checkResult("inc", "baseline-cis", "INCONSISTENT"),
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

	// Score is PASS/(PASS+FAIL); MANUAL/ERROR/INCONSISTENT/NOT-APPLICABLE are excluded.
	if cb.Status.Score == nil || *cb.Status.Score != 66 {
		t.Fatalf("score = %v, want 66", cb.Status.Score)
	}
	p := cb.Status.Profiles[0]
	if p.Pass != 2 || p.Fail != 1 || p.Manual != 1 || p.Error != 1 || p.Inconsistent != 1 || p.NotApplicable != 1 {
		t.Fatalf("profile counts = %+v", p)
	}
}

// TestAggregateStatusUnknownStatusCountsAsError: empty or future/unknown CCR
// status must land in Error, never vanish from ResultCounts.
func TestAggregateStatusUnknownStatusCountsAsError(t *testing.T) {
	scheme := testScheme(t)
	// Wrong JSON type for status (not a string): NestedString yields "" -> ERROR.
	// Use a map (JSON object) so the fake client can deep-copy the unstructured.
	wrongType := checkResult("wrong", "baseline-cis", "PASS")
	wrongType.Object["status"] = map[string]any{"phase": "DONE"}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("ok", "baseline-cis", "PASS"),
			checkResult("empty", "baseline-cis", ""),
			checkResult("future", "baseline-cis", "PENDING"),
			wrongType,
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	p := cb.Status.Profiles[0]
	if p.Pass != 1 || p.Error != 3 {
		t.Fatalf("profile counts = %+v, want Pass=1 Error=3 (unknown statuses)", p)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 100 {
		t.Fatalf("score = %v, want 100 (errors excluded from denominator)", cb.Status.Score)
	}
}

func checkResultSev(name, suite, status, sev string) *unstructured.Unstructured {
	u := checkResult(name, suite, status)
	// CO sets both .severity and the check-severity label; pin both so tests
	// cover the field-preferred path used in production.
	_ = unstructured.SetNestedField(u.Object, sev, "severity")
	labels := u.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[suiteLabel] = suite
	labels[checkSeverityLabel] = sev
	u.SetLabels(labels)
	return u
}

// TestAggregateStatusWaiverExpiry: an expired waiver no longer excludes its check
// (it counts by raw status again); an unexpired one still excludes.
func TestAggregateStatusWaiverExpiry(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("p1", "baseline-cis", "PASS"),
			checkResult("f1", "baseline-cis", "FAIL"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	past := metav1.NewTime(time.Now().Add(-time.Hour))
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "f1", ExpiresAt: &past}}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 50 {
		t.Fatalf("expired-waiver score = %v, want 50 (not excluded)", cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Waived != 0 || p.Fail != 1 {
		t.Fatalf("expired waiver should not exclude: %+v", p)
	}
	future := metav1.NewTime(time.Now().Add(time.Hour))
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "f1", ExpiresAt: &future}}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 100 {
		t.Fatalf("unexpired-waiver score = %v, want 100", cb.Status.Score)
	}
	if cb.Status.Profiles[0].Waived != 1 {
		t.Fatalf("unexpired waiver should exclude, waived=%d", cb.Status.Profiles[0].Waived)
	}

	// Boundary: an ExpiresAt at the aggregation instant counts as expired, not
	// excluded. aggregateStatus reads a now >= this captured time and the
	// predicate is !ExpiresAt.After(now), so equality (==now) is expired. This
	// is the lockstep boundary with the console waiverExpired (t <= now).
	nowBoundary := metav1.NewTime(time.Now())
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "f1", ExpiresAt: &nowBoundary}}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 50 {
		t.Fatalf("now-boundary waiver score = %v, want 50 (expired at ==now)", cb.Status.Score)
	}
	if cb.Status.Profiles[0].Waived != 0 {
		t.Fatalf("now-boundary waiver must not exclude, waived=%d", cb.Status.Profiles[0].Waived)
	}
}

// TestAggregateStatusEmptyFailName: a FAIL with empty metadata.name still tallies
// in ResultCounts but must not enter scan-diff failure lists (empty names never
// match waivers and produce useless newlyFailed/fixed entries).
func TestAggregateStatusEmptyFailName(t *testing.T) {
	scheme := testScheme(t)
	emptyName := checkResult("placeholder", "baseline-cis", "FAIL")
	emptyName.SetName("")
	suite := completedSuite("baseline-cis", time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC))
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("p1", "baseline-cis", "PASS"),
			emptyName,
			checkResult("f1", "baseline-cis", "FAIL"),
			suite,
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if p := cb.Status.Profiles[0]; p.Pass != 1 || p.Fail != 2 {
		t.Fatalf("counts = pass=%d fail=%d, want pass=1 fail=2 (empty name still tallies)", p.Pass, p.Fail)
	}
	// First completed scan: no regressions, but PreviousFailures must omit "".
	if got := cb.Status.PreviousFailures; len(got) != 1 || got[0] != "f1" {
		t.Fatalf("PreviousFailures = %v, want [f1] (empty name omitted)", got)
	}
}

// TestAggregateStatusSeverityWeighted: weighted mode weights FAILs by severity,
// so 1 high PASS (10) + 1 low FAIL (2) scores 83, vs flat 1/2 = 50.
func TestAggregateStatusSeverityWeighted(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResultSev("p1", "baseline-cis", "PASS", "high"),
			checkResultSev("f1", "baseline-cis", "FAIL", "low"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if *cb.Status.Score != 50 {
		t.Fatalf("flat score = %d, want 50", *cb.Status.Score)
	}
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if *cb.Status.Score != 83 {
		t.Fatalf("weighted score = %d, want 83", *cb.Status.Score)
	}
}

// TestCheckSeverityPrefersField: .severity is the CO typed field; the label is a
// secondary selector. A mismatch must not silently use the wrong weight source.
func TestCheckSeverityPrefersField(t *testing.T) {
	u := checkResult("x", "baseline-cis", "FAIL")
	_ = unstructured.SetNestedField(u.Object, "high", "severity")
	u.SetLabels(map[string]string{suiteLabel: "baseline-cis", checkSeverityLabel: "low"})
	if got := checkSeverity(u); got != "high" {
		t.Fatalf("checkSeverity = %q, want high (field over label)", got)
	}
	// Label-only still works for older or partial objects.
	u2 := checkResult("y", "baseline-cis", "FAIL")
	u2.SetLabels(map[string]string{checkSeverityLabel: "medium"})
	if got := checkSeverity(u2); got != "medium" {
		t.Fatalf("checkSeverity label-only = %q, want medium", got)
	}
	// Missing field and label: "unknown" (console checkSeverity lockstep).
	u3 := checkResult("z", "baseline-cis", "FAIL")
	if got := checkSeverity(u3); got != "unknown" {
		t.Fatalf("checkSeverity empty = %q, want unknown", got)
	}
}

// TestProfileBucketScoreWeighted: per-profile history must use the same mode as
// status.score so Overview cards/trends do not disagree with the headline.
func TestProfileBucketScoreWeighted(t *testing.T) {
	// high PASS (10) + low FAIL (2) => 83 weighted, 50 flat.
	w := weightedSum{pass: 10, fail: 2}
	if got := profileBucketScore(1, 1, w, baselinev1alpha1.ScoringSeverityWeighted, true); got == nil || *got != 83 {
		t.Fatalf("weighted = %v, want 83", got)
	}
	if got := profileBucketScore(1, 1, w, baselinev1alpha1.ScoringFlat, true); got == nil || *got != 50 {
		t.Fatalf("flat mode = %v, want 50", got)
	}
	if got := profileBucketScore(1, 1, w, baselinev1alpha1.ScoringSeverityWeighted, false); got == nil || *got != 50 {
		t.Fatalf("weighted without maps falls back to flat = %v, want 50", got)
	}
}

// TestAggregateStatusPoolsMultipleBenchmarks pins the score as a pooled ratio
// over every enabled benchmark, not a mean of per-profile scores. cis is 3/4
// (75%) and stig is 1/2 (50%); the pooled score is 4/6 = 66%, distinct from
// their mean (62%). Per-profile counts stay independent.
func TestAggregateStatusPoolsMultipleBenchmarks(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("c1", "baseline-cis", "PASS"),
			checkResult("c2", "baseline-cis", "PASS"),
			checkResult("c3", "baseline-cis", "PASS"),
			checkResult("c4", "baseline-cis", "FAIL"),
			checkResult("s1", "baseline-stig", "PASS"),
			checkResult("s2", "baseline-stig", "FAIL"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis", "stig"},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 66 {
		t.Fatalf("pooled score = %v, want 66", cb.Status.Score)
	}
	byKey := map[baselinev1alpha1.ProfileKey]baselinev1alpha1.ProfileStatus{}
	for _, p := range cb.Status.Profiles {
		byKey[p.Key] = p
	}
	if p := byKey["cis"]; p.Pass != 3 || p.Fail != 1 {
		t.Fatalf("cis counts = %+v, want 3/1", p)
	}
	if p := byKey["stig"]; p.Pass != 1 || p.Fail != 1 {
		t.Fatalf("stig counts = %+v, want 1/1", p)
	}
}

// TestAggregateStatusAllManualNilScore covers a completed scan whose checks are
// all MANUAL/NOT-APPLICABLE: pass+fail is zero so the score is nil (the Overview
// item reads "Not scanned"), yet the per-profile counts still record the checks.
func TestAggregateStatusAllManualNilScore(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("m1", "baseline-cis", "MANUAL"),
			checkResult("m2", "baseline-cis", "MANUAL"),
			checkResult("na", "baseline-cis", "NOT-APPLICABLE"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("score = %v, want nil for an all-MANUAL scan", *cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Manual != 2 || p.NotApplicable != 1 {
		t.Fatalf("profile counts = %+v, want 2 manual / 1 n-a", p)
	}
}

// TestAggregateStatusInfoOnlyNilScore: INFO is excluded from the score
// denominator. An INFO-only profile must leave Score nil (not 0 or 100) so the
// dashboard does not show a false success/failure color.
func TestAggregateStatusInfoOnlyNilScore(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("i1", "baseline-cis", "INFO"),
			checkResult("i2", "baseline-cis", "INFO"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("score = %v, want nil for INFO-only", *cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Info != 2 || p.Pass != 0 || p.Fail != 0 {
		t.Fatalf("profile counts = %+v, want info=2 pass=0 fail=0", p)
	}
}

// TestAggregateStatusErrorOnlyNilScore: ERROR is excluded from the score
// denominator. An ERROR-only profile must leave Score nil (not 0) so a broken
// content run is not reported as "0 / 100" failure.
func TestAggregateStatusErrorOnlyNilScore(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("e1", "baseline-cis", "ERROR"),
			checkResult("e2", "baseline-cis", "ERROR"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("score = %v, want nil for ERROR-only", *cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Error != 2 || p.Pass != 0 || p.Fail != 0 {
		t.Fatalf("profile counts = %+v, want error=2 pass=0 fail=0", p)
	}
}

// TestAggregateStatusLargeBenchmarkDominance: a large failing profile outweighs
// a small perfect one in the pooled score, while per-profile cards keep their
// own ratios. Guards against a mean-of-profiles refactor looking "mostly green".
func TestAggregateStatusLargeBenchmarkDominance(t *testing.T) {
	scheme := testScheme(t)
	objs := make([]client.Object, 0, 12)
	// Large CIS: 9 FAIL + 1 PASS = 10% per-profile.
	for i := 0; i < 9; i++ {
		objs = append(objs, checkResult(fmt.Sprintf("cis-f%d", i), "baseline-cis", "FAIL"))
	}
	objs = append(objs, checkResult("cis-p", "baseline-cis", "PASS"))
	// Small STIG: 1 PASS = 100% per-profile.
	objs = append(objs, checkResult("stig-p", "baseline-stig", "PASS"))
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis", "stig"},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	// Pooled: 2 PASS / (2 PASS + 9 FAIL) = 18%, not mean(10, 100)=55.
	if cb.Status.Score == nil || *cb.Status.Score != 18 {
		t.Fatalf("pooled score = %v, want 18 (large CIS dominates)", cb.Status.Score)
	}
	byKey := map[baselinev1alpha1.ProfileKey]baselinev1alpha1.ProfileStatus{}
	for _, p := range cb.Status.Profiles {
		byKey[p.Key] = p
	}
	if p := byKey["cis"]; p.Pass != 1 || p.Fail != 9 {
		t.Fatalf("cis counts = %+v, want 1/9", p)
	}
	if p := byKey["stig"]; p.Pass != 1 || p.Fail != 0 {
		t.Fatalf("stig counts = %+v, want 1/0", p)
	}
}

// TestAggregateStatusWaivers pins waiver semantics: a waived FAIL leaves the
// pass/fail denominator (raising the score) and is reported in the Waived bucket
// instead, so accepted risk is visible but not counted against compliance.
func TestAggregateStatusWaivers(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("p1", "baseline-cis", "PASS"),
			checkResult("f1", "baseline-cis", "FAIL"),
			checkResult("f2", "baseline-cis", "FAIL"),
		).Build(),
		Scheme: scheme,
	}
	// Without waivers: 1 pass / 2 fail = 33.
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 33 {
		t.Fatalf("baseline score = %v, want 33", cb.Status.Score)
	}

	// Waive one FAIL: 1 pass / 1 fail = 50, one Waived, fail drops to 1.
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "f2", Reason: "accepted"}}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 50 {
		t.Fatalf("waived score = %v, want 50", cb.Status.Score)
	}
	p := cb.Status.Profiles[0]
	if p.Fail != 1 || p.Waived != 1 || p.Pass != 1 {
		t.Fatalf("counts = %+v, want pass=1 fail=1 waived=1", p)
	}

	// Self-healing: a waiver on a check that is currently PASS has no effect
	// (it counts as PASS, not Waived), so a stale waiver never depresses the
	// score. Waive p1 (a PASS) in addition; score and counts stay 50/pass=1.
	cb.Spec.Waivers = append(cb.Spec.Waivers, baselinev1alpha1.WaiverEntry{Name: "p1"})
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 50 {
		t.Fatalf("score after waiving a PASS = %v, want 50 (no effect)", cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Pass != 1 || p.Waived != 1 {
		t.Fatalf("waiving a PASS changed counts: %+v", p)
	}

	// Waive every FAIL: denominator is pass/(pass+0) => 100. Foreign / empty
	// waiver names must not invent matches or change the score.
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{
		{Name: "f1"},
		{Name: "f2"},
		{Name: "not-a-real-check"},
		{Name: ""},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil || *cb.Status.Score != 100 {
		t.Fatalf("waive-all-fails score = %v, want 100", cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Pass != 1 || p.Fail != 0 || p.Waived != 2 {
		t.Fatalf("waive-all-fails counts = %+v, want pass=1 fail=0 waived=2", p)
	}
}

// TestAggregateStatusAllFailsWaived: when every FAIL is waived and there is no
// PASS, score is nil (no countable mass), not a false 0 or 100.
func TestAggregateStatusAllFailsWaived(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("f1", "baseline-cis", "FAIL"),
			checkResult("f2", "baseline-cis", "FAIL"),
		).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis"},
			Waivers:  []baselinev1alpha1.WaiverEntry{{Name: "f1"}, {Name: "f2"}},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("all-fails-waived score = %v, want nil", *cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Pass != 0 || p.Fail != 0 || p.Waived != 2 {
		t.Fatalf("all-fails-waived counts = %+v, want pass=0 fail=0 waived=2", p)
	}
}

// TestRecordHistoryRegression: when a new scan completes, newlyFailed/fixed are
// computed against the previous scan's failures, then the snapshot advances.
func TestRecordHistoryRegression(t *testing.T) {
	scheme := testScheme(t)
	suite := completedSuite("baseline-cis", time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC))
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	previousScan := metav1.NewTime(time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC))
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime: &previousScan, PreviousFailures: []string{"a", "c"},
		},
	}
	// Current scan fails a,b (a persists, b new); c was fixed.
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(90)), []string{"a", "b"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.NewlyFailed; len(got) != 1 || got[0] != "b" {
		t.Fatalf("newlyFailed = %v, want [b]", got)
	}
	if got := cb.Status.Fixed; len(got) != 1 || got[0] != "c" {
		t.Fatalf("fixed = %v, want [c]", got)
	}
	if got := cb.Status.PreviousFailures; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("previousFailures snapshot = %v, want [a b]", got)
	}
}

func TestRecordHistoryFirstScanHasNoFalseRegressions(t *testing.T) {
	scheme := testScheme(t)
	suite := completedSuite("baseline-cis", time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC))
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(50)), []string{"initial-fail"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(cb.Status.NewlyFailed) != 0 || len(cb.Status.Fixed) != 0 {
		t.Fatalf("first scan reported a regression: new=%v fixed=%v", cb.Status.NewlyFailed, cb.Status.Fixed)
	}
	if got := cb.Status.PreviousFailures; len(got) != 1 || got[0] != "initial-fail" {
		t.Fatalf("first failure snapshot = %v, want [initial-fail]", got)
	}
}

func TestRecordHistoryWaitsForEverySuiteGeneration(t *testing.T) {
	scheme := testScheme(t)
	previous := metav1.NewTime(time.Date(2026, 7, 10, 1, 10, 0, 0, time.UTC))
	cisEnd := time.Date(2026, 7, 11, 1, 1, 0, 0, time.UTC)
	oldPCIEnd := time.Date(2026, 7, 10, 1, 5, 0, 0, time.UTC)
	cis := completedSuite("baseline-cis", cisEnd.Add(-time.Minute), cisEnd)
	pci := completedSuite("baseline-pci-dss", oldPCIEnd)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cis, pci).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis", "pci-dss"},
		},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime:     &previous,
			PreviousFailures: []string{"old-fail"},
			History: []baselinev1alpha1.ScoreSnapshot{{
				Time: previous, Score: 90,
			}},
		},
	}

	// CIS has completed the new run, but PCI-DSS still reports the prior run.
	// A partial aggregate must not become a history point or regression diff.
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(40)), []string{"partial-fail"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !cb.Status.LastScanTime.Equal(&previous) || len(cb.Status.History) != 1 {
		t.Fatalf("partial suite generation advanced history: last=%v history=%+v", cb.Status.LastScanTime, cb.Status.History)
	}
	if got := cb.Status.PreviousFailures; len(got) != 1 || got[0] != "old-fail" {
		t.Fatalf("partial suite generation advanced failure baseline: %v", got)
	}

	freshPCI := u(suiteGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-pci-dss"}, freshPCI); err != nil {
		t.Fatal(err)
	}
	newPCIEnd := time.Date(2026, 7, 11, 1, 3, 0, 0, time.UTC)
	freshPCI.Object["status"] = completedSuite("baseline-pci-dss", newPCIEnd).Object["status"]
	if err := r.Update(context.Background(), freshPCI); err != nil {
		t.Fatal(err)
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(80)), []string{"final-fail"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cb.Status.LastScanTime == nil || !cb.Status.LastScanTime.Time.Equal(newPCIEnd) {
		t.Fatalf("completed suite generation lastScanTime = %v, want %v", cb.Status.LastScanTime, newPCIEnd)
	}
	if len(cb.Status.History) != 2 || cb.Status.History[1].Score != 80 {
		t.Fatalf("completed suite generation history = %+v", cb.Status.History)
	}
	if got := cb.Status.NewlyFailed; len(got) != 1 || got[0] != "final-fail" {
		t.Fatalf("newlyFailed = %v, want [final-fail]", got)
	}
	if got := cb.Status.Fixed; len(got) != 1 || got[0] != "old-fail" {
		t.Fatalf("fixed = %v, want [old-fail]", got)
	}
}

func TestRecordHistoryWaitsForEveryMemberScan(t *testing.T) {
	scheme := testScheme(t)
	previous := metav1.NewTime(time.Date(2026, 7, 10, 1, 10, 0, 0, time.UTC))
	oldEnd := time.Date(2026, 7, 10, 1, 5, 0, 0, time.UTC)
	newEnd := time.Date(2026, 7, 11, 1, 2, 0, 0, time.UTC)
	// A partial rescan can leave one old member DONE while another member has
	// completed again. Suite phase alone is therefore not a generation barrier.
	suite := completedSuite("baseline-cis", oldEnd, newEnd)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime: &previous,
			History:      []baselinev1alpha1.ScoreSnapshot{{Time: previous, Score: 90}},
		},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(50)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !cb.Status.LastScanTime.Equal(&previous) || len(cb.Status.History) != 1 {
		t.Fatalf("mixed member generations advanced history: last=%v history=%+v", cb.Status.LastScanTime, cb.Status.History)
	}

	fresh := u(suiteGVK)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: complianceNamespace, Name: "baseline-cis"}, fresh); err != nil {
		t.Fatal(err)
	}
	finalEnd := newEnd.Add(time.Minute)
	fresh.Object["status"] = completedSuite("baseline-cis", newEnd, finalEnd).Object["status"]
	if err := r.Update(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(75)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cb.Status.LastScanTime == nil || !cb.Status.LastScanTime.Time.Equal(finalEnd) {
		t.Fatalf("fully completed member generation lastScanTime = %v, want %v", cb.Status.LastScanTime, finalEnd)
	}
	if len(cb.Status.History) != 2 || cb.Status.History[1].Score != 75 {
		t.Fatalf("fully completed member generation history = %+v", cb.Status.History)
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

func TestAggregateStatusPropagatesSuiteGetError(t *testing.T) {
	scheme := testScheme(t)
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: suiteGVK.Group, Resource: "compliancesuites"},
		"baseline-cis",
		nil,
	)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(checkResult("p1", "baseline-cis", "PASS")).
			WithInterceptorFuncs(interceptor.Funcs{
				// recordHistory fetches owned suites by name (not a full List).
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok {
						gvk := u.GroupVersionKind()
						if gvk.Group == suiteGVK.Group && gvk.Kind == suiteGVK.Kind && key.Name == "baseline-cis" {
							return forbidden
						}
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err == nil {
		t.Fatal("aggregateStatus swallowed ComplianceSuite get error")
	}
}

// TestAggregateStatusCRDsMissingClearsRegressionLists: when ComplianceCheckResult
// CRDs are gone (List NoMatchError), score/history and the regression lists
// must all clear so Overview does not keep stale NewlyFailed/Fixed chips.
func TestAggregateStatusCRDsMissingClearsRegressionLists(t *testing.T) {
	scheme := testScheme(t)
	noMatch := &meta.NoKindMatchError{
		GroupKind: schema.GroupKind{Group: checkResultGVK.Group, Kind: checkResultGVK.Kind},
	}
	prev := int32(88)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					gvk := list.GetObjectKind().GroupVersionKind()
					if gvk.Group == checkResultGVK.Group && gvk.Kind == checkResultGVK.Kind+"List" {
						return noMatch
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			Score:            &prev,
			History:          []baselinev1alpha1.ScoreSnapshot{{Score: 88}},
			PreviousFailures: []string{"old-fail"},
			DiffBaseFailures: []string{"older-fail"},
			DiffBaseScanTime: &metav1.Time{Time: time.Now()},
			NewlyFailed:      []string{"new-fail"},
			Fixed:            []string{"was-fixed"},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("score = %v, want nil", *cb.Status.Score)
	}
	if len(cb.Status.History) != 0 {
		t.Fatalf("history = %v, want empty", cb.Status.History)
	}
	if cb.Status.PreviousFailures != nil {
		t.Fatalf("PreviousFailures = %v, want nil", cb.Status.PreviousFailures)
	}
	if cb.Status.DiffBaseFailures != nil || cb.Status.DiffBaseScanTime != nil {
		t.Fatalf("diff base not cleared: failures=%v time=%v", cb.Status.DiffBaseFailures, cb.Status.DiffBaseScanTime)
	}
	if cb.Status.NewlyFailed != nil {
		t.Fatalf("NewlyFailed = %v, want nil", cb.Status.NewlyFailed)
	}
	if cb.Status.Fixed != nil {
		t.Fatalf("Fixed = %v, want nil", cb.Status.Fixed)
	}
}

// TestAggregateStatusScanningDisabledClearsLiveDiff: empty profiles+tailored
// disables scanning (bindings pruned elsewhere). Score and live regression
// lists must clear so Overview/alerts do not keep stale NewlyFailed; history
// and PreviousFailures stay for re-enable continuity.
func TestAggregateStatusScanningDisabledClearsLiveDiff(t *testing.T) {
	scheme := testScheme(t)
	prev := int32(91)
	last := metav1.NewTime(time.Date(2026, 7, 10, 1, 0, 0, 0, time.UTC))
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles:         nil,
			TailoredProfiles: nil,
			Schedule:         "0 1 * * *",
		},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			Score:            &prev,
			LastScanTime:     &last,
			NextScanTime:     &metav1.Time{Time: last.Add(24 * time.Hour)},
			History:          []baselinev1alpha1.ScoreSnapshot{{Time: last, Score: 91}},
			PreviousFailures: []string{"old-fail"},
			DiffBaseFailures: []string{"older-fail"},
			DiffBaseScanTime: &last,
			NewlyFailed:      []string{"new-fail"},
			Fixed:            []string{"was-fixed"},
			Profiles: []baselinev1alpha1.ProfileStatus{
				{Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 10, Fail: 1}},
			},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score != nil {
		t.Fatalf("score = %v, want nil", *cb.Status.Score)
	}
	if cb.Status.NextScanTime != nil {
		t.Fatalf("NextScanTime = %v, want nil when scanning disabled", cb.Status.NextScanTime)
	}
	if cb.Status.NewlyFailed != nil {
		t.Fatalf("NewlyFailed = %v, want nil", cb.Status.NewlyFailed)
	}
	if cb.Status.Fixed != nil {
		t.Fatalf("Fixed = %v, want nil", cb.Status.Fixed)
	}
	// Keep history and last-scan + prior fail snapshot for re-enable.
	if len(cb.Status.History) != 1 || cb.Status.History[0].Score != 91 {
		t.Fatalf("history = %v, want preserved", cb.Status.History)
	}
	if cb.Status.LastScanTime == nil || !cb.Status.LastScanTime.Equal(&last) {
		t.Fatalf("LastScanTime = %v, want preserved", cb.Status.LastScanTime)
	}
	if got := cb.Status.PreviousFailures; len(got) != 1 || got[0] != "old-fail" {
		t.Fatalf("PreviousFailures = %v, want preserved", got)
	}
	if len(cb.Status.Profiles) != 0 {
		t.Fatalf("Profiles = %v, want empty", cb.Status.Profiles)
	}
}

func TestRecordHistoryRing(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)
	foreign := completedSuite("someone-else", time.Now().UTC())

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite, foreign).Build(),
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
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(77)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
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
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(88)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(cb.Status.History) != before {
		t.Fatalf("duplicate history append: len %d", len(cb.Status.History))
	}
	// Same endTimestamp: refresh the last snapshot score (late results).
	if cb.Status.History[29].Score != 88 {
		t.Fatalf("equal-scan score refresh = %d, want 88", cb.Status.History[29].Score)
	}
}

func TestRecordHistoryEqualScanRefreshesProfileTrends(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	last := metav1.NewTime(end)
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime: &last,
			History:      []baselinev1alpha1.ScoreSnapshot{{Time: last, Score: 50}},
			Profiles: []baselinev1alpha1.ProfileStatus{{
				Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 3, Fail: 1},
				History: []baselinev1alpha1.ScoreSnapshot{{Time: last, Score: 50}},
			}},
			TailoredProfiles: []baselinev1alpha1.TailoredProfileStatus{{
				Name: "custom", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 1, Fail: 1},
				History: []baselinev1alpha1.ScoreSnapshot{{Time: last, Score: 10}},
			}},
		},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(80)), []string{"late-fail"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.Profiles[0].History; len(got) != 1 || got[0].Score != 75 {
		t.Fatalf("built-in profile history = %+v, want refreshed score 75", got)
	}
	if got := cb.Status.TailoredProfiles[0].History; len(got) != 1 || got[0].Score != 50 {
		t.Fatalf("tailored profile history = %+v, want refreshed score 50", got)
	}
	if got := cb.Status.PreviousFailures; len(got) != 1 || got[0] != "late-fail" {
		t.Fatalf("late failure snapshot = %v, want [late-fail]", got)
	}
	if got := cb.Annotations[historyScoringModeAnn]; got != string(baselinev1alpha1.ScoringFlat) {
		t.Fatalf("history mode stamp = %q, want Flat", got)
	}
}

// New scan after a Flat -> SeverityWeighted flip must drop prior ring points so
// charts never mix formulas, then write a single fresh snapshot under the new mode.
// TestReconcilePersistsScoringModeStampThroughStatusUpdate drives the stamp
// through the FULL Reconcile + Status().Update round trip. The status-update
// response resets in-memory annotations to their stored values (the real
// apiserver ignores metadata on /status and the client decodes the response
// back into cb), so the persist guard must compare a snapshot taken BEFORE the
// update; reading cb.Annotations afterwards compares old==old, the durable
// stamp never advances, and every later scan wipes the history rings again.
func TestReconcilePersistsScoringModeStampThroughStatusUpdate(t *testing.T) {
	scheme := testScheme(t)
	previous := time.Date(2026, 7, 8, 1, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	prev := metav1.NewTime(previous)
	cb := newCB("cis")
	cb.Finalizers = []string{finalizerName}
	cb.Annotations = map[string]string{historyScoringModeAnn: string(baselinev1alpha1.ScoringFlat)}
	cb.Spec.Scoring.Mode = baselinev1alpha1.ScoringSeverityWeighted
	cb.Status = baselinev1alpha1.ClusterBaselineStatus{
		LastScanTime: &prev,
		History:      []baselinev1alpha1.ScoreSnapshot{{Time: prev, Score: 90}},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, completedSuite("baseline-cis", end), checkResult("p1", "baseline-cis", "PASS")).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	server := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, server); err != nil {
		t.Fatal(err)
	}
	if got := server.Annotations[historyScoringModeAnn]; got != string(baselinev1alpha1.ScoringSeverityWeighted) {
		t.Fatalf("durable stamp = %q, want SeverityWeighted (guard must snapshot before Status().Update)", got)
	}
	if got := server.Status.History; len(got) != 1 {
		t.Fatalf("history after mode-flip scan = %+v, want single fresh point", got)
	}
	// Second reconcile: stamp persisted, so history must NOT be cleared again.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster"}, server); err != nil {
		t.Fatal(err)
	}
	if got := server.Status.History; len(got) != 1 {
		t.Fatalf("history after second reconcile = %+v, want unchanged single point", got)
	}
}

func TestRecordHistoryNewScanClearsHistoryWhenScoringModeFlips(t *testing.T) {
	scheme := testScheme(t)
	previous := time.Date(2026, 7, 8, 1, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	prev := metav1.NewTime(previous)
	cb := &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{historyScoringModeAnn: string(baselinev1alpha1.ScoringFlat)},
		},
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis"},
			Scoring:  baselinev1alpha1.ScoringSpec{Mode: baselinev1alpha1.ScoringSeverityWeighted},
		},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime: &prev,
			History: []baselinev1alpha1.ScoreSnapshot{
				{Time: prev, Score: 90},
				{Time: prev, Score: 80},
			},
			Profiles: []baselinev1alpha1.ProfileStatus{{
				Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 9, Fail: 1},
				History: []baselinev1alpha1.ScoreSnapshot{{Time: prev, Score: 90}},
			}},
			PreviousFailures: []string{"old-fail"},
		},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(70)), []string{"new-fail"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.History; len(got) != 1 || got[0].Score != 70 {
		t.Fatalf("overall history after mode flip = %+v, want single score 70", got)
	}
	if got := cb.Status.Profiles[0].History; len(got) != 1 || got[0].Score != 90 {
		// Flat pass/fail from counts when weights nil: 9/(9+1)=90.
		t.Fatalf("profile history after mode flip = %+v, want single score 90", got)
	}
	if got := cb.Annotations[historyScoringModeAnn]; got != string(baselinev1alpha1.ScoringSeverityWeighted) {
		t.Fatalf("mode stamp = %q, want SeverityWeighted", got)
	}
}

// Waived FAILs stay in the scan-diff failure set so accepting risk is not
// reported as Fixed (score still uses the Waived bucket).
func TestAggregateStatusWaivedFailStaysInFailureDiff(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			suite,
			checkResult("p1", "baseline-cis", "PASS"),
			checkResult("f1", "baseline-cis", "FAIL"),
			checkResult("f2", "baseline-cis", "FAIL"),
		).Build(),
		Scheme: scheme,
	}
	prev := metav1.NewTime(end.Add(-24 * time.Hour))
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis"},
			Waivers:  []baselinev1alpha1.WaiverEntry{{Name: "f2", Reason: "accepted"}},
		},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime:     &prev,
			PreviousFailures: []string{"f1", "f2"},
		},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	// Score excludes waived f2: 1 pass / 1 fail = 50.
	if cb.Status.Score == nil || *cb.Status.Score != 50 {
		t.Fatalf("score = %v, want 50", cb.Status.Score)
	}
	if p := cb.Status.Profiles[0]; p.Fail != 1 || p.Waived != 1 {
		t.Fatalf("counts = %+v, want fail=1 waived=1", p)
	}
	// Diff vs previous scan: both f1 and f2 still "fail outcomes"; Fixed empty.
	if len(cb.Status.Fixed) != 0 {
		t.Fatalf("Fixed = %v, want empty (waived is not fixed)", cb.Status.Fixed)
	}
	if got := cb.Status.PreviousFailures; len(got) != 2 || got[0] != "f1" || got[1] != "f2" {
		t.Fatalf("PreviousFailures = %v, want [f1 f2] including waived", got)
	}
}

// Equal-scan late refresh must not rewrite history under a different scoring
// mode (status.score may still change in aggregateStatus; rings stay as written).
func TestRecordHistoryEqualScanSkipsHistoryWhenScoringModeFlips(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	last := metav1.NewTime(end)
	cb := &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{historyScoringModeAnn: string(baselinev1alpha1.ScoringFlat)},
		},
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis"},
			Scoring:  baselinev1alpha1.ScoringSpec{Mode: baselinev1alpha1.ScoringSeverityWeighted},
		},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime: &last,
			History:      []baselinev1alpha1.ScoreSnapshot{{Time: last, Score: 50}},
			Profiles: []baselinev1alpha1.ProfileStatus{{
				Key: "cis", ResultCounts: baselinev1alpha1.ResultCounts{Pass: 3, Fail: 1},
				History: []baselinev1alpha1.ScoreSnapshot{{Time: last, Score: 50}},
			}},
		},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(90)), []string{"late-fail"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.History; len(got) != 1 || got[0].Score != 50 {
		t.Fatalf("overall history rewritten under new mode: %+v", got)
	}
	if got := cb.Status.Profiles[0].History; len(got) != 1 || got[0].Score != 50 {
		t.Fatalf("profile history rewritten under new mode: %+v", got)
	}
	// Failure baseline still advances so the next scan's diff stays correct.
	if got := cb.Status.PreviousFailures; len(got) != 1 || got[0] != "late-fail" {
		t.Fatalf("late failure snapshot = %v, want [late-fail]", got)
	}
	if got := cb.Annotations[historyScoringModeAnn]; got != string(baselinev1alpha1.ScoringFlat) {
		t.Fatalf("mode stamp should stay Flat until a new scan writes history, got %q", got)
	}
}

func TestRecordHistoryEqualScanCorrectsLateFailureDiff(t *testing.T) {
	scheme := testScheme(t)
	previous := metav1.NewTime(time.Date(2026, 7, 8, 1, 0, 0, 0, time.UTC))
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime:     &previous,
			PreviousFailures: []string{"fixed", "persistent"},
		},
	}
	// The suite event arrives while only part of the new result set is visible.
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(90)), []string{"persistent"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.Fixed; len(got) != 1 || got[0] != "fixed" {
		t.Fatalf("initial fixed = %v, want [fixed]", got)
	}
	// A late CheckResult event for the same completed suite must recompute against
	// the prior scan, not against the first partial view of this scan.
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(80)), []string{"late", "persistent"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := cb.Status.NewlyFailed; len(got) != 1 || got[0] != "late" {
		t.Fatalf("late newlyFailed = %v, want [late]", got)
	}
	if got := cb.Status.Fixed; len(got) != 1 || got[0] != "fixed" {
		t.Fatalf("late fixed = %v, want [fixed]", got)
	}
	if got := cb.Status.PreviousFailures; len(got) != 2 || got[0] != "late" || got[1] != "persistent" {
		t.Fatalf("late failure snapshot = %v", got)
	}
}

func TestRecordHistoryNoOwnedSuites(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(50)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cb.Status.LastScanTime != nil || len(cb.Status.History) != 0 {
		t.Fatalf("expected no history, got last=%v hist=%v", cb.Status.LastScanTime, cb.Status.History)
	}
}

func TestRecordHistoryDoesNotRewind(t *testing.T) {
	scheme := testScheme(t)
	// Only an older owned suite remains after the newer suite was removed.
	older := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", older)

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	newer := metav1.NewTime(time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC))
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			LastScanTime: &newer,
			History: []baselinev1alpha1.ScoreSnapshot{
				{Time: newer, Score: 90},
			},
		},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(10)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !cb.Status.LastScanTime.Equal(&newer) {
		t.Fatalf("LastScanTime rewound to %v", cb.Status.LastScanTime)
	}
	if len(cb.Status.History) != 1 || cb.Status.History[0].Score != 90 {
		t.Fatalf("history mutated on rewind path: %+v", cb.Status.History)
	}
}

func TestRecordHistoryIgnoresFarFutureEndTimestamp(t *testing.T) {
	scheme := testScheme(t)
	future := time.Now().UTC().Add(48 * time.Hour)
	suite := completedSuite("baseline-cis", future)

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(50)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cb.Status.LastScanTime != nil || len(cb.Status.History) != 0 {
		t.Fatalf("far-future endTimestamp must not set history: last=%v hist=%v",
			cb.Status.LastScanTime, cb.Status.History)
	}
}

func TestRecordHistoryAppendsWhenScoreAppearsLater(t *testing.T) {
	scheme := testScheme(t)
	end := time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)
	suite := completedSuite("baseline-cis", end)

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(suite).Build(),
		Scheme: scheme,
	}
	last := metav1.NewTime(end)
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
		Status: baselinev1alpha1.ClusterBaselineStatus{
			// Prior reconcile saw the scan complete but score was nil (all MANUAL).
			LastScanTime: &last,
		},
	}
	if err := r.recordHistory(context.Background(), cb, ptr.To(int32(80)), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(cb.Status.History) != 1 || cb.Status.History[0].Score != 80 {
		t.Fatalf("expected first history point for equal endTimestamp, got %+v", cb.Status.History)
	}
	if !cb.Status.LastScanTime.Equal(&last) {
		t.Fatalf("LastScanTime changed: %v", cb.Status.LastScanTime)
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
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "CSVFailed" {
		t.Fatalf("condition = %+v, want False/CSVFailed", c)
	}
	if cb.Status.ComplianceOperatorVersion != "" {
		t.Fatalf("version must stay empty on Failed, got %q", cb.Status.ComplianceOperatorVersion)
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

// When the Console capability is disabled the console CRDs are absent; the
// teardown paths must tolerate NoKindMatch so the CR is not wedged. The fake
// client fabricates unknown kinds, so interceptors inject the real
// NoKindMatchError a live RESTMapper produces for a missing CRD.
func TestConsoleTeardownToleratesMissingCRDs(t *testing.T) {
	scheme := testScheme(t)
	noMatch := func(gvk schema.GroupVersionKind) error {
		if gvk.Group == "console.openshift.io" || gvk.Group == "operator.openshift.io" {
			return &meta.NoKindMatchError{GroupKind: gvk.GroupKind()}
		}
		return nil
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := noMatch(obj.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if err := noMatch(obj.GetObjectKind().GroupVersionKind()); err != nil {
						return err
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.deregisterConsolePlugin(context.Background()); err != nil {
		t.Fatalf("deregisterConsolePlugin should tolerate missing Console CRD: %v", err)
	}
	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.removeConsolePlugin(context.Background(), cb); err != nil {
		t.Fatalf("removeConsolePlugin should tolerate missing ConsolePlugin CRD: %v", err)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady"); c == nil || c.Reason != "Disabled" {
		t.Fatalf("ConsolePluginReady = %+v, want Disabled", c)
	}
}
