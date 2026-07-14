package main

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

// defaultClusterBaseline creates ClusterBaseline/cluster once when none exist.
// NeedLeaderElection keeps HA replicas from racing the create.
type defaultClusterBaseline struct {
	Client client.Client
	Cache  cache.Cache
	Log    logr.Logger
}

func (d *defaultClusterBaseline) Start(ctx context.Context) error {
	if !d.Cache.WaitForCacheSync(ctx) {
		// Shutdown is normal (ctx cancelled). A live context with failed sync is
		// unexpected and would leave the cluster without the zero-config CR.
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		d.Log.Error(errCacheNotSynced, "cache did not sync; skipping default ClusterBaseline creation")
		return nil
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
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
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

func (d *defaultClusterBaseline) ensureOnce(ctx context.Context) error {
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

func (*defaultClusterBaseline) NeedLeaderElection() bool { return true }
