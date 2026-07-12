package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// Owned ScanSetting product defaults (ADR-023). Not CR fields: zero-config
// scans match Compliance Operator teaching defaults. verify-product-lockstep
// does not need these (console does not set ScanSetting leaves).
const (
	scanResultStorageSize     = "1Gi"
	scanResultStorageRotation = int64(3)
)

func (r *ClusterBaselineReconciler) ensureScanConfig(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	// Validate schedule first, but still reconcile ScanSetting fields other than
	// schedule and all bindings so a bad cron does not freeze profile/tp or
	// auto-apply changes. Invalid schedule is reported as Degraded at the end.
	schedule, schedErr := normalizedSchedule(cb.Spec.Schedule)
	invalidScheduleMessage := ""
	if schedErr != nil {
		invalidScheduleMessage = schedErr.Error()
	}

	ss := u(scanSettingGVK)
	ss.SetName(scanSettingName)
	ss.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		autoApply := cb.Spec.Remediation.Apply == baselinev1alpha1.RemediationApplyAutomatic
		// Only write a validated schedule; keep the last-good cron if invalid.
		// On first create with a bad cron there is no last-good value: fall back
		// to the operator default so CO is not left with an empty schedule.
		if schedErr == nil {
			ss.Object["schedule"] = schedule
		} else if existing, found, err := unstructured.NestedString(ss.Object, "schedule"); err != nil {
			return fmt.Errorf("reading ScanSetting schedule: %w", err)
		} else if !found || existing == "" {
			ss.Object["schedule"] = defaultScanSchedule
		}
		// Both control-plane and worker pools (ADR-023); SNO still has both roles.
		ss.Object["roles"] = []any{"worker", "master"}
		// Set only the storage leaves we own; preserve server-defaulted siblings
		// (e.g. pvAccessModes) so this does not diff on every reconcile.
		// Wrong-type rawResultStorage must not be overwritten with a bare map
		// that would drop siblings; fail the reconcile so the shape is fixed.
		storage, _, err := unstructured.NestedMap(ss.Object, "rawResultStorage")
		if err != nil {
			return fmt.Errorf("reading ScanSetting rawResultStorage: %w", err)
		}
		if storage == nil {
			storage = map[string]any{}
		}
		storage["size"] = scanResultStorageSize
		storage["rotation"] = scanResultStorageRotation
		ss.Object["rawResultStorage"] = storage
		ss.Object["autoApplyRemediations"] = autoApply
		ss.Object["autoUpdateRemediations"] = autoApply
		return controllerutil.SetControllerReference(cb, ss, r.Scheme)
	})
	if err != nil {
		if meta.IsNoMatchError(err) {
			setScanCRDsMissing(cb)
			return nil
		}
		return fmt.Errorf("ensuring ScanSetting %s/%s: %w", complianceNamespace, scanSettingName, err)
	}

	for _, key := range cb.Spec.Profiles {
		names := key.ProfileNames()
		profiles := make([]any, 0, len(names))
		for _, p := range names {
			profiles = append(profiles, complianceRef("Profile", p))
		}
		if err := r.ensureScanBinding(ctx, cb, bindingName(key), profiles); err != nil {
			return err
		}
	}

	for _, name := range cb.Spec.TailoredProfiles {
		profiles := []any{complianceRef("TailoredProfile", name)}
		if err := r.ensureScanBinding(ctx, cb, tailoredBindingName(name), profiles); err != nil {
			return err
		}
	}

	bindings := uList(bindingGVK)
	if err := r.List(ctx, bindings, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			setScanCRDsMissing(cb)
			return nil
		}
		return fmt.Errorf("listing ScanSettingBindings in %s: %w", complianceNamespace, err)
	}
	selected := ownedSuites(cb)
	for i := range bindings.Items {
		b := &bindings.Items[i]
		if selected[b.GetName()] || !metav1.IsControlledBy(b, cb) {
			continue
		}
		if err := r.Delete(ctx, b); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting ScanSettingBinding %s/%s: %w", complianceNamespace, b.GetName(), err)
		}
	}
	if invalidScheduleMessage != "" {
		msg := fmt.Sprintf("spec.schedule %q is not a valid standard cron schedule: %s", cb.Spec.Schedule, invalidScheduleMessage)
		// Info once on transition: InvalidSchedule rolls up to Degraded but leaves
		// last-good cron on the ScanSetting. Without this log, on-call only sees a
		// generic Degraded reason until the 15m alert (and may miss the bad cron
		// text if the condition message was truncated). Do not re-log every requeue.
		prev := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
		setCond(cb, "ScanConfigured", metav1.ConditionFalse, "InvalidSchedule", msg)
		if prev == nil || prev.Status != metav1.ConditionFalse || prev.Reason != "InvalidSchedule" {
			log.FromContext(ctx).Info("invalid scan schedule; keeping last-good cron on ScanSetting",
				"name", cb.Name, "schedule", cb.Spec.Schedule, "error", invalidScheduleMessage)
		}
		return nil
	}
	// No profiles and no tailored profiles: scanning is intentionally disabled.
	// Bindings were pruned above; report it as a healthy (not Degraded) state.
	if len(cb.Spec.Profiles) == 0 && len(cb.Spec.TailoredProfiles) == 0 {
		setCond(cb, "ScanConfigured", metav1.ConditionTrue, "ScanningDisabled",
			"No profiles selected; scanning is disabled.")
		return nil
	}
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	return nil
}

// complianceRef builds a compliance.openshift.io object reference as used in
// ScanSettingBinding profiles/settingsRef entries.
func complianceRef(kind, name string) map[string]any {
	return map[string]any{
		"apiGroup": "compliance.openshift.io/v1alpha1", "kind": kind, "name": name,
	}
}

// ensureScanBinding creates or updates a single ScanSettingBinding pointing the
// given profile refs at the shared ScanSetting, owned by cb.
func (r *ClusterBaselineReconciler) ensureScanBinding(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, name string, profileRefs []any) error {
	binding := u(bindingGVK)
	binding.SetName(name)
	binding.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Object["profiles"] = profileRefs
		binding.Object["settingsRef"] = complianceRef("ScanSetting", scanSettingName)
		return controllerutil.SetControllerReference(cb, binding, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("ensuring ScanSettingBinding %s/%s: %w", complianceNamespace, name, err)
	}
	return nil
}
