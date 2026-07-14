package controller

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func TestSetCondEmptyReasonDefaults(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "", "pending")
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || c.Reason != "Unknown" {
		t.Fatalf("empty reason must become Unknown, got %+v", c)
	}
	// Rollup must use a fixed CamelCase reason, never the detail Reason.
	setRollupConditions(cb)
	d := meta.FindStatusCondition(cb.Status.Conditions, "Degraded")
	if d == nil || d.Status != metav1.ConditionTrue || d.Reason != "ScanStorageNotReady" {
		t.Fatalf("Degraded must be ScanStorageNotReady, got %+v", d)
	}
}

// TestSanitizeStatusConditions repairs hand-edited conditions that would fail
// CRD admission (empty/invalid reason, bad status Enum, invalid type, zero
// lastTransitionTime) so Status().Update cannot freeze reconcile.
func TestSanitizeStatusConditions(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{
		Status: baselinev1alpha1.ClusterBaselineStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Available",
					Status:             "NotAStatus",
					Reason:             "", // empty fails minLength=1
					Message:            "ok",
					LastTransitionTime: metav1.Time{}, // zero fails format
				},
				{
					Type:               "Progressing",
					Status:             metav1.ConditionFalse,
					Reason:             "bad reason with spaces", // fails pattern
					Message:            "x",
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "!!!invalid-type!!!",
					Status:             metav1.ConditionTrue,
					Reason:             "Ok",
					Message:            "drop me",
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "Available", // duplicate type; keep first repaired
					Status:             metav1.ConditionTrue,
					Reason:             "AsExpected",
					Message:            "dup",
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "Degraded",
					Status:             metav1.ConditionFalse,
					Reason:             "AsExpected",
					Message:            "fine",
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}
	sanitizeStatusForUpdate(cb)
	if got := len(cb.Status.Conditions); got != 3 {
		t.Fatalf("conditions len = %d, want 3 (invalid type dropped, dup collapsed)", got)
	}
	avail := meta.FindStatusCondition(cb.Status.Conditions, "Available")
	if avail == nil {
		t.Fatal("Available missing")
	}
	if avail.Status != metav1.ConditionUnknown {
		t.Fatalf("bad status Enum must become Unknown, got %q", avail.Status)
	}
	if avail.Reason != "Unknown" {
		t.Fatalf("empty reason must become Unknown, got %q", avail.Reason)
	}
	if avail.LastTransitionTime.IsZero() {
		t.Fatal("zero lastTransitionTime must be filled")
	}
	prog := meta.FindStatusCondition(cb.Status.Conditions, "Progressing")
	if prog == nil || prog.Reason != "Unknown" {
		t.Fatalf("invalid reason pattern must become Unknown, got %+v", prog)
	}
	if meta.FindStatusCondition(cb.Status.Conditions, "!!!invalid-type!!!") != nil {
		t.Fatal("invalid type must be dropped")
	}
	deg := meta.FindStatusCondition(cb.Status.Conditions, "Degraded")
	if deg == nil || deg.Reason != "AsExpected" || deg.Message != "fine" {
		t.Fatalf("valid condition must be preserved: %+v", deg)
	}
}

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

	// Manual install, CO absent: the reasons production actually emits
	// (ComplianceOperatorReady=NotInstalled, ScanConfigured=CRDsMissing). Neither
	// is progress, so this steady state must settle Progressing=False.
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled", "manual")
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing", "no CRDs")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Progressing must be False for permanent NotInstalled: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Available must be False when CO not installed: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Manual-not-installed steady state must not Degrade: %+v", c)
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
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Degraded when healthy: %+v", c)
	}

	// Plugin still rolling out (pending reason) keeps Progressing True.
	setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "WaitingForPods", "0/2 ready")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing while plugin pending: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("plugin pending must not be Degraded: %+v", c)
	}

	// Plugin down past grace period rolls into Degraded.
	setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "Unavailable", "no ready pods for >5m")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ConsolePluginUnavailable" {
		t.Fatalf("Degraded for unavailable plugin: %+v", c)
	}

	// Pending scan storage rolls into Degraded with a fixed rollup reason
	// (never copies a possibly hostile detail Reason).
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "ScanStoragePending", "PVC pending")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ScanStorageNotReady" {
		t.Fatalf("Degraded for pending storage: %+v", c)
	}
	// Hostile detail Reason must not land on Degraded (CRD Reason pattern).
	setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "not a valid reason!!!", "still pending")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Reason != "ScanStorageNotReady" {
		t.Fatalf("Degraded must use fixed ScanStorageNotReady, got %+v", c)
	}
	setCond(cb, "ScanStorageReady", metav1.ConditionTrue, "AsExpected", "")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Degraded must clear: %+v", c)
	}

	// Invalid cron leaves Available=False and Degraded=True so operators notice.
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "InvalidSchedule", "bad cron")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "InvalidSchedule" {
		t.Fatalf("Degraded for invalid schedule: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Available must be False for invalid schedule: %+v", c)
	}

	// Terminal CSV failure is Degraded (not Progressing forever).
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVFailed", "phase=Failed")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "CSVFailed" {
		t.Fatalf("Degraded for CSVFailed: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("CSVFailed must not be Progressing: %+v", c)
	}
}

