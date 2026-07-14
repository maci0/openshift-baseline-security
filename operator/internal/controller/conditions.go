package controller

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// failureListMax aliases the API constant so clamps stay CRD-aligned (ADR-013).
const failureListMax = baselinev1alpha1.FailureListMax

// sanitizeStatusForUpdate applies admission-safe bounds to status fields the
// reconciler writes so a hostile or stale status cannot brick updates.
// Per-profile and tailored history share the CRD MaxItems=30 / score [0,100]
// bounds with the top-level history ring. Failure-name lists share MaxItems=4096.
func sanitizeStatusForUpdate(cb *baselinev1alpha1.ClusterBaseline) {
	cb.Status.History = clampHistory(cb.Status.History, historyMax)
	cb.Status.Score = clampScore(cb.Status.Score)
	for i := range cb.Status.Profiles {
		cb.Status.Profiles[i].History = clampHistory(cb.Status.Profiles[i].History, historyMax)
	}
	for i := range cb.Status.TailoredProfiles {
		cb.Status.TailoredProfiles[i].History = clampHistory(cb.Status.TailoredProfiles[i].History, historyMax)
	}
	cb.Status.NewlyFailed = clampFailureList(cb.Status.NewlyFailed)
	cb.Status.Fixed = clampFailureList(cb.Status.Fixed)
	cb.Status.PreviousFailures = clampFailureList(cb.Status.PreviousFailures)
	cb.Status.DiffBaseFailures = clampFailureList(cb.Status.DiffBaseFailures)
	// complianceOperatorVersion is derived from a CSV name (object names allow up
	// to 253 chars), but the CRD caps the field at 128; clamp so a pathologically
	// long CSV name cannot fail Status().Update admission and freeze reconcile.
	cb.Status.ComplianceOperatorVersion = clampString(cb.Status.ComplianceOperatorVersion, complianceOperatorVersionMax)
}

// complianceOperatorVersionMax mirrors the CRD MaxLength on
// status.complianceOperatorVersion.
const complianceOperatorVersionMax = 128

// clampString truncates s to at most max runes (fast path when it already fits
// by byte length, which implies it fits by rune count). Rune-aware so truncation
// never splits a multibyte character into invalid UTF-8.
func clampString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// clampFailureList trims a status failure-name list to failureListMax (keeps
// the prefix). nil stays nil; empty stays empty. Does not alias after truncation.
func clampFailureList(in []string) []string {
	if len(in) <= failureListMax {
		return in
	}
	return append([]string(nil), in[:failureListMax]...)
}

// condMessage caps condition messages so a huge wrapped error, invalid cron,
// or long PVC list cannot exceed the Condition message budget or fail status
// admission. Truncates on a UTF-8 boundary so the CR JSON stays valid.
func condMessage(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	// Drop incomplete trailing rune; leave room for "...".
	end := max - 3
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end] + "..."
}

// parseScanEndTimestamp parses a ComplianceScan status.endTimestamp. Accepts
// RFC3339 with optional fractional seconds. Far-future values are rejected so
// clock skew / corrupt data cannot pin LastScanTime ahead of real scans.
func parseScanEndTimestamp(ts string, now time.Time) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	// RFC3339Nano is a superset of RFC3339 (fractional seconds optional), so it
	// parses plain RFC3339 too; no separate fallback needed.
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, false
	}
	// Allow modest clock skew; anything further ahead is treated as garbage.
	if t.After(now.Add(time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}

func setCond(cb *baselinev1alpha1.ClusterBaseline, typ string, status metav1.ConditionStatus, reason, msg string) {
	// Reason is required (minLength 1) and pattern-constrained on the CRD.
	// Never write empty: a hand-edited detail condition with Reason "" would
	// otherwise brick Status().Update when rolled up into Degraded.
	if reason == "" {
		reason = "Unknown"
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type:   typ,
		Status: status,
		Reason: reason,
		// Cap every message: InvalidSchedule embeds user cron text; storage
		// embeds PVC names; wrap errors can be huge. One path keeps status
		// updates from failing admission on an oversized message.
		Message:            condMessage(msg),
		ObservedGeneration: cb.Generation,
	})
}

// setCondFalseLogOnce sets a False detail condition and Info-logs only when the
// condition first enters this (False, reason) state, so a sticky failure does
// not spam the default log on every requeue. keysAndValues are structured log
// fields. Shared by the storage/schedule/plugin not-ready paths.
func setCondFalseLogOnce(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, typ, reason, msg, logMsg string, keysAndValues ...any) {
	prev := meta.FindStatusCondition(cb.Status.Conditions, typ)
	setCond(cb, typ, metav1.ConditionFalse, reason, msg)
	if prev == nil || prev.Status != metav1.ConditionFalse || prev.Reason != reason {
		log.FromContext(ctx).Info(logMsg, keysAndValues...)
	}
}

