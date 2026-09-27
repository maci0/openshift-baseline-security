package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// scriptedSyncCache stands in for cache.Cache in the readyz check: only
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

// The Deployment ships 2 replicas with leader election on, so the flag that
// fails readyz on SIGTERM has to start on a standby too. A bare
// manager.RunnableFunc is filed in controller-runtime's leader-election group
// and never starts there, which left a terminating standby reporting Ready for
// the whole drain.
func TestShutdownFlagRunsWithoutLeadership(t *testing.T) {
	shuttingDown.Store(false)
	t.Cleanup(func() { shuttingDown.Store(false) })

	r := &shutdownFlag{}
	if r.NeedLeaderElection() {
		t.Fatal("shutdown flag must start on every replica, not only the leader")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()

	// Still serving before SIGTERM.
	select {
	case err := <-done:
		t.Fatalf("Start returned %v before the context was cancelled", err)
	case <-time.After(50 * time.Millisecond):
	}
	if shuttingDown.Load() {
		t.Fatal("flag flipped before the manager context was cancelled")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start = %v, want nil on graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the manager context was cancelled")
	}
	if !shuttingDown.Load() {
		t.Fatal("shuttingDown must be set once the manager context is cancelled")
	}
}

func TestCacheSyncReadyz(t *testing.T) {
	// Package global: reset so a failure in one case cannot leak into another.
	shuttingDown.Store(false)
	t.Cleanup(func() { shuttingDown.Store(false) })

	// Not synced yet: not ready, so kubelet does not route to a pod whose
	// informers are still empty.
	c := &scriptedSyncCache{syncedAfter: 2}
	check := cacheSyncReadyz(c)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/readyz", nil)
	if err := check(req); err == nil {
		t.Fatal("readyz before cache sync = nil, want error")
	}
	if err := check(req); err != nil {
		t.Fatalf("readyz after cache sync = %v, want nil", err)
	}

	// SIGTERM: caches still synced, but the pod is draining, so readiness
	// fails instead of keeping the pod in the Service endpoints.
	shuttingDown.Store(true)
	if err := check(req); !errors.Is(err, errShuttingDown) {
		t.Fatalf("readyz while shutting down = %v, want %v", err, errShuttingDown)
	}
}
