package controller

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

func TestValidRelatedImage(t *testing.T) {
	for _, ref := range []string{
		"nginx",
		"quay.io/org/plugin:1.0",
		"registry.example.com:5000/ns/img:tag",
		"example.test/plugin@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if !ValidRelatedImage(ref) {
			t.Errorf("%q should be valid", ref)
		}
	}
	for _, ref := range []string{
		"",
		"has space",
		"bad!!!",
		"cmd;inject",
		"$(boom)",
		"img%20name",
		"img#frag",
		`img\path`,
		strings.Repeat("a", 1025),
	} {
		if ValidRelatedImage(ref) {
			t.Errorf("%q should be invalid", ref)
		}
	}
}

// relatedImageConsolePlugin trims whitespace so a mis-set env (padding, empty
// quotes) does not create a Deployment with an unpullable image ref.
func TestRelatedImageConsolePluginTrim(t *testing.T) {
	const key = "RELATED_IMAGE_CONSOLE_PLUGIN"
	prev, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if got := relatedImageConsolePlugin(); got != "" {
		t.Fatalf("unset env = %q, want empty", got)
	}

	if err := os.Setenv(key, "   "); err != nil {
		t.Fatal(err)
	}
	if got := relatedImageConsolePlugin(); got != "" {
		t.Fatalf("whitespace-only env = %q, want empty", got)
	}

	if err := os.Setenv(key, "  quay.io/org/plugin:1.0  "); err != nil {
		t.Fatal(err)
	}
	if got := relatedImageConsolePlugin(); got != "quay.io/org/plugin:1.0" {
		t.Fatalf("padded env = %q, want trimmed image", got)
	}
}

// FuzzValidRelatedImage: RELATED_IMAGE_CONSOLE_PLUGIN is untrusted env text.
// Must never panic; rejects empty, oversize, control chars, and shell/URL noise;
// accepts only when at least one alnum is present and no forbidden metachar.
func FuzzValidRelatedImage(f *testing.F) {
	for _, seed := range []string{
		"", "nginx", "quay.io/org/plugin:1.0", "has space", "cmd;inject",
		"$(boom)", "img%20", "img#frag", `img\path`,
		strings.Repeat("a", 1024), strings.Repeat("a", 1025),
		"registry:5000/ns/img@sha256:dead", "!!!", "\x00img", "img\n",
	} {
		f.Add(seed)
	}
	// Bound work: oversize refs are a single reject path.
	const maxSeed = 2048
	f.Fuzz(func(t *testing.T, ref string) {
		if len(ref) > maxSeed {
			ref = ref[:maxSeed]
		}
		got := ValidRelatedImage(ref)
		if ref == "" || len(ref) > 1024 {
			if got {
				t.Fatalf("empty/oversize accepted: len=%d", len(ref))
			}
			return
		}
		for _, r := range ref {
			if r <= 0x20 || r == 0x7f {
				if got {
					t.Fatalf("control/space accepted: %q", ref)
				}
				return
			}
		}
		if strings.ContainsAny(ref, "<>|;&$`\\\"'*?[]{}()!%#\\") {
			if got {
				t.Fatalf("metachar accepted: %q", ref)
			}
			return
		}
		hasAlnum := false
		for _, r := range ref {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				hasAlnum = true
				break
			}
		}
		if got != hasAlnum {
			t.Fatalf("ValidRelatedImage(%q) = %v, want %v", ref, got, hasAlnum)
		}
	})
}

