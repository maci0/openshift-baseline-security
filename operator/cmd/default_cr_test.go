package main

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// scriptedSyncCache stands in for cache.Cache in Start tests: only
// WaitForCacheSync runs; everything else would panic (and fail the test loudly).
type scriptedSyncCache struct {
	cache.Cache
	calls int
	// syncedAfter is the call number on which sync reports success.
	syncedAfter int
}

func (c *scriptedSyncCache) WaitForCacheSync(context.Context) bool {
	c.calls++
	return c.calls >= c.syncedAfter
}

func crScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := baselinev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// ensureOnce creates the singleton when none exist.
func TestEnsureOnceCreatesWhenEmpty(t *testing.T) {
	s := crScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	d := &defaultClusterBaseline{Client: c, Log: logr.Discard()}
	if err := d.ensureOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "cluster"}, got); err != nil {
		t.Fatalf("default CR not created: %v", err)
	}
	if len(got.Spec.Profiles) != 1 || got.Spec.Profiles[0] != "cis" {
		t.Fatalf("default profiles = %v, want [cis]", got.Spec.Profiles)
	}
}

// ensureOnce must not create (or fight) when a ClusterBaseline already exists,
// even a user-renamed or user-edited one.
func TestEnsureOnceNoopWhenPresent(t *testing.T) {
	s := crScheme(t)
	existing := &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"stig"}},
	}
	created := 0
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				created++
				return cl.Create(ctx, obj, opts...)
			},
		}).Build()
	d := &defaultClusterBaseline{Client: c, Log: logr.Discard()}
	if err := d.ensureOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("ensureOnce created a CR though one exists (%d creates)", created)
	}
	// User's profile choice is untouched.
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "cluster"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Profiles[0] != "stig" {
		t.Fatalf("existing CR mutated: %v", got.Spec.Profiles)
	}
}

// A create losing the race to another replica (AlreadyExists) is tolerated.
func TestEnsureOnceToleratesAlreadyExists(t *testing.T) {
	s := crScheme(t)
	gvr := schema.GroupResource{Group: "baselinesecurity.openshift.io", Resource: "clusterbaselines"}
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				return apierrors.NewAlreadyExists(gvr, "cluster")
			},
		}).Build()
	d := &defaultClusterBaseline{Client: c, Log: logr.Discard()}
	if err := d.ensureOnce(context.Background()); err != nil {
		t.Fatalf("AlreadyExists must be tolerated, got %v", err)
	}
}

// A transient List failure surfaces so Start retries instead of silently
// leaving the cluster without the zero-config CR.
func TestEnsureOnceListErrorPropagates(t *testing.T) {
	s := crScheme(t)
	boom := apierrors.NewServiceUnavailable("apiserver down")
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return boom
			},
		}).Build()
	d := &defaultClusterBaseline{Client: c, Log: logr.Discard()}
	if err := d.ensureOnce(context.Background()); err == nil {
		t.Fatal("list error must propagate so Start retries")
	}
}

func TestIsPermanentDefaultCRError(t *testing.T) {
	gvr := schema.GroupResource{Group: "baselinesecurity.openshift.io", Resource: "clusterbaselines"}
	if !isPermanentDefaultCRError(apierrors.NewForbidden(gvr, "cluster", nil)) {
		t.Fatal("Forbidden must be permanent")
	}
	if !isPermanentDefaultCRError(apierrors.NewUnauthorized("no token")) {
		t.Fatal("Unauthorized must be permanent")
	}
	// Invalid (422, e.g. an admission webhook rejecting the CR) is deterministic:
	// retrying the identical object cannot succeed, so treat it as permanent.
	if !isPermanentDefaultCRError(apierrors.NewInvalid(
		schema.GroupKind{Group: gvr.Group, Kind: "ClusterBaseline"}, "cluster", nil)) {
		t.Fatal("Invalid must be permanent")
	}
	if isPermanentDefaultCRError(apierrors.NewServiceUnavailable("blip")) {
		t.Fatal("ServiceUnavailable must not be permanent")
	}
	if isPermanentDefaultCRError(apierrors.NewAlreadyExists(gvr, "cluster")) {
		t.Fatal("AlreadyExists must not be permanent")
	}
}

// A cache that syncs late (false returns with a live context) must not end the
// runnable permanently: Start keeps polling and creates the CR once synced.
func TestStartRetriesWhenCacheSyncsLate(t *testing.T) {
	s := crScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	d := &defaultClusterBaseline{
		Client: c,
		Cache:  &scriptedSyncCache{syncedAfter: 3},
		Log:    logr.Discard(),
		// Shrink the inter-attempt pause so the test stays off the wall clock.
		retryDelay: time.Millisecond,
	}
	done := make(chan error, 1)
	go func() { done <- d.Start(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not finish after late cache sync")
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "cluster"}, got); err != nil {
		t.Fatalf("default CR missing after cache recovered: %v", err)
	}
	cacheUsed := d.Cache.(*scriptedSyncCache)
	if cacheUsed.calls < 3 {
		t.Fatalf("WaitForCacheSync calls = %d, want >= 3 (must poll past failures)", cacheUsed.calls)
	}
}

// A cancelled context during the sync-retry loop exits cleanly instead of
// spinning or erroring.
func TestStartStopsOnShutdownDuringSyncRetry(t *testing.T) {
	s := crScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	ctx, cancel := context.WithCancel(context.Background())
	d := &defaultClusterBaseline{
		Client: c,
		Cache:  &scriptedSyncCache{}, // never syncs
		Log:    logr.Discard(),
		waitForCacheSync: func(context.Context) bool {
			cancel() // shutdown arrives while a failed sync would otherwise loop
			return false
		},
		retryDelay: time.Millisecond,
	}
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start after shutdown: %v", err)
	}
	list := &baselinev1alpha1.ClusterBaselineList{}
	if err := c.List(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatal("no CR should be created on shutdown before sync")
	}
}
