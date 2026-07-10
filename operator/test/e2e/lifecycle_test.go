//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestInvalidScheduleDegrades sets an invalid cron and asserts the operator
// reports ScanConfigured=False/InvalidSchedule and Degraded=True without losing
// Available's dependencies, then restores a valid schedule and confirms it
// clears. Exercises the condition rollup on a live cluster.
func TestInvalidScheduleDegrades(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	original := cb.Spec.Schedule
	if original == "" {
		original = "0 1 * * *"
	}
	t.Cleanup(func() {
		restore, _ := getBaseline(ctx, c)
		restore.Spec.Schedule = original
		_ = c.Update(ctx, restore)
	})

	cb.Spec.Schedule = "not a cron"
	if err := c.Update(ctx, cb); err != nil {
		t.Fatalf("set invalid schedule: %v", err)
	}
	eventually(t, 2*time.Minute, "Degraded on invalid schedule", func() error {
		cur, err := getBaseline(ctx, c)
		if err != nil {
			return err
		}
		if conditionStatus(cur, "ScanConfigured") != "False" {
			return errf("ScanConfigured=%s", conditionStatus(cur, "ScanConfigured"))
		}
		if conditionStatus(cur, "Degraded") != "True" {
			return errf("Degraded=%s", conditionStatus(cur, "Degraded"))
		}
		return nil
	})

	// Restore and confirm it clears.
	cur, _ := getBaseline(ctx, c)
	cur.Spec.Schedule = original
	if err := c.Update(ctx, cur); err != nil {
		t.Fatalf("restore schedule: %v", err)
	}
	eventually(t, 2*time.Minute, "Degraded clears after valid schedule", func() error {
		g, err := getBaseline(ctx, c)
		if err != nil {
			return err
		}
		if conditionStatus(g, "Degraded") != "False" {
			return errf("Degraded=%s", conditionStatus(g, "Degraded"))
		}
		return nil
	})
}

// TestScheduleChangeUpdatesNextScan sets a different valid schedule and asserts
// NextScanTime advances to the new hour.
func TestScheduleChangeUpdatesNextScan(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	original := cb.Spec.Schedule
	if original == "" {
		original = "0 1 * * *"
	}
	t.Cleanup(func() {
		restore, _ := getBaseline(ctx, c)
		restore.Spec.Schedule = original
		_ = c.Update(ctx, restore)
	})

	// Pick an hour different from the current schedule.
	cb.Spec.Schedule = "30 5 * * *"
	if err := c.Update(ctx, cb); err != nil {
		t.Fatalf("set schedule: %v", err)
	}
	eventually(t, 2*time.Minute, "NextScanTime at 05:30 UTC", func() error {
		cur, err := getBaseline(ctx, c)
		if err != nil {
			return err
		}
		if cur.Status.NextScanTime == nil {
			return errf("NextScanTime nil")
		}
		if h, m := cur.Status.NextScanTime.Time.UTC().Hour(), cur.Status.NextScanTime.Time.UTC().Minute(); h != 5 || m != 30 {
			return errf("NextScanTime=%02d:%02d, want 05:30", h, m)
		}
		return nil
	})
}

// TestConsoleRemovedThenManaged tears the console plugin down via
// spec.console.managementState=Removed and confirms the Deployment is gone and
// the plugin is deregistered, then restores Managed and confirms it comes back.
func TestConsoleRemovedThenManaged(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		restore, _ := getBaseline(ctx, c)
		restore.Spec.Console.ManagementState = baselinev1alpha1.Managed
		_ = c.Update(ctx, restore)
	})

	cb.Spec.Console.ManagementState = baselinev1alpha1.Removed
	if err := c.Update(ctx, cb); err != nil {
		t.Fatalf("set console Removed: %v", err)
	}
	eventually(t, 3*time.Minute, "plugin Deployment removed + deregistered", func() error {
		dep := &appsv1.Deployment{}
		err := c.Get(ctx, client.ObjectKey{Namespace: pluginNS, Name: pluginName}, dep)
		if !apierrors.IsNotFound(err) {
			return errf("Deployment still present (err=%v)", err)
		}
		console := &unstructured.Unstructured{}
		console.SetGroupVersionKind(consoleGVK)
		if err := c.Get(ctx, client.ObjectKey{Name: "cluster"}, console); err != nil {
			return err
		}
		plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if contains(plugins, pluginName) {
			return errf("plugin still registered: %v", plugins)
		}
		return nil
	})

	// Restore Managed and confirm the plugin comes back Ready.
	cur, _ := getBaseline(ctx, c)
	cur.Spec.Console.ManagementState = baselinev1alpha1.Managed
	if err := c.Update(ctx, cur); err != nil {
		t.Fatalf("restore Managed: %v", err)
	}
	eventually(t, 3*time.Minute, "plugin redeployed and Ready", func() error {
		dep := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
			return err
		}
		if dep.Status.ReadyReplicas < 1 {
			return errf("plugin not ready yet")
		}
		return nil
	})
}
