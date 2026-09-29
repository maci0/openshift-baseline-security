// Condition layer: writing, reading, and rolling up status.conditions.
// Everything here either sets a condition or decides what the top-level
// Available/Progressing/Degraded rollups read. The clamping that keeps a
// condition admissible for the apiserver lives in sanitize.go.
package controller

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// graceMinutes renders a grace period as whole minutes for a status message.
// Exact integer arithmetic on the Duration, not int(d.Minutes()): Minutes
// returns a float64 and the conversion truncates toward zero, so a grace that is
// not a whole number of minutes renders SHORTER than the window the code
// actually applies (a 90s grace prints "Pending >1m"). Ceiling keeps the
// message at or above the real window, which is the safe direction for a human
// reading "stuck for >Nm": rounding down would have them check before the
// condition could fire. A non-positive grace reports 0.
func graceMinutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	m := d / time.Minute
	if d%time.Minute != 0 {
		m++
	}
	return int(m)
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

// setCond writes one status condition, stamping LastTransitionTime from now
// (the reconciler's clock reading) rather than letting meta.SetStatusCondition
// stamp the wall clock: the install-stall and plugin-unavailable graces below
// measure elapsed time against that stamp, so a wall-clock write would compare
// real elapsed time with a simulated now. meta.SetStatusCondition keeps an
// existing stamp while the status is unchanged, so passing a zero time when
// now is zero preserves its behavior exactly.
func setCond(cb *baselinev1alpha1.ClusterBaseline, now time.Time, typ string, status metav1.ConditionStatus, reason, msg string) {
	// Reason is required (minLength 1) and pattern-constrained on the CRD.
	// Never write empty: a hand-edited detail condition with Reason "" would
	// otherwise brick Status().Update when rolled up into Degraded.
	if reason == "" {
		reason = "Unknown"
	}
	cond := metav1.Condition{
		Type:   typ,
		Status: status,
		Reason: reason,
		// Cap every message: InvalidSchedule embeds user cron text; storage
		// embeds PVC names; wrap errors can be huge. One path keeps status
		// updates from failing admission on an oversized message.
		Message:            condMessage(msg),
		ObservedGeneration: cb.Generation,
	}
	if !now.IsZero() {
		cond.LastTransitionTime = metav1.NewTime(now)
	}
	meta.SetStatusCondition(&cb.Status.Conditions, cond)
}

// setCondFalseLogOnce sets a False detail condition and Info-logs only when the
// condition first enters this (False, reason) state, so a sticky failure does
// not spam the default log on every requeue. keysAndValues are structured log
// fields. Shared by the storage/schedule/plugin not-ready paths.
func setCondFalseLogOnce(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, now time.Time, typ, reason, msg, logMsg string, keysAndValues ...any) {
	// Snapshot before the write: FindStatusCondition returns a pointer into the
	// slice and SetStatusCondition mutates that entry in place, so reading prev
	// afterwards would compare the new status against itself and a changed
	// reason on an already-False condition would never log.
	prevStatus, prevReason, hadPrev := prevCondState(cb, typ)
	setCond(cb, now, typ, metav1.ConditionFalse, reason, msg)
	if !hadPrev || prevStatus != metav1.ConditionFalse || prevReason != reason {
		log.FromContext(ctx).Info(logMsg, keysAndValues...)
	}
}

// setCondTrueLogRecovered sets a True detail condition and Info-logs only when
// the condition recovers from a previously False state. setCondFalseLogOnce
// covers entering a failure; without this the recovery is invisible in default
// logs, so a Degraded alert that clears leaves no breadcrumb pairing the
// resolution with the earlier failure line. Steady True re-asserts stay silent.
func setCondTrueLogRecovered(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, now time.Time, typ, reason, msg, logMsg string, keysAndValues ...any) {
	prevStatus, _, hadPrev := prevCondState(cb, typ)
	setCond(cb, now, typ, metav1.ConditionTrue, reason, msg)
	if hadPrev && prevStatus != metav1.ConditionTrue {
		log.FromContext(ctx).Info(logMsg, keysAndValues...)
	}
}

// prevCondState copies a condition's status and reason by value. The pointer
// FindStatusCondition returns aliases the slice entry that SetStatusCondition
// overwrites, so any transition guard must read a copy taken before the write.
func prevCondState(cb *baselinev1alpha1.ClusterBaseline, typ string) (status metav1.ConditionStatus, reason string, found bool) {
	c := meta.FindStatusCondition(cb.Status.Conditions, typ)
	if c == nil {
		return "", "", false
	}
	return c.Status, c.Reason, true
}

// condIsTrue is true when c is present (non-nil) and True. One definition of
// "condition is True" so call sites cannot drift on the nil guard.
func condIsTrue(c *metav1.Condition) bool {
	return c != nil && c.Status == metav1.ConditionTrue
}

// statusUnchanged reports whether a reconcile derived exactly the status the
// apiserver already holds, so the Status().Update can be skipped. The poll
// re-derives the same rollup every time nothing on the cluster moved, and the
// status carries four failure lists budgeted to 768 KiB, so the unconditional
// write cost a large PUT through admission, structural-schema validation,
// set-uniqueness checking, and etcd on every tick.
//
// The history scoring-mode stamp is part of the decision: recordHistory advances
// it in memory before the rings it guards are durable, so a status that compares
// equal but carries an advanced stamp still has to be written, or the stamp
// would never persist.
func statusUnchanged(persisted *baselinev1alpha1.ClusterBaselineStatus, derived *baselinev1alpha1.ClusterBaselineStatus, stamped, preReconcileMode string) bool {
	return stamped == preReconcileMode && equality.Semantic.DeepEqual(persisted, derived)
}

