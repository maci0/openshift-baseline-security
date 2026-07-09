package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func TestCheckScanStorageDegradedOnPendingPVC(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	stale := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ocp4-cis",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build(),
		Scheme: scheme,
	}

	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ScanStoragePending" {
		t.Fatalf("Degraded condition = %+v, want True/ScanStoragePending", c)
	}

	// Bound PVC clears it.
	stale.Status.Phase = corev1.ClaimBound
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Degraded condition = %+v, want False", c)
	}
}
