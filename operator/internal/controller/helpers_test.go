package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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

	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", "phase=Installing")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing while CSVNotReady: %+v", c)
	}

	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled", "manual")
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "NotInstalled", "no CRDs")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Progressing must be False for permanent NotInstalled: %+v", c)
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

func TestConditionProgressing(t *testing.T) {
	if conditionProgressing(nil) {
		t.Fatal("nil")
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionTrue, Reason: "Installing"}) {
		t.Fatal("True status is not progressing")
	}
	for _, reason := range []string{"Installing", "CSVNotReady", "ImageMissing", "WaitingForPods", "CRDsMissing", "ConsoleMissing"} {
		c := &metav1.Condition{Status: metav1.ConditionFalse, Reason: reason}
		if !conditionProgressing(c) {
			t.Fatalf("%s should progress", reason)
		}
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "NotInstalled"}) {
		t.Fatal("NotInstalled should not progress")
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "Unavailable"}) {
		t.Fatal("Unavailable should not progress")
	}
}

func TestPluginDeploymentUnavailable(t *testing.T) {
	now := metav1.Now()
	old := metav1.NewTime(now.Add(-10 * time.Minute))
	dep := &appsv1.Deployment{}
	dep.CreationTimestamp = old
	if !pluginDeploymentUnavailable(dep, 5*time.Minute) {
		t.Fatal("old creation without Available condition should be unavailable")
	}
	dep.CreationTimestamp = now
	if pluginDeploymentUnavailable(dep, 5*time.Minute) {
		t.Fatal("fresh creation should still be waiting")
	}
	// Old object with a *recent* Available=False must still be Waiting, not Unavailable.
	dep.CreationTimestamp = old
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:               appsv1.DeploymentAvailable,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: now,
	}}
	if pluginDeploymentUnavailable(dep, 5*time.Minute) {
		t.Fatal("recent Available=False on old Deployment must not count as Unavailable")
	}
	dep.Status.Conditions[0].LastTransitionTime = old
	if !pluginDeploymentUnavailable(dep, 5*time.Minute) {
		t.Fatal("Available=False for >timeout should be unavailable")
	}
	// Available=True must never flip to Unavailable even if ReadyReplicas is 0.
	dep.Status.Conditions[0].Status = corev1.ConditionTrue
	dep.Status.Conditions[0].LastTransitionTime = old
	if pluginDeploymentUnavailable(dep, 5*time.Minute) {
		t.Fatal("Available=True must not count as Unavailable")
	}
}

func TestDeploymentAvailable(t *testing.T) {
	dep := &appsv1.Deployment{}
	if deploymentAvailable(dep) {
		t.Fatal("missing condition is not available")
	}
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse,
	}}
	if deploymentAvailable(dep) {
		t.Fatal("False is not available")
	}
	dep.Status.Conditions[0].Status = corev1.ConditionTrue
	if !deploymentAvailable(dep) {
		t.Fatal("True should be available")
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
	again := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns.Name}}
	if err := createIfMissing(context.Background(), c, again); err != nil {
		t.Fatal("AlreadyExists should be ignored:", err)
	}
}
