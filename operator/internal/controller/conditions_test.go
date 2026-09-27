package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-logr/logr/funcr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// Fixed clock for the rollup tests: setRollupConditions measures the CO
// install-stall grace against it, so a fixed reading keeps those assertions
// independent of when the suite runs.
var rollupTestNow = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func TestSetCondEmptyReasonDefaults(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "", "pending")
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")
	if c == nil || c.Reason != "Unknown" {
		t.Fatalf("empty reason must become Unknown, got %+v", c)
	}
	// Rollup must use a fixed CamelCase reason, never the detail Reason.
	setRollupConditions(cb, rollupTestNow)
	d := meta.FindStatusCondition(cb.Status.Conditions, "Degraded")
	if d == nil || d.Status != metav1.ConditionTrue || d.Reason != "ScanStorageNotReady" {
		t.Fatalf("Degraded must be ScanStorageNotReady, got %+v", d)
	}
}

// TestSanitizeStatusConditionsSizeBudget: per-entry clamps leave the LIST
// unbounded, so enough foreign pattern-valid types with max-size messages would
// push the object past the apiserver/etcd cap and freeze every Status().Update.
// The aggregate budget must drop foreign overflow while always keeping the
// operator-owned types, regardless of their position in the list.
func TestSanitizeStatusConditionsSizeBudget(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	// Worst-case adversarial content: 4-byte runes (rune clamp vs byte size)
	// and control chars (JSON escaping inflates each byte up to 6x).
	big := strings.Repeat("\U0001F600", conditionMessageMaxLen)
	inflate := strings.Repeat("\x01", conditionMessageMaxLen)
	now := metav1.Now()
	// Foreign conditions first with both payload shapes; operator types
	// appended LAST with hostile messages, so the reservation (not list order)
	// must save them AND their re-clamp must keep the reservation small.
	for i := 0; i < 25; i++ {
		cb.Status.Conditions = append(cb.Status.Conditions,
			metav1.Condition{
				Type: fmt.Sprintf("Custom%d", i), Status: metav1.ConditionTrue,
				Reason: "HandEdited", Message: big, LastTransitionTime: now,
			},
			metav1.Condition{
				Type: fmt.Sprintf("Inflate%d", i), Status: metav1.ConditionTrue,
				Reason: "HandEdited", Message: inflate, LastTransitionTime: now,
			})
	}
	for typ := range operatorConditionTypes {
		cb.Status.Conditions = append(cb.Status.Conditions, metav1.Condition{
			Type: typ, Status: metav1.ConditionTrue, Reason: "HandEdited",
			Message: big, LastTransitionTime: now,
		})
	}
	sanitizeStatusConditions(cb)
	total := 0
	for i := range cb.Status.Conditions {
		b, err := json.Marshal(&cb.Status.Conditions[i])
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
	}
	// Reserved operator types are re-clamped to the 1024-byte condMessage cap,
	// so ~4 KiB serialized each is generous slack on top of the budget.
	if total > conditionsSizeBudget+len(operatorConditionTypes)*4096 {
		t.Fatalf("conditions aggregate marshaled size %d exceeds budget", total)
	}
	for typ := range operatorConditionTypes {
		if meta.FindStatusCondition(cb.Status.Conditions, typ) == nil {
			t.Fatalf("operator condition %s dropped by the size budget", typ)
		}
	}
	if len(cb.Status.Conditions) >= 50+len(operatorConditionTypes) {
		t.Fatal("no foreign condition was dropped; budget did not engage")
	}
	// A small foreign set must pass through untouched (no churn in normal use).
	small := &baselinev1alpha1.ClusterBaseline{Status: baselinev1alpha1.ClusterBaselineStatus{
		Conditions: []metav1.Condition{
			{Type: "Custom", Status: metav1.ConditionTrue, Reason: "R", Message: "m", LastTransitionTime: now},
			{Type: "Available", Status: metav1.ConditionTrue, Reason: "AsExpected", LastTransitionTime: now},
		},
	}}
	sanitizeStatusConditions(small)
	if len(small.Status.Conditions) != 2 || small.Status.Conditions[0].Type != "Custom" {
		t.Fatalf("small list churned: %+v", small.Status.Conditions)
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
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing while installing: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Available while installing: %+v", c)
	}

	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", "phase=Installing")
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing while CSVNotReady: %+v", c)
	}

	// Manual install, CO absent: the reasons production actually emits
	// (ComplianceOperatorReady=NotInstalled, ScanConfigured=CRDsMissing). Neither
	// is progress, so this steady state must settle Progressing=False.
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled", "manual")
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing", "no CRDs")
	setRollupConditions(cb, rollupTestNow)
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
	setRollupConditions(cb, rollupTestNow)
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
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("Progressing while plugin pending: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("plugin pending must not be Degraded: %+v", c)
	}

	// Plugin down past grace period rolls into Degraded.
	setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "Unavailable", "no ready pods for >5m")
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ConsolePluginUnavailable" {
		t.Fatalf("Degraded for unavailable plugin: %+v", c)
	}

	// Pending scan storage rolls into Degraded with a fixed rollup reason
	// (never copies a possibly hostile detail Reason).
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "ScanStoragePending", "PVC pending")
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ScanStorageNotReady" {
		t.Fatalf("Degraded for pending storage: %+v", c)
	}
	// Hostile detail Reason must not land on Degraded (CRD Reason pattern).
	setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "not a valid reason!!!", "still pending")
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Reason != "ScanStorageNotReady" {
		t.Fatalf("Degraded must use fixed ScanStorageNotReady, got %+v", c)
	}
	setCond(cb, "ScanStorageReady", metav1.ConditionTrue, "AsExpected", "")
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Degraded must clear: %+v", c)
	}

	// Invalid cron leaves Available=False and Degraded=True so operators notice.
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "InvalidSchedule", "bad cron")
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "InvalidSchedule" {
		t.Fatalf("Degraded for invalid schedule: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Available"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("Available must be False for invalid schedule: %+v", c)
	}

	// Terminal CSV failure is Degraded (not Progressing forever).
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVFailed", "phase=Failed")
	setRollupConditions(cb, rollupTestNow)
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
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("fresh install must Progress: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("fresh install must not Degrade: %+v", c)
	}

	// Backdate the CO condition past the grace window.
	co := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	co.LastTransitionTime = metav1.NewTime(rollupTestNow.Add(-coInstallGrace - time.Minute))
	setRollupConditions(cb, rollupTestNow)
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Degraded"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "InstallStalled" {
		t.Fatalf("stuck install must Degrade/InstallStalled: %+v", c)
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("stuck install must not Progress: %+v", c)
	}

	// Empty detail message still yields a usable Degraded message (no trailing junk).
	co.Message = ""
	setRollupConditions(cb, rollupTestNow)
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