// condTrue is true when the named status condition is present and True.
func condTrue(cb *baselinev1alpha1.ClusterBaseline, typ string) bool {
	return condIsTrue(meta.FindStatusCondition(cb.Status.Conditions, typ))
}

// condFalseWith reports whether c is present, False, and (when reasons is
// non-empty) carries one of them. One definition of "detail condition is False
// for this reason", so the rollup cases cannot drift on the nil guard.
func condFalseWith(c *metav1.Condition, reasons ...string) bool {
	if c == nil || c.Status != metav1.ConditionFalse {
		return false
	}
	return len(reasons) == 0 || slices.Contains(reasons, c.Reason)
}

// Detail reasons that mean work is still in flight, not a permanent admin action
// (Manual NotInstalled). Steady states are deliberately absent: ImageMissing /
// ImageInvalid is a permanent deployment misconfig, ConsoleMissing is the
// Console capability disabled, and CRDsMissing is the common state under
// installComplianceOperator=Manual until the admin installs CO (Automatic is
// already Progressing through Installing/CSVNotReady on ComplianceOperatorReady).
var progressingReasons = []string{"Installing", "CSVNotReady", "WaitingForPods"}

// coInstallStuckReasons are the progressing reasons that mean the Compliance
// Operator install itself has not finished, the subset coInstallGrace applies to.
var coInstallStuckReasons = []string{"Installing", "CSVNotReady"}

// conditionProgressing is true for non-terminal False detail reasons that mean
// work is still in flight.
func conditionProgressing(c *metav1.Condition) bool {
	return condFalseWith(c, progressingReasons...)
}

// setRollupConditions sets Available, Progressing, and Degraded from the
// detail conditions (ClusterOperator-style rollups). now is the reconciler's
// clock reading, so every grace period here is measured on the injected clock
// rather than the wall clock, and it is the same reading setCond stamps on a
// new LastTransitionTime, so a simulated run compares virtual against virtual.
func setRollupConditions(cb *baselinev1alpha1.ClusterBaseline, now time.Time) {
	co := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	scan := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	plugin := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	storage := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")

	coReady := condIsTrue(co)
	scanOK := condIsTrue(scan)
	// A Compliance Operator install that never becomes ready (bad catalog source,
	// unresolvable Subscription) would otherwise Progress + fast-poll forever. Past
	// a grace window, stop treating it as progress so it rolls up to Degraded and
	// the poll backs off, mirroring the console plugin's Unavailable-past-grace.
	coStuck := condFalseWith(co, coInstallStuckReasons...) &&
		!co.LastTransitionTime.IsZero() &&
		now.Sub(co.LastTransitionTime.Time) > coInstallGrace
	progressing := (conditionProgressing(co) && !coStuck) ||
		conditionProgressing(scan) || conditionProgressing(plugin)

	if progressing {
		setCond(cb, now, "Progressing", metav1.ConditionTrue, "Reconciling", "installing or configuring dependencies")
	} else {
		setCond(cb, now, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	}
	if coReady && scanOK {
		setCond(cb, now, "Available", metav1.ConditionTrue, "AsExpected", "compliance operator ready and scans configured")
	} else {
		setCond(cb, now, "Available", metav1.ConditionFalse, "NotReady", "waiting for compliance operator and scan configuration")
	}
	// Degraded: persistent failures that are not mere installation progress:
	// failed Compliance Operator CSV, invalid schedule, scan result storage
	// wedged, or the plugin down past its grace period.
	// Use fixed CamelCase reasons (never copy a possibly empty/hostile detail
	// Reason) so status admission cannot fail on Reason pattern/minLength.
	switch {
	case condFalseWith(co, "CSVFailed"):
		setCond(cb, now, "Degraded", metav1.ConditionTrue, "CSVFailed", co.Message)
	case coStuck:
		// Prefer the detail message; fall back to reason so we never end with a
		// trailing empty ": ".
		detail := co.Message
		if detail == "" {
			detail = co.Reason
		}
		setCond(cb, now, "Degraded", metav1.ConditionTrue, "InstallStalled",
			fmt.Sprintf("Compliance Operator not ready after %dm: %s", graceMinutes(coInstallGrace), detail))
	case condFalseWith(scan, "InvalidSchedule"):
		setCond(cb, now, "Degraded", metav1.ConditionTrue, "InvalidSchedule", scan.Message)
	case condFalseWith(storage):
		// Fixed reason only: never copy storage.Reason (hand-edit can violate
		// Condition Reason pattern and brick Status().Update admission).
		setCond(cb, now, "Degraded", metav1.ConditionTrue, "ScanStorageNotReady", storage.Message)
	case condFalseWith(plugin, "Unavailable"):
		setCond(cb, now, "Degraded", metav1.ConditionTrue, "ConsolePluginUnavailable", plugin.Message)
	default:
		setCond(cb, now, "Degraded", metav1.ConditionFalse, "AsExpected", "")
	}
}
