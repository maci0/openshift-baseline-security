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
		t.Fatalf("ScanStorageReady = %+v, want False/ScanStoragePending", c)
	}
	if c.Message == "" || !strings.Contains(c.Message, "ocp4-cis") {
		t.Fatalf("Message should list ocp4-cis: %q", c.Message)
	}
	if strings.Contains(c.Message, "ocp4-cis-node-master") {
		t.Fatalf("fresh PVC should not be reported: %q", c.Message)
	}

	// Bound PVC clears ScanStorageReady to True/AsExpected (not a leftover Pending).
	stale.Status.Phase = corev1.ClaimBound
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale, other, fresh).Build()
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "AsExpected" {
		t.Fatalf("ScanStorageReady = %+v, want True/AsExpected after Bound", c)
	}
}

// TestCheckScanStoragePendingMessageSorted pins that a multi-PVC Pending message
// is emitted in sorted order: cache List order is map-randomized, and an
// unsorted message would rewrite the condition every reconcile, spinning a
// status-write/requeue loop.
func TestCheckScanStoragePendingMessageSorted(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	old := metav1.Time{Time: time.Now().Add(-5 * time.Minute)}
	// Two owned Pending PVCs; added in reverse of sorted order.
	node := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ocp4-cis-node-master", Namespace: complianceNamespace, CreationTimestamp: old},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	plat := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ocp4-cis", Namespace: complianceNamespace, CreationTimestamp: old},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, plat).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || !strings.Contains(c.Message, "ocp4-cis, ocp4-cis-node-master") {
		t.Fatalf("message not sorted: %q", c.Message)
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
	// A node-type TailoredProfile scans per role, producing role-suffixed PVCs
	// (custom-worker); a Pending one must also flag, matched by the "<name>-"
	// boundary, not just the exact name.
	nodePVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "custom-worker",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	// Short tailored name "a" must not prefix-match foreign PVC "anything".
	foreign := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "anything",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	// Ambiguous tailored base "ocp4" must not match foreign built-in PVC ocp4-cis.
	foreignCIS := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ocp4-cis",
			Namespace:         complianceNamespace,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc, nodePVC, foreign, foreignCIS).Build(),
		Scheme: scheme,
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			// No built-in cis: only tailored profiles, so ocp4-cis is foreign.
			Profiles:         []baselinev1alpha1.ProfileKey{"e8"},
			TailoredProfiles: []string{"custom", "a", "ocp4"},
		},
	}
	if err := r.checkScanStorage(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("tailored Pending PVC should flag ScanStorageReady=False, got %+v", c)
	}
	if !strings.Contains(c.Message, "custom-worker") {
		t.Fatalf("node-role tailored PVC should be flagged: %q", c.Message)
	}
	if strings.Contains(c.Message, "anything") {
		t.Fatalf("short tailored name must not match foreign PVC: %q", c.Message)
	}
	if strings.Contains(c.Message, "ocp4-cis") {
		t.Fatalf("ambiguous tailored base must not match foreign ocp4-cis: %q", c.Message)
	}
}