// FuzzUnstructuredMetadataReads: CCR/remediation metadata maps are untrusted
// cluster JSON. Labels/annotations may be map[string]string, map[string]any, or
// the wrong type entirely. Helpers must never panic and must return "" on
// missing/wrong types; string values round-trip when present.
func FuzzUnstructuredMetadataReads(f *testing.F) {
	f.Add("name", "suite-a", "ann-v", "label-v", byte(0))
	f.Add("", "", "", "", byte(1))
	f.Add("x", "baseline-cis", "k", "v", byte(2))
	f.Add(strings.Repeat("n", 300), strings.Repeat("s", 300), "a", "l", byte(3))
	f.Fuzz(func(t *testing.T, name, suite, annVal, labelVal string, shape byte) {
		const max = 512
		if len(name) > max {
			name = name[:max]
		}
		if len(suite) > max {
			suite = suite[:max]
		}
		if len(annVal) > max {
			annVal = annVal[:max]
		}
		if len(labelVal) > max {
			labelVal = labelVal[:max]
		}

		meta := map[string]any{}
		if name != "" {
			meta["name"] = name
		}
		// Exercise typed and untyped maps, plus deliberate type confusion.
		switch shape % 4 {
		case 0:
			meta["labels"] = map[string]string{"compliance.openshift.io/suite": suite, "k": labelVal}
			meta["annotations"] = map[string]string{"a": annVal}
		case 1:
			meta["labels"] = map[string]any{"compliance.openshift.io/suite": suite, "k": labelVal}
			meta["annotations"] = map[string]any{"a": annVal}
		case 2:
			// Wrong types: must not panic; reads return "".
			meta["labels"] = suite
			meta["annotations"] = 42
			meta["name"] = []any{name}
		default:
			// Non-string values inside map[string]any.
			meta["labels"] = map[string]any{"compliance.openshift.io/suite": 7, "k": true}
			meta["annotations"] = map[string]any{"a": map[string]any{"nested": annVal}}
		}

		obj := map[string]any{"metadata": meta}
		// Never panic on any shape.
		gotName := unstructuredName(obj)
		gotLabel := unstructuredLabel(obj, "compliance.openshift.io/suite")
		gotAnn := unstructuredAnnotation(obj, "a")
		gotMissing := unstructuredLabel(obj, "does-not-exist")
		if gotMissing != "" {
			t.Fatalf("missing label returned %q", gotMissing)
		}
		// Empty / nil metadata.
		if unstructuredName(nil) != "" || unstructuredLabel(nil, "k") != "" {
			t.Fatal("nil object must yield empty reads")
		}
		if unstructuredName(map[string]any{}) != "" {
			t.Fatal("empty object must yield empty name")
		}

		switch shape % 4 {
		case 0, 1:
			if name != "" && gotName != name {
				t.Fatalf("name: got %q want %q", gotName, name)
			}
			if gotLabel != suite {
				t.Fatalf("suite label: got %q want %q", gotLabel, suite)
			}
			if gotAnn != annVal {
				t.Fatalf("annotation: got %q want %q", gotAnn, annVal)
			}
			// stringMapValue on typed map[string]string.
			if shape%4 == 0 {
				if v := stringMapValue(meta["labels"], "k"); v != labelVal {
					t.Fatalf("stringMapValue typed: got %q want %q", v, labelVal)
				}
			}
		case 2:
			// name was a non-string; labels/annotations wrong type.
			if gotName != "" || gotLabel != "" || gotAnn != "" {
				t.Fatalf("wrong types must yield empty: name=%q label=%q ann=%q", gotName, gotLabel, gotAnn)
			}
		default:
			// Non-string values inside any-maps: cast fails -> "".
			if gotLabel != "" || gotAnn != "" {
				t.Fatalf("non-string map values must yield empty: label=%q ann=%q", gotLabel, gotAnn)
			}
		}
	})
}

func TestRequeueAfter(t *testing.T) {
	steady := &baselinev1alpha1.ClusterBaseline{}
	setCond(steady, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	if got := requeueAfter(steady); got != time.Minute {
		t.Fatalf("steady = %v, want 1m", got)
	}
	installing := &baselinev1alpha1.ClusterBaseline{}
	setCond(installing, "Progressing", metav1.ConditionTrue, "Reconciling", "installing")
	if got := requeueAfter(installing); got != 15*time.Second {
		t.Fatalf("Progressing = %v, want 15s", got)
	}
	// In-flight batch must poll faster so cancel/grace/Applied are not stuck
	// behind the 1m steady cadence when the informer is lagging.
	batching := &baselinev1alpha1.ClusterBaseline{}
	setCond(batching, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	batching.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{Phase: "Applying"}
	if got := requeueAfter(batching); got != 15*time.Second {
		t.Fatalf("batch Applying = %v, want 15s", got)
	}
}

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