// conditionProgressing is true for non-terminal False detail reasons that mean
// work is still in flight (not permanent admin action like Manual NotInstalled).
func conditionProgressing(c *metav1.Condition) bool {
	if c == nil || c.Status != metav1.ConditionFalse {
		return false
	}
	switch c.Reason {
	// Steady states (must not Progress / 15s-poll forever):
	// - ImageMissing / ImageInvalid: permanent deployment misconfig
	// - ConsoleMissing: Console capability disabled
	// - CRDsMissing: no compliance CRDs (common with installComplianceOperator=Manual
	//   until the admin installs CO; Automatic install is already Progressing via
	//   Installing/CSVNotReady on ComplianceOperatorReady)
	case "Installing", "CSVNotReady", "WaitingForPods":
		return true
	default:
		return false
	}
}

// setRollupConditions sets Available, Progressing, and Degraded from the
// detail conditions (ClusterOperator-style rollups).
func setRollupConditions(cb *baselinev1alpha1.ClusterBaseline) {
	co := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	scan := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	plugin := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	storage := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")

	coReady := co != nil && co.Status == metav1.ConditionTrue
	scanOK := scan != nil && scan.Status == metav1.ConditionTrue
	// A Compliance Operator install that never becomes ready (bad catalog source,
	// unresolvable Subscription) would otherwise Progress + fast-poll forever. Past
	// a grace window, stop treating it as progress so it rolls up to Degraded and
	// the poll backs off, mirroring the console plugin's Unavailable-past-grace.
	coStuck := co != nil && co.Status == metav1.ConditionFalse &&
		(co.Reason == "Installing" || co.Reason == "CSVNotReady") &&
		!co.LastTransitionTime.IsZero() &&
		time.Since(co.LastTransitionTime.Time) > coInstallGrace
	progressing := (conditionProgressing(co) && !coStuck) ||
		conditionProgressing(scan) || conditionProgressing(plugin)

	if progressing {
		setCond(cb, "Progressing", metav1.ConditionTrue, "Reconciling", "installing or configuring dependencies")
	} else {
		setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	}
	if coReady && scanOK {
		setCond(cb, "Available", metav1.ConditionTrue, "AsExpected", "compliance operator ready and scans configured")
	} else {
		setCond(cb, "Available", metav1.ConditionFalse, "NotReady", "waiting for compliance operator and scan configuration")
	}
	// Degraded: persistent failures that are not mere installation progress:
	// failed Compliance Operator CSV, invalid schedule, scan result storage
	// wedged, or the plugin down past its grace period.
	// Use fixed CamelCase reasons (never copy a possibly empty/hostile detail
	// Reason) so status admission cannot fail on Reason pattern/minLength.
	switch {
	case co != nil && co.Status == metav1.ConditionFalse && co.Reason == "CSVFailed":
		setCond(cb, "Degraded", metav1.ConditionTrue, "CSVFailed", co.Message)
	case coStuck:
		// Prefer the detail message; fall back to reason so we never end with a
		// trailing empty ": ".
		detail := co.Message
		if detail == "" {
			detail = co.Reason
		}
		setCond(cb, "Degraded", metav1.ConditionTrue, "InstallStalled",
			fmt.Sprintf("Compliance Operator not ready after %s: %s", coInstallGrace, detail))
	case scan != nil && scan.Status == metav1.ConditionFalse && scan.Reason == "InvalidSchedule":
		setCond(cb, "Degraded", metav1.ConditionTrue, "InvalidSchedule", scan.Message)
	case storage != nil && storage.Status == metav1.ConditionFalse:
		// Fixed reason only: never copy storage.Reason (hand-edit can violate
		// Condition Reason pattern and brick Status().Update admission).
		setCond(cb, "Degraded", metav1.ConditionTrue, "ScanStorageNotReady", storage.Message)
	case plugin != nil && plugin.Status == metav1.ConditionFalse && plugin.Reason == "Unavailable":
		setCond(cb, "Degraded", metav1.ConditionTrue, "ConsolePluginUnavailable", plugin.Message)
	default:
		setCond(cb, "Degraded", metav1.ConditionFalse, "AsExpected", "")
	}
}
