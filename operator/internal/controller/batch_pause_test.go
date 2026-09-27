package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// The setMCPPaused guards below exist so an untrusted pool name or an absent
// MachineConfigPool CRD cannot wedge a batch. Each one is a distinct failure
// mode: a returned error sticky-Degrades batch start (annotation kept, no grace,
// no resume), so a skip that regressed into an error would freeze the batch.

// A pool name arrives from a remediation label or a scan-name suffix. One that
// is not DNS-1123 must be skipped, not Get: a non-NotFound Get error would fail
// the whole batch start for every remediation sharing that name.
func TestSetMCPPausedSkipsInvalidPoolName(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	for _, name := range []string{"Not_Valid", "has/slash", "-leading-dash", "trailing-", "UPPER"} {
		if err := r.setMCPPaused(context.Background(), name, true, batchPauseOwner(cb)); err != nil {
			t.Fatalf("invalid pool %q must skip, got %v", name, err)
		}
		// Same guard on the resume side: an unowned marker must not resume either.
		if err := r.setMCPPaused(context.Background(), name, false, batchPauseOwner(cb)); err != nil {
			t.Fatalf("invalid pool %q resume must skip, got %v", name, err)
		}
	}
	// An empty name is a no-op before the DNS check.
	if err := r.setMCPPaused(context.Background(), "", true, batchPauseOwner(cb)); err != nil {
		t.Fatalf("empty pool name must be a no-op, got %v", err)
	}
}

// A missing pool, or an absent MCP CRD, must not wedge the batch: Compliance
// Operator installs mid-life and a stale pool name outlives the pool object.
func TestSetMCPPausedMissingPoolSkips(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	// No objects at all: Get returns NotFound.
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	if err := r.setMCPPaused(context.Background(), "worker", true, batchPauseOwner(cb)); err != nil {
		t.Fatalf("missing pool must skip, got %v", err)
	}
	// NoKindMatchError (MCP CRD not installed) takes the same path.
	noMatch := &meta.NoKindMatchError{
		GroupKind: schema.GroupKind{Group: mcpGVK.Group, Kind: mcpGVK.Kind},
	}
	r2 := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return noMatch
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r2.setMCPPaused(context.Background(), "worker", true, batchPauseOwner(cb)); err != nil {
		t.Fatalf("absent MCP CRD must skip, got %v", err)
	}
	// Any other Get error is a real API failure and must propagate so the
	// batch retries with backoff instead of silently reporting progress.
	boom := apierrors.NewServiceUnavailable("apiserver blip")
	r3 := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return boom
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r3.setMCPPaused(context.Background(), "worker", true, batchPauseOwner(cb)); err == nil {
		t.Fatal("a non-NotFound Get failure must propagate, not be swallowed as a skip")
	}
}

// Pausing without an owner would leave the pool paused with no marker, so the
// resume path could never undo it. That is a hard error, unlike every other
// skip in this function.
func TestSetMCPPausedEmptyOwnerErrors(t *testing.T) {
	scheme := testScheme(t)
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(machineConfigPool("worker")).Build(),
		Scheme: scheme,
	}
	if err := r.setMCPPaused(context.Background(), "worker", true, ""); err == nil {
		t.Fatal("pausing with an empty owner must fail; the pool could never be resumed")
	}
	// The pool must be untouched: no partial pause written before the check.
	got := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
		t.Fatal("pool must not be paused when the owner check fails")
	}
	if got.GetAnnotations()[batchPauseOwnerAnnotation] != "" {
		t.Fatalf("no pause marker may be written, got %q", got.GetAnnotations()[batchPauseOwnerAnnotation])
	}
}

