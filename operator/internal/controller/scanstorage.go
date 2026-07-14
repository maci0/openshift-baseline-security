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

// Grace before a still-Pending owned scan PVC is treated as stuck (no default
// StorageClass). Brand-new PVCs are ignored so provisioning lag is not Degraded.
const scanStoragePendingGrace = 2 * time.Minute

// checkScanStorage sets the ScanStorageReady detail condition false when owned
// scan PVCs stay Pending (no default StorageClass); the Degraded rollup
// propagates it. Listing in a nonexistent namespace returns an empty list, so
// no NotFound handling is needed.
func (r *ClusterBaselineReconciler) checkScanStorage(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(complianceNamespace)); err != nil {
		return fmt.Errorf("listing PVCs in %s: %w", complianceNamespace, err)
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
			time.Since(pvc.CreationTimestamp.Time) > scanStoragePendingGrace {
			pending = append(pending, pvc.Name)
		}
	}
	if len(pending) > 0 {
		// Info once on transition (not every requeue): rolls up to Degraded;
		// default logs must name the stuck PVCs so operators can diagnose them
		// without only reading the CR condition. Steady-state spam is noise.
		// Duration text from scanStoragePendingGrace so the message cannot drift
		// from the grace constant (and TEST-PLAN "Pending >2m" row).
		// We only observe Phase==Pending, not the StorageClass, so the message must
		// not assert "no default StorageClass": a WaitForFirstConsumer class with no
		// schedulable consumer, slow CSI provisioning, or node capacity all present
		// the same way. State the fact (Pending) and both likely causes.
		msg := fmt.Sprintf("PVC(s) %s in namespace %s Pending >%dm; ensure a StorageClass can provision them (a default StorageClass, and for WaitForFirstConsumer a schedulable consumer)",
			strings.Join(pending, ", "), complianceNamespace, int(scanStoragePendingGrace.Minutes()))
		setCondFalseLogOnce(ctx, cb, "ScanStorageReady", "ScanStoragePending", msg,
			"scan storage PVCs pending", "namespace", complianceNamespace, "pvcs", pending, "name", cb.Name)
		return nil
	}
	setCond(cb, "ScanStorageReady", metav1.ConditionTrue, "AsExpected", "")
	return nil
}
