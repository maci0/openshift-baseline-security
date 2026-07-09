package main

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

// defaultClusterBaseline creates ClusterBaseline/cluster once when none exist.
// NeedLeaderElection keeps HA replicas from racing the create.
type defaultClusterBaseline struct {
	Client client.Client
	Cache  cache.Cache
	Log    logr.Logger
}

func (d *defaultClusterBaseline) Start(ctx context.Context) error {
	if !d.Cache.WaitForCacheSync(ctx) {
		return nil
	}
	// Retry on transient list/create failures so a brief API blip does not
	// leave the cluster without the zero-config CR until restart.
	for {
		if err := d.ensureOnce(ctx); err == nil {
			return nil
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

func (d *defaultClusterBaseline) ensureOnce(ctx context.Context) error {
	list := &baselinev1alpha1.ClusterBaselineList{}
	if err := d.Client.List(ctx, list); err != nil {
		d.Log.Error(err, "listing ClusterBaselines for default creation")
		return err
	}
	if len(list.Items) > 0 {
		return nil
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := d.Client.Create(ctx, cb); err != nil && !apierrors.IsAlreadyExists(err) {
		d.Log.Error(err, "creating default ClusterBaseline")
		return err
	}
	return nil
}

func (*defaultClusterBaseline) NeedLeaderElection() bool { return true }
