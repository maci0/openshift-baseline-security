//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestBaselineReadyAndScored is the core happy-path assertion: the operator
// reconciles the default ClusterBaseline to Available with a numeric score and
// all detail conditions healthy.
func TestBaselineReadyAndScored(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	eventually(t, 10*time.Minute, "ClusterBaseline Available", func() error {
		cb, err := getBaseline(ctx, c)
		if err != nil {
			return err
		}
		if !conditionTrue(cb, "Available") {
			return errf("Available=%s", conditionStatus(cb, "Available"))
		}
		if cb.Status.Score == nil {
			return errf("score not set")
		}
		return nil
	})

	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("score=%d complianceOperator=%s", *cb.Status.Score, cb.Status.ComplianceOperatorVersion)

	for _, typ := range []string{"ComplianceOperatorReady", "ScanConfigured", "ConsolePluginReady", "ScanStorageReady"} {
		if !conditionTrue(cb, typ) {
			t.Errorf("condition %s = %s, want True", typ, conditionStatus(cb, typ))
		}
	}
	if s := conditionStatus(cb, "Degraded"); s != "False" {
		t.Errorf("Degraded = %s, want False", s)
	}
	if s := conditionStatus(cb, "Progressing"); s != "False" {
		t.Errorf("Progressing = %s, want False", s)
	}
	if *cb.Status.Score < 0 || *cb.Status.Score > 100 {
		t.Errorf("score %d out of range", *cb.Status.Score)
	}
	if len(cb.Status.Profiles) == 0 {
		t.Error("no per-profile status reported")
	}
	if cb.Status.LastScanTime == nil {
		t.Error("LastScanTime not set")
	}
}

// TestScanConfigResources verifies the owned Compliance Operator objects exist:
// one ScanSetting and one ScanSettingBinding per selected profile.
func TestScanConfigResources(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	ss := &unstructured.Unstructured{}
	ss.SetGroupVersionKind(scanSettingGVK)
	if err := c.Get(ctx, client.ObjectKey{Namespace: complianceNamespace, Name: "baseline"}, ss); err != nil {
		t.Fatalf("baseline ScanSetting missing: %v", err)
	}

	for _, key := range cb.Spec.Profiles {
		b := &unstructured.Unstructured{}
		b.SetGroupVersionKind(bindingGVK)
		name := "baseline-" + string(key)
		if err := c.Get(ctx, client.ObjectKey{Namespace: complianceNamespace, Name: name}, b); err != nil {
			t.Errorf("ScanSettingBinding %s missing: %v", name, err)
		}
	}
}

// TestConsolePluginDeployed verifies the plugin Deployment/Service/ConsolePlugin
// exist and the plugin is registered with the console operator.
func TestConsolePluginDeployed(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		t.Fatalf("plugin Deployment missing: %v", err)
	}
	if dep.Status.ReadyReplicas < 1 {
		t.Errorf("plugin Deployment has %d ready replicas", dep.Status.ReadyReplicas)
	}
	if dep.Spec.Template.Spec.AutomountServiceAccountToken == nil || *dep.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Error("plugin pod should set automountServiceAccountToken=false")
	}

	svc := &corev1.Service{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: pluginNS, Name: pluginName}, svc); err != nil {
		t.Fatalf("plugin Service missing: %v", err)
	}
	if svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] == "" {
		t.Error("plugin Service missing serving-cert annotation")
	}

	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(consolePluginGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: pluginName}, cp); err != nil {
		t.Fatalf("ConsolePlugin missing: %v", err)
	}

	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: "cluster"}, console); err != nil {
		t.Fatalf("console operator config missing: %v", err)
	}
	plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if !contains(plugins, pluginName) {
		t.Errorf("plugin not registered in console spec.plugins: %v", plugins)
	}
}

// TestProfileToggle adds a second profile, verifies its binding is created,
// then removes it and verifies the binding is pruned. Restores the original
// profile set on completion.
func TestProfileToggle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]baselinev1alpha1.ProfileKey(nil), cb.Spec.Profiles...)
	if contains(profileKeys(original), "e8") {
		t.Skip("e8 already selected; toggle test needs an unselected profile")
	}
	t.Cleanup(func() {
		restore, _ := getBaseline(ctx, c)
		restore.Spec.Profiles = original
		_ = c.Update(ctx, restore)
	})

	cb.Spec.Profiles = append(original, "e8")
	if err := c.Update(ctx, cb); err != nil {
		t.Fatalf("add e8: %v", err)
	}

	eventually(t, 2*time.Minute, "baseline-e8 binding created", func() error {
		b := &unstructured.Unstructured{}
		b.SetGroupVersionKind(bindingGVK)
		return c.Get(ctx, client.ObjectKey{Namespace: complianceNamespace, Name: "baseline-e8"}, b)
	})

	cb, _ = getBaseline(ctx, c)
	cb.Spec.Profiles = original
	if err := c.Update(ctx, cb); err != nil {
		t.Fatalf("remove e8: %v", err)
	}

	eventually(t, 2*time.Minute, "baseline-e8 binding pruned", func() error {
		b := &unstructured.Unstructured{}
		b.SetGroupVersionKind(bindingGVK)
		err := c.Get(ctx, client.ObjectKey{Namespace: complianceNamespace, Name: "baseline-e8"}, b)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err == nil {
			return errf("binding still present")
		}
		return err
	})
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func profileKeys(keys []baselinev1alpha1.ProfileKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = string(k)
	}
	return out
}

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }
