package controller

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// pluginGraceTestNow is the clock reading the grace helpers are given: a fixed
// instant keeps the plugin-unavailable assertions independent of when the suite
// runs.
var pluginGraceTestNow = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func TestPluginDeploymentUnavailable(t *testing.T) {
	now := metav1.NewTime(pluginGraceTestNow)
	old := metav1.NewTime(pluginGraceTestNow.Add(-10 * time.Minute))
	dep := &appsv1.Deployment{}
	dep.CreationTimestamp = old
	if !pluginDeploymentUnavailable(dep, pluginGraceTestNow) {
		t.Fatal("old creation without Available condition should be unavailable")
	}
	dep.CreationTimestamp = now
	if pluginDeploymentUnavailable(dep, pluginGraceTestNow) {
		t.Fatal("fresh creation should still be waiting")
	}
	// Old object with a *recent* Available=False must still be Waiting, not Unavailable.
	dep.CreationTimestamp = old
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:               appsv1.DeploymentAvailable,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: now,
	}}
	if pluginDeploymentUnavailable(dep, pluginGraceTestNow) {
		t.Fatal("recent Available=False on old Deployment must not count as Unavailable")
	}
	dep.Status.Conditions[0].LastTransitionTime = old
	if !pluginDeploymentUnavailable(dep, pluginGraceTestNow) {
		t.Fatal("Available=False for >timeout should be unavailable")
	}
	// Enough ready replicas: never Unavailable regardless of Available condition age.
	dep.Status.ReadyReplicas = pluginReadyMin
	dep.Status.Conditions[0].Status = corev1.ConditionTrue
	dep.Status.Conditions[0].LastTransitionTime = old
	if pluginDeploymentUnavailable(dep, pluginGraceTestNow) {
		t.Fatal("ReadyReplicas >= min must not count as Unavailable")
	}
	// Available=True but zero ready past grace is pathological (stuck HA).
	dep.Status.ReadyReplicas = 0
	if !pluginDeploymentUnavailable(dep, pluginGraceTestNow) {
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
	now := metav1.NewTime(pluginGraceTestNow)
	old := metav1.NewTime(pluginGraceTestNow.Add(-10 * time.Minute))
	dep := &appsv1.Deployment{}
	if deploymentAvailableFalsePastGrace(dep, pluginGraceTestNow) {
		t.Fatal("missing condition")
	}
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, LastTransitionTime: now,
	}}
	if deploymentAvailableFalsePastGrace(dep, pluginGraceTestNow) {
		t.Fatal("recent False must wait")
	}
	dep.Status.Conditions[0].LastTransitionTime = old
	if !deploymentAvailableFalsePastGrace(dep, pluginGraceTestNow) {
		t.Fatal("old False should be past grace")
	}
	dep.Status.Conditions[0].Status = corev1.ConditionTrue
	if deploymentAvailableFalsePastGrace(dep, pluginGraceTestNow) {
		t.Fatal("True is not False-past-grace")
	}
}

// The grace helpers must measure against the clock they are handed, not the
// process wall clock: a reconcile replayed from a virtual clock (see clock.go)
// or a host whose clock steps backward would otherwise report a different
// Degraded state than the one the reconcile decided on.
func TestPluginGraceUsesSuppliedClock(t *testing.T) {
	transition := metav1.NewTime(pluginGraceTestNow)
	dep := &appsv1.Deployment{}
	dep.CreationTimestamp = transition
	dep.Status.Conditions = []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse,
		LastTransitionTime: transition,
	}}
	// Clock reading before the transition: age is negative, so nothing is past
	// grace even though real wall time is months later.
	before := pluginGraceTestNow.Add(-time.Hour)
	if pluginDeploymentUnavailable(dep, before) {
		t.Fatal("a clock reading before the transition must not report Unavailable")
	}
	if deploymentAvailableFalsePastGrace(dep, before) {
		t.Fatal("a clock reading before the transition must not be past grace")
	}
	after := pluginGraceTestNow.Add(pluginUnavailableGrace + time.Minute)
	if !pluginDeploymentUnavailable(dep, after) {
		t.Fatal("a clock reading past the grace must report Unavailable")
	}
	if !deploymentAvailableFalsePastGrace(dep, after) {
		t.Fatal("a clock reading past the grace must be past grace")
	}
}

