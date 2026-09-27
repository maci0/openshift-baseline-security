package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

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
