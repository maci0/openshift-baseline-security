package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
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
	// Unrelated PVC must not flip Degraded.
	other := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "someone-else-scan",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	// Fresh pending PVC (<2m) for our profile must not flip Degraded yet.
	fresh := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ocp4-cis-node-master",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale, other, fresh).Build(),
		Scheme: scheme,
	}

	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "ScanStoragePending" {
		t.Fatalf("Degraded condition = %+v, want False/ScanStoragePending", c)
	}
	if c.Message == "" || !strings.Contains(c.Message, "ocp4-cis") {
		t.Fatalf("Message should list ocp4-cis: %q", c.Message)
	}
	if strings.Contains(c.Message, "ocp4-cis-node-master") {
		t.Fatalf("fresh PVC should not be reported: %q", c.Message)
	}

	// Bound PVC clears it.
	stale.Status.Phase = corev1.ClaimBound
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale, other, fresh).Build()
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Degraded condition = %+v, want False", c)
	}
}

func TestCheckScanStorageEmptyNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("%+v", c)
	}
}

func TestCheckScanStorageTailoredPVC(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	// A tailored scan PVC is named after the TailoredProfile; a Pending one
	// must flag ScanStorageReady=False.
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "custom",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles:         []baselinev1alpha1.ProfileKey{"cis"},
			TailoredProfiles: []string{"custom"},
		},
	}
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("tailored Pending PVC should flag ScanStorageReady=False, got %+v", c)
	}
}
