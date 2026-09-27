// Bootstrap layer: the manager Runnable that creates ClusterBaseline/cluster
// when the cluster has none, so a fresh install reaches a CIS scan with no
// CR the operator has to be told about. It lives here, not in cmd, because it
// is a manager Runnable reconciling a ClusterBaseline, the same role
// lazyComplianceWatch plays in clusterbaseline_controller.go.
//
// Not part of Reconcile: this is one-shot provisioning, not steady-state
// convergence, so it runs once per process under leader election rather than
// on every watch event.
package controller

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// errCacheNotSynced is logged when WaitForCacheSync fails with a live context
// (not shutdown). Keeps Error() from receiving a nil error value.
var errCacheNotSynced = errors.New("cache did not sync")

// defaultCRRetryDelay is the pause between ensure attempts and between cache
// sync attempts.
const defaultCRRetryDelay = 10 * time.Second

// DefaultClusterBaseline creates ClusterBaseline/cluster once when none exist.
// NeedLeaderElection keeps HA replicas from racing the create.
type DefaultClusterBaseline struct {
	Client client.Client
	Cache  cache.Cache
	Log    logr.Logger

	// Clock carries the retry waits between attempts. Production nil (real
	// time); a simulated run injects a virtual clock so the loop advances
	// simulated time instead of blocking on it.
	Clock clock
	// waitForCacheSync overrides Cache.WaitForCacheSync (tests only) so the
	// sync-retry loop can be driven without manager machinery. Production nil.
	waitForCacheSync func(ctx context.Context) bool
	// retryDelay is the pause between attempts; zero means defaultCRRetryDelay.
	// Test knob: keeps retry-loop tests off the 10s wall clock.
	retryDelay time.Duration
}

func (d *DefaultClusterBaseline) delay() time.Duration {
	if d.retryDelay > 0 {
		return d.retryDelay
	}
	return defaultCRRetryDelay
}

// sleep waits out the inter-attempt pause on the injected clock.
func (d *DefaultClusterBaseline) sleep(ctx context.Context) error {
	if d.Clock == nil {
		return realClock{}.Sleep(ctx, d.delay())
	}
	return d.Clock.Sleep(ctx, d.delay())
}

func (d *DefaultClusterBaseline) Start(ctx context.Context) error {
	// Retry cache sync while the context stays live: a false return without
	// shutdown means informers were not ready yet (or failed transiently).
	// Giving up here would leave the cluster without the zero-config CR until
	// process restart even though reconciles resume once the cache recovers
	// (readyz re-evaluates every probe). Rate-limit Error logs like the ensure
	// loop below.
	var syncAttempt int
	for !d.waitForCacheSyncFn()(ctx) {
		if ctx.Err() != nil {
			// Shutdown is normal: a Runnable returning ctx.Err() is fatal to the manager.
			return nil //nolint:nilerr // graceful shutdown, not a runtime failure
		}
		syncAttempt++
		if syncAttempt == 1 || syncAttempt%6 == 0 {
			d.Log.Error(errCacheNotSynced,
				"cache did not sync; deferring default ClusterBaseline creation",
				"syncAttempt", syncAttempt)
		}
		if err := d.sleep(ctx); err != nil {
			return nil //nolint:nilerr // graceful shutdown, not a runtime failure
		}
	}
	// Retry on transient list/create failures so a brief API blip does not
	// leave the cluster without the zero-config CR until restart. Permanent
	// auth failures stop immediately: retrying Forbidden forever only spams
	// logs and cannot succeed until RBAC is fixed and the pod restarts.
	// Rate-limit Error logs (first failure, then every ~1m) so a sticky API
	// outage does not fill the log stream every 10s with the same stack.
	var attempt int
	for {
		err := d.ensureOnce(ctx)
		if err == nil {
			return nil
		}
		if isPermanentDefaultCRError(err) {
			d.Log.Error(err, "permanent error creating default ClusterBaseline; not retrying")
			return nil
		}
		attempt++
		if attempt == 1 || attempt%6 == 0 {
			d.Log.Error(err, "default ClusterBaseline ensure failed; will retry",
				"attempt", attempt)
		}
		if err := d.sleep(ctx); err != nil {
			return nil //nolint:nilerr // graceful shutdown, not a runtime failure
		}
	}
}

// waitForCacheSyncFn resolves the sync check: the test override when set, else
// the real cache.
func (d *DefaultClusterBaseline) waitForCacheSyncFn() func(ctx context.Context) bool {
	if d.waitForCacheSync != nil {
		return d.waitForCacheSync
	}
	return d.Cache.WaitForCacheSync
}

// isPermanentDefaultCRError is true for auth/RBAC failures that will not clear
// without a config change (and usually a process restart to re-read SA tokens).
func isPermanentDefaultCRError(err error) bool {
	// Invalid (422) is deterministic: the shipped default spec is provably valid,
	// so a rejection means an admission webhook (Kyverno/Gatekeeper) refuses it.
	// Retrying the identical object every 10s forever cannot succeed, so give up
	// (log once) instead of spinning until an admin changes the policy.
	return apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsInvalid(err)
}

func (d *DefaultClusterBaseline) ensureOnce(ctx context.Context) error {
	list := &baselinev1alpha1.ClusterBaselineList{}
	if err := d.Client.List(ctx, list); err != nil {
		// Caller (Start) rate-limits Error logs on retries; avoid double-logging.
		return err
	}
	if len(list.Items) > 0 {
		return nil
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{baselinev1alpha1.ProfileCIS}},
	}
	err := d.Client.Create(ctx, cb)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	if err == nil {
		d.Log.Info("created default ClusterBaseline", "name", "cluster", "profiles", []string{string(baselinev1alpha1.ProfileCIS)})
	}
	return nil
}

func (*DefaultClusterBaseline) NeedLeaderElection() bool { return true }
