package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func TestSetCond(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Generation = 7
	setCond(cb, "Degraded", metav1.ConditionTrue, "ScanStoragePending", "msg")
	c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded")
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ScanStoragePending" || c.Message != "msg" {
		t.Fatalf("%+v", c)
	}
	if c.ObservedGeneration != 7 {
		t.Fatalf("ObservedGeneration = %d, want 7", c.ObservedGeneration)
	}
	// Update in place.
	setCond(cb, "Degraded", metav1.ConditionFalse, "AsExpected", "")
	c = meta.FindStatusCondition(cb.Status.Conditions, "Degraded")
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "AsExpected" {
		t.Fatalf("%+v", c)
	}
	if len(cb.Status.Conditions) != 1 {
		t.Fatalf("expected single condition type, got %d", len(cb.Status.Conditions))
	}
}

func TestSetRollupConditions(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.Generation = 3
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing while installing: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Available while installing: %+v", c)
	}

	setCond(cb, "ComplianceOperatorReady", metav1.ConditionTrue, "CSVSucceeded", "")
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Available when ready: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Progressing when ready: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c.ObservedGeneration != 3 {
		t.Fatalf("ObservedGeneration = %d", c.ObservedGeneration)
	}
}

func TestCreateIfMissing(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "create-if-missing-test"}}
	if err := createIfMissing(context.Background(), c, ns); err != nil {
		t.Fatal(err)
	}
	got := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: ns.Name}, got); err != nil {
		t.Fatal(err)
	}
	// AlreadyExists is ignored.
	again := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns.Name}}
	if err := createIfMissing(context.Background(), c, again); err != nil {
		t.Fatal("AlreadyExists should be ignored:", err)
	}
}