// Resume only unpauses a pool this batch owns. A foreign marker, or an
// unmarked administrator pause, must be left exactly as found.
func TestSetMCPPausedResumeLeavesUnownedPoolsAlone(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	owner := batchPauseOwner(cb)

	t.Run("foreign marker", func(t *testing.T) {
		pool := machineConfigPool("worker")
		_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
		pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: "another-owner"})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
			Scheme: scheme,
		}
		if err := r.setMCPPaused(context.Background(), "worker", false, owner); err != nil {
			t.Fatalf("foreign-owned resume must skip, got %v", err)
		}
		got := machineConfigPool("worker")
		if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
			t.Fatal(err)
		}
		if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); !paused {
			t.Fatal("a pool paused by another owner must stay paused")
		}
		if got.GetAnnotations()[batchPauseOwnerAnnotation] != "another-owner" {
			t.Fatalf("foreign marker must survive, got %q", got.GetAnnotations()[batchPauseOwnerAnnotation])
		}
	})

	t.Run("administrator pause with no marker", func(t *testing.T) {
		pool := machineConfigPool("worker")
		_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
			Scheme: scheme,
		}
		if err := r.setMCPPaused(context.Background(), "worker", false, owner); err != nil {
			t.Fatalf("unmarked admin pause must skip, got %v", err)
		}
		got := machineConfigPool("worker")
		if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
			t.Fatal(err)
		}
		if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); !paused {
			t.Fatal("an administrator pause must never be undone by the resume path")
		}
		if len(got.GetAnnotations()) != 0 {
			t.Fatalf("resume must not write annotations on a pool it does not own: %v", got.GetAnnotations())
		}
	})

	// A pool this batch owns must resume: marker dropped and unpaused. A
	// regression here strands nodes paused through an entire completed batch.
	t.Run("owned pool resumes and drops the marker", func(t *testing.T) {
		pool := machineConfigPool("worker")
		_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
		pool.SetAnnotations(map[string]string{
			batchPauseOwnerAnnotation: owner,
			"unrelated":               "keep-me",
		})
		r := &ClusterBaselineReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build(),
			Scheme: scheme,
		}
		if err := r.setMCPPaused(context.Background(), "worker", false, owner); err != nil {
			t.Fatalf("owned resume must succeed, got %v", err)
		}
		got := machineConfigPool("worker")
		if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
			t.Fatal(err)
		}
		if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
			t.Fatal("owned pool must be unpaused")
		}
		if _, still := got.GetAnnotations()[batchPauseOwnerAnnotation]; still {
			t.Fatal("pause marker must be dropped on resume")
		}
		if got.GetAnnotations()["unrelated"] != "keep-me" {
			t.Fatalf("unrelated annotations must survive: %v", got.GetAnnotations())
		}
	})
}

// Pool rediscovery reads every remediation named in the batch-apply annotation
// through one paged List, not a Get per name. The finalizer blocks CR deletion,
// so a per-name round trip made recovery latency scale with the batch size (up
// to 256 sequential live apiserver calls). The pools must still be recovered
// from the same names, so assert both the call shape and the outcome.
func TestResumeBatchPoolsOnDeleteListsRemediationsOnce(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1,rem2,rem3"})
	objs := []client.Object{cb}
	for i, pool := range []string{"worker", "master", "infra"} {
		rem := nodeRemediation(fmt.Sprintf("rem%d", i+1), pool)
		objs = append(objs, rem)
		mcp := machineConfigPool(pool)
		_ = unstructured.SetNestedField(mcp.Object, true, "spec", "paused")
		mcp.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: batchPauseOwner(cb)})
		objs = append(objs, mcp)
	}
	lists, gets := 0, 0
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(objs...).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if u, ok := list.(*unstructured.UnstructuredList); ok &&
						u.GroupVersionKind() == remediationGVK.GroupVersion().WithKind(remediationGVK.Kind+"List") {
						lists++
					}
					return c.List(ctx, list, opts...)
				},
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok && u.GroupVersionKind() == remediationGVK {
						gets++
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.resumeBatchPoolsOnDelete(context.Background(), cb); err != nil {
		t.Fatalf("pool recovery must not fail: %v", err)
	}
	if gets != 0 {
		t.Errorf("pool recovery issued %d remediation Gets; it must use the paged List", gets)
	}
	if lists == 0 {
		t.Error("pool recovery never listed remediations, so it resolved no pools")
	}
	for _, pool := range []string{"worker", "master", "infra"} {
		got := machineConfigPool(pool)
		if err := r.Get(context.Background(), types.NamespacedName{Name: pool}, got); err != nil {
			t.Fatal(err)
		}
		if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
			t.Errorf("MachineConfigPool %q was discovered but never resumed", pool)
		}
	}
}

