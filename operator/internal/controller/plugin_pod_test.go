package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPluginDeploymentUnavailable(t *testing.T) {
	now := metav1.Now()
	old := metav1.NewTime(now.Add(-10 * time.Minute))
	dep := &appsv1.Deployment{}
	dep.CreationTimestamp = old
	if !pluginDeploymentUnavailable(dep) {
		t.Fatal("old creation without Available condition should be unavailable")
	}
	dep.CreationTimestamp = now
	if pluginDeploymentUnavailable(dep) {
		t.Fatal("fresh creation should still be waiting")
	}
	// Old object with a *recent* Available=False must still be Waiting, not Unavailable.
	dep.CreationTimestamp = old
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:               appsv1.DeploymentAvailable,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: now,
	}}
	if pluginDeploymentUnavailable(dep) {
		t.Fatal("recent Available=False on old Deployment must not count as Unavailable")
	}
	dep.Status.Conditions[0].LastTransitionTime = old
	if !pluginDeploymentUnavailable(dep) {
		t.Fatal("Available=False for >timeout should be unavailable")
	}
	// Enough ready replicas: never Unavailable regardless of Available condition age.
	dep.Status.ReadyReplicas = pluginReadyMin
	dep.Status.Conditions[0].Status = corev1.ConditionTrue
	dep.Status.Conditions[0].LastTransitionTime = old
	if pluginDeploymentUnavailable(dep) {
		t.Fatal("ReadyReplicas >= min must not count as Unavailable")
	}
	// Available=True but zero ready past grace is pathological (stuck HA).
	dep.Status.ReadyReplicas = 0
	if !pluginDeploymentUnavailable(dep) {
		t.Fatal("Available=True with 0 ready past grace should be Unavailable")
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

func TestDeploymentAvailableFalsePastGrace(t *testing.T) {
	now := metav1.Now()
	old := metav1.NewTime(now.Add(-10 * time.Minute))
	dep := &appsv1.Deployment{}
	if deploymentAvailableFalsePastGrace(dep) {
		t.Fatal("missing condition")
	}
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, LastTransitionTime: now,
	}}
	if deploymentAvailableFalsePastGrace(dep) {
		t.Fatal("recent False must wait")
	}
	dep.Status.Conditions[0].LastTransitionTime = old
	if !deploymentAvailableFalsePastGrace(dep) {
		t.Fatal("old False should be past grace")
	}
	dep.Status.Conditions[0].Status = corev1.ConditionTrue
	if deploymentAvailableFalsePastGrace(dep) {
		t.Fatal("True is not False-past-grace")
	}
}
