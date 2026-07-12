package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// checkScanStorage sets the ScanStorageReady detail condition false when owned
// scan PVCs stay Pending (no default StorageClass); the Degraded rollup
// propagates it. Listing in a nonexistent namespace returns an empty list, so
// no NotFound handling is needed.
func (r *ClusterBaselineReconciler) checkScanStorage(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(complianceNamespace)); err != nil {
		return err
	}
	// Owned scan PVC names: built-in CO profile names and TailoredProfile names,
	// plus known role suffixes only (see matchesAnyProfile / scanRoleSuffix).
	names := map[string]bool{}
	for _, key := range cb.Spec.Profiles {
		for _, p := range key.ProfileNames() {
			names[p] = true
		}
	}
	for _, name := range cb.Spec.TailoredProfiles {
		names[name] = true
	}
	var pending []string
	for _, pvc := range pvcs.Items {
		owned := matchesAnyProfile(pvc.Name, names)
		// Require a real CreationTimestamp: a zero time makes time.Since huge and
		// would false-Degrade brand-new objects in some test/API edge paths.
		if owned &&
			pvc.Status.Phase == corev1.ClaimPending &&
			!pvc.CreationTimestamp.IsZero() &&
			time.Since(pvc.CreationTimestamp.Time) > 2*time.Minute {
			pending = append(pending, pvc.Name)
		}
	}
	if len(pending) > 0 {
		setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "ScanStoragePending",
			fmt.Sprintf("PVC(s) %s/%s Pending >2m; need a default StorageClass",
				complianceNamespace, strings.Join(pending, ", ")))
		return nil
	}
	setCond(cb, "ScanStorageReady", metav1.ConditionTrue, "AsExpected", "")
	return nil
}