// TestStuckInstallDegrades: a CO install that has been Installing/CSVNotReady
// past the grace window rolls up to Degraded and stops Progressing (no eternal
// 15s hot-poll), while a fresh Installing still Progresses.
func TestStuckInstallDegrades(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")

	// Fresh install: Progressing, not Degraded.
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "installing")
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("fresh install must Progress: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("fresh install must not Degrade: %+v", c)
	}

	// Backdate the CO condition past the grace window.
	co := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	co.LastTransitionTime = metav1.NewTime(time.Now().Add(-coInstallGrace - time.Minute))
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "InstallStalled" {
		t.Fatalf("stuck install must Degrade/InstallStalled: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("stuck install must not Progress: %+v", c)
	}

	// Empty detail message still yields a usable Degraded message (no trailing junk).
	co.Message = ""
	setRollupConditions(cb)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Message == "" ||
		c.Message[len(c.Message)-1] == ' ' || c.Message[len(c.Message)-1] == ':' {
		t.Fatalf("InstallStalled message should use reason fallback, got %q", c)
	}
}

func TestConditionProgressing(t *testing.T) {
	if conditionProgressing(nil) {
		t.Fatal("nil")
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionTrue, Reason: "Installing"}) {
		t.Fatal("True status is not progressing")
	}
	for _, reason := range []string{"Installing", "CSVNotReady", "WaitingForPods"} {
		c := &metav1.Condition{Status: metav1.ConditionFalse, Reason: reason}
		if !conditionProgressing(c) {
			t.Fatalf("%s should progress", reason)
		}
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "NotInstalled"}) {
		t.Fatal("NotInstalled should not progress")
	}
	// ConsoleMissing is a steady state (Console capability off), not progress.
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "ConsoleMissing"}) {
		t.Fatal("ConsoleMissing is steady state, not progress")
	}
	// CRDsMissing is steady until admin installs CO (Manual) or OLM finishes
	// (Automatic still Progresses via Installing/CSVNotReady).
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "CRDsMissing"}) {
		t.Fatal("CRDsMissing is steady state, not progress")
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "ImageMissing"}) {
		t.Fatal("ImageMissing is permanent misconfig, not progress")
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "ImageInvalid"}) {
		t.Fatal("ImageInvalid is permanent misconfig, not progress")
	}
	if conditionProgressing(&metav1.Condition{Status: metav1.ConditionFalse, Reason: "Unavailable"}) {
		t.Fatal("Unavailable should not progress")
	}
}