func TestApplyPluginContainerPreStop(t *testing.T) {
	pod := &corev1.PodSpec{}
	applyPluginContainer(pod, "quay.io/example/plugin:0.1.0")
	c := pod.Containers[0]
	if c.Lifecycle == nil || c.Lifecycle.PreStop == nil {
		t.Fatal("preStop hook required so the endpoint leaves the Service before SIGTERM")
	}
	// A sleep handler, never exec: the hook must not depend on a shell or
	// coreutils binary existing in the base image (the manager image is
	// ubi-micro and ships neither), and an exec that cannot run is a hook the
	// kubelet skips before the pod ever drains.
	if c.Lifecycle.PreStop.Exec != nil || c.Lifecycle.PreStop.Sleep == nil {
		t.Fatalf("preStop must be the native sleep handler, got exec=%v sleep=%v",
			c.Lifecycle.PreStop.Exec, c.Lifecycle.PreStop.Sleep)
	}
	if c.Lifecycle.PreStop.Sleep.Seconds != pluginPreStopSeconds {
		t.Fatalf("preStop sleep = %ds, want %ds", c.Lifecycle.PreStop.Sleep.Seconds, pluginPreStopSeconds)
	}
	// The sleep must fit inside the grace period or the pod is SIGKILLed
	// before nginx ever sees SIGTERM.
	if *pod.TerminationGracePeriodSeconds <= pluginPreStopSeconds {
		t.Fatalf("terminationGracePeriodSeconds = %d, must exceed the preStop sleep", *pod.TerminationGracePeriodSeconds)
	}
}

// The plugin probes are only useful if nginx actually answers on that path. A
// probe aimed at a location that does not exist fails the pod forever, and a
// TCP probe silently stops checking anything. Pin both sides to the same file
// the image is built from.
func TestPluginHealthzPathExistsInNginxConf(t *testing.T) {
	const conf = "../../../console-plugin/nginx.conf"
	raw, err := os.ReadFile(filepath.FromSlash(conf))
	if err != nil {
		t.Fatalf("read %s: %v", conf, err)
	}
	text := string(raw)
	// Startup and liveness take the constant return: a restart cannot change
	// whether the baked-in asset tree is readable, so failing liveness there
	// would only CrashLoop a broken image.
	block := locationBlock(t, text, pluginHealthzPath)
	if !strings.Contains(block, "return 200") {
		t.Fatalf("%s block does not `return 200`:\n%s", pluginHealthzPath, block)
	}
	// Readiness has to answer a question the constant return cannot: can this
	// worker read the asset root. Without the check a pod holding the listener
	// over an unreadable tree reports ready and 404s every console asset.
	ready := locationBlock(t, text, pluginReadyzPath)
	if !strings.Contains(ready, "return 503") {
		t.Fatalf("%s block does not `return 503` on an unreadable asset root:\n%s", pluginReadyzPath, ready)
	}
	if !strings.Contains(ready, "-r /opt/app-root/src") {
		t.Fatalf("%s block does not test readability of the asset root:\n%s", pluginReadyzPath, ready)
	}
	// The listen port the probes target must match the TLS listener.
	if !strings.Contains(text, "listen "+strconv.Itoa(pluginPort)+" ssl http2;") {
		t.Fatalf("nginx.conf does not listen on %d, the port the probes target", pluginPort)
	}
}

// locationBlock returns the body of the `location = <path>` block in an nginx
// config, so a test can assert on the directives a probe path really carries.
func locationBlock(t *testing.T, text, path string) string {
	t.Helper()
	loc := "location = " + path + " {"
	start := strings.Index(text, loc)
	if start < 0 {
		t.Fatalf("nginx.conf has no `%s` block; the plugin probes it", loc)
	}
	block := text[start:]
	if end := strings.Index(block, "\n        }"); end >= 0 {
		block = block[:end]
	}
	return block
}