func TestCondIsTrue(t *testing.T) {
	if condIsTrue(nil) {
		t.Fatal("nil must be false")
	}
	if condIsTrue(&metav1.Condition{Status: metav1.ConditionFalse}) {
		t.Fatal("False must be false")
	}
	if !condIsTrue(&metav1.Condition{Status: metav1.ConditionTrue}) {
		t.Fatal("True must be true")
	}
}

func TestCondTrue(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	if condTrue(cb, "Available") {
		t.Fatal("missing condition must be false")
	}
	setCond(cb, "Available", metav1.ConditionTrue, "AsExpected", "")
	if !condTrue(cb, "Available") {
		t.Fatal("True Available must be true")
	}
	setCond(cb, "Available", metav1.ConditionFalse, "NotReady", "")
	if condTrue(cb, "Available") {
		t.Fatal("False Available must be false")
	}
}

// TestJSONStringLenMatchesMarshal pins the hand-rolled size estimate to the
// real encoder. Every escape class json.Marshal applies is exercised, plus
// multi-byte runes and the CRD-legal failure-name shapes. An ASCII-only corpus
// would pass with a plain len(s)+2 and hide the 6x under-count the budget fix
// exists to close.
func TestJSONStringLenMatchesMarshal(t *testing.T) {
	cases := []string{
		"",
		"rule_1",
		strings.Repeat("a", 253),
		// HTML-escaped by json.Marshal into the 6-byte \u00XX form: the case
		// that made a raw byte-length budget 6x wrong.
		"a&b<c>d",
		strings.Repeat("&", 253),
		`quote"backslash\`,
		"tab\tnewline\ncr\rff\fbs\b",
		"nul\x00bell\x07esc\x1b",
		// Valid JSON, escaped because they end a JavaScript line.
		"sep\u2028para\u2029",
		// Multi-byte runes are copied verbatim: a byte-length estimate that
		// added per-rune overhead here would over-count instead.
		"世界",
		"caf\u00e9",        // NFC, 2 bytes
		"cafe\u0301",       // NFD, 3 bytes, canonically equal to the NFC form
		"emoji \U0001F600", // 4 bytes
		"zwj \U0001F469\u200D\U0001F4BB",
		"ascii-with-\u00e7-\u4e16",
		strings.Repeat("\u2028", 100),
	}
	for _, s := range cases {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %q: %v", s, err)
		}
		if got, want := jsonStringLen(s), len(b); got != want {
			t.Errorf("jsonStringLen(%q) = %d, json.Marshal wrote %d (%q)", s, got, want, b)
		}
	}
}

// TestClampFailureListsToBudgetEscaping pins the budget to the escaped size: a
// list of '&'-bearing names is 6x its byte length once marshaled, and the budget
// must be computed from the escaped size or it admits a status write that
// overshoots failureListsSizeBudget by the same factor.
func TestClampFailureListsToBudgetEscaping(t *testing.T) {
	escaped := strings.Repeat("&", failureNameMaxLen)
	plain := strings.Repeat("a", failureNameMaxLen)

	// Both lists marshal to the same number of bytes only if the estimate
	// accounts for escaping; the trimmed result must match that budget.
	for _, name := range []string{plain, escaped} {
		l := make([]string, 0, 128)
		for i := 0; i < 128; i++ {
			l = append(l, name)
		}
		clampFailureListsToBudget(&l)

		actual := 0
		for _, n := range l {
			b, err := json.Marshal(n)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			actual += len(b) + 1
		}
		if actual > failureListsSizeBudget {
			t.Fatalf("trimmed list still exceeds budget: %d > %d (kept %d of 128)",
				actual, failureListsSizeBudget, len(l))
		}
		// A tail entry is kept whenever dropping one more would not help;
		// assert the estimate did not over-trim to nothing.
		if len(l) == 0 {
			t.Fatal("budget trim emptied the list")
		}
	}
}

func TestCondMessage(t *testing.T) {
	if condMessage("short") != "short" {
		t.Fatal("short message unchanged")
	}
	long := strings.Repeat("x", 2000)
	got := condMessage(long)
	if len(got) != 1024 || !strings.HasSuffix(got, "...") {
		t.Fatalf("condMessage len=%d suffix=%q", len(got), got[len(got)-3:])
	}
	// Multi-byte runes near the cut must not produce invalid UTF-8.
	// "界" is 3 bytes; pad so a naive byte cut would split it.
	multi := strings.Repeat("a", 1020) + "世界世界"
	got = condMessage(multi)
	if !utf8.ValidString(got) {
		t.Fatal("condMessage produced invalid UTF-8")
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", got[len(got)-10:])
	}
	if len(got) > 1024 {
		t.Fatalf("condMessage longer than cap: %d", len(got))
	}
}

func TestSetCondCapsMessage(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	// InvalidSchedule embeds the user schedule; a huge cron must not land
	// unbounded on the condition (status admission / etcd size).
	huge := strings.Repeat("0 ", 2000)
	setCond(cb, "ScanConfigured", metav1.ConditionFalse, "InvalidSchedule",
		fmt.Sprintf("spec.schedule %q is not a valid standard cron schedule: bad", huge))
	c := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	if c == nil || len(c.Message) > 1024 {
		t.Fatalf("setCond must cap message, got len=%d", len(c.Message))
	}
	if !strings.HasSuffix(c.Message, "...") {
		t.Fatalf("expected truncated message, got %q", c.Message[len(c.Message)-20:])
	}
}

// FuzzCondMessage: condition messages embed untrusted cron text, PVC names, and
// wrap errors. Truncation must stay <=1024 bytes and always emit valid UTF-8.
func FuzzCondMessage(f *testing.F) {
	for _, seed := range []string{
		"", "short", strings.Repeat("x", 2000),
		strings.Repeat("a", 1020) + "世界世界",
		"\x80\x81", // invalid UTF-8 lead bytes
		strings.Repeat("界", 400),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := condMessage(s)
		if len(got) > 1024 {
			t.Fatalf("condMessage len %d > 1024", len(got))
		}
		if len(s) <= 1024 {
			// Short path is identity (may preserve invalid UTF-8 from wrap errors).
			if got != s {
				t.Fatalf("short input mutated: in=%q out=%q", s, got)
			}
			return
		}
		// Truncation must stay on a UTF-8 boundary so CR JSON remains valid.
		if !utf8.ValidString(got) {
			t.Fatal("condMessage produced invalid UTF-8")
		}
		if !strings.HasSuffix(got, "...") {
			t.Fatalf("long message missing ellipsis: %q", got[max(0, len(got)-10):])
		}
	})
}

func TestSetCondTrueLogRecovered(t *testing.T) {
	var buf bytes.Buffer
	logger := funcr.NewJSON(func(obj string) { _, _ = buf.WriteString(obj + "\n") }, funcr.Options{})
	ctx := log.IntoContext(t.Context(), logger)

	cb := &baselinev1alpha1.ClusterBaseline{}
	// Never False before: a first True write is not a recovery.
	setCondTrueLogRecovered(ctx, cb, "ScanStorageReady", "AsExpected", "", "ready", "name", cb.Name)
	if buf.Len() != 0 {
		t.Fatalf("first True write must not log a recovery: %s", buf.String())
	}
	if c := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("condition not set True: %+v", c)
	}

	// Failure, then recovery: the recovery logs.
	buf.Reset()
	setCondFalseLogOnce(ctx, cb, "ScanStorageReady", "ScanStoragePending", "PVC pending", "pending", "name", cb.Name)
	buf.Reset()
	setCondTrueLogRecovered(ctx, cb, "ScanStorageReady", "AsExpected", "", "ready", "name", cb.Name)
	if !strings.Contains(buf.String(), `"msg":"ready"`) {
		t.Fatalf("recovery must log at Info: %s", buf.String())
	}

	// Steady True re-assert stays silent (the 1m requeue must not spam).
	buf.Reset()
	setCondTrueLogRecovered(ctx, cb, "ScanStorageReady", "AsExpected", "", "ready", "name", cb.Name)
	if buf.Len() != 0 {
		t.Fatalf("steady True re-assert must stay silent: %s", buf.String())
	}

	// A reason change while the status stays False is a new failure and must log:
	// the transition guard reads a copy, not the entry SetStatusCondition
	// overwrites in place.
	buf.Reset()
	setCondFalseLogOnce(ctx, cb, "ScanStorageReady", "ScanStoragePending", "PVC pending", "pending one", "name", cb.Name)
	if !strings.Contains(buf.String(), `"msg":"pending one"`) {
		t.Fatalf("entering False must log: %s", buf.String())
	}
	buf.Reset()
	setCondFalseLogOnce(ctx, cb, "ScanStorageReady", "ScanStoragePending", "PVC pending", "pending one", "name", cb.Name)
	if buf.Len() != 0 {
		t.Fatalf("same False reason must stay silent: %s", buf.String())
	}
	buf.Reset()
	setCondFalseLogOnce(ctx, cb, "ScanStorageReady", "ScanStorageMissing", "no class", "pending two", "name", cb.Name)
	if !strings.Contains(buf.String(), `"msg":"pending two"`) {
		t.Fatalf("reason change while False must log: %s", buf.String())
	}
}