// A remediation named in the batch-apply annotation that no longer exists must
// not block the finalizer. Race-deleted mid-batch is normal: the batch is
// already done for that name and the remaining pools still need resuming.
func TestResumeBatchPoolsOnDeleteSkipsMissingRemediation(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "gone,rem1"})
	rem := nodeRemediation("rem1", "worker")
	pool := machineConfigPool("worker")
	_ = unstructured.SetNestedField(pool.Object, true, "spec", "paused")
	pool.SetAnnotations(map[string]string{batchPauseOwnerAnnotation: batchPauseOwner(cb)})
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem, pool).Build(),
		Scheme: scheme,
	}
	// "gone" has no object: NotFound is a skip, not a wedge.
	if err := r.resumeBatchPoolsOnDelete(context.Background(), cb); err != nil {
		t.Fatalf("a deleted remediation must not block deletion: %v", err)
	}
	got := machineConfigPool("worker")
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker"}, got); err != nil {
		t.Fatal(err)
	}
	if paused, _, _ := unstructured.NestedBool(got.Object, "spec", "paused"); paused {
		t.Fatal("the valid remediation's pool must still resume")
	}
}

// A real API failure (not NotFound/NoMatch) must block finalizer removal, so
// the delete retries with backoff rather than dropping the finalizer with pools
// still paused. Dropping it is unrecoverable: nothing would ever unpause them.
func TestResumeBatchPoolsOnDeleteBlocksOnAPIFailure(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	cb.SetAnnotations(map[string]string{batchApplyAnnotation: "rem1"})
	rem := nodeRemediation("rem1", "worker")
	boom := apierrors.NewServiceUnavailable("apiserver blip")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb, rem).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if u, ok := list.(*unstructured.UnstructuredList); ok &&
						u.GroupVersionKind() == remediationGVK.GroupVersion().WithKind(remediationGVK.Kind+"List") {
						return boom
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.resumeBatchPoolsOnDelete(context.Background(), cb); err == nil {
		t.Fatal("a non-NotFound remediation read failure must block finalizer removal, not be swallowed")
	}
}

// A pool that cannot be resumed on delete must also block finalizer removal:
// dropping the finalizer with a pool still paused strands its nodes until
// someone notices the label by hand.
func TestResumeBatchPoolsOnDeleteBlocksOnPoolResumeFailure(t *testing.T) {
	scheme := testScheme(t)
	cb := newBatchCB()
	now := metav1.Now()
	cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
		Phase:      "Applying",
		Pools:      []string{"worker"},
		PauseOwner: batchPauseOwner(cb),
		StartedAt:  now,
	}
	boom := apierrors.NewServiceUnavailable("apiserver blip")
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if u, ok := obj.(*unstructured.Unstructured); ok && u.GroupVersionKind() == mcpGVK {
						return boom
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	if err := r.resumeBatchPoolsOnDelete(context.Background(), cb); err == nil {
		t.Fatal("a failed pool resume must block finalizer removal")
	}
}

// Compliance CRDs uninstalled mid-batch must be distinguishable from a
// remediation that individually 404s. Both take the same terminal path (every
// name counts as done, so the pools are released rather than held to the grace
// deadline), but the batch is about to be finished as reason=applied: on-call
// needs the CRD-gone cause in the log, not N copies of "notFound".
func TestListRemediationsForBatchReportsCRDsAbsent(t *testing.T) {
	scheme := testScheme(t)
	noMatch := &meta.NoKindMatchError{
		GroupKind: schema.GroupKind{Group: remediationGVK.Group, Kind: remediationGVK.Kind},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if gvk := list.GetObjectKind().GroupVersionKind(); gvk.Kind == remediationGVK.Kind+"List" {
						return noMatch
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Scheme: scheme,
	}
	got, err := r.listRemediationsForBatch(context.Background(), []string{"fix-a"})
	if !errors.Is(err, errComplianceCRDsAbsent) {
		t.Fatalf("err = %v, want errComplianceCRDsAbsent", err)
	}
	if got != nil {
		t.Fatalf("rems = %v, want nil so every name takes the terminal path", got)
	}

	// A transient failure must stay distinct: it holds the pools until grace.
	boom := apierrors.NewServiceUnavailable("apiserver blip")
	r.Client = fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if gvk := list.GetObjectKind().GroupVersionKind(); gvk.Kind == remediationGVK.Kind+"List" {
					return boom
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()
	_, err = r.listRemediationsForBatch(context.Background(), []string{"fix-a"})
	if err == nil || errors.Is(err, errComplianceCRDsAbsent) {
		t.Fatalf("transient err = %v, want a wrapped apiserver error, not the CRD sentinel", err)
	}
	if !strings.Contains(err.Error(), complianceNamespace) {
		t.Fatalf("transient err = %q, want the namespace in the wrap", err)
	}
}
