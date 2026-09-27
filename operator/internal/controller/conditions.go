// Condition layer: writing, reading, and rolling up status.conditions.
// Everything here either sets a condition or decides what the top-level
// Available/Progressing/Degraded rollups read. The clamping that keeps a
// condition admissible for the apiserver lives in sanitize.go.
package controller

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

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
	// Snapshot before the write: FindStatusCondition returns a pointer into the
	// slice and SetStatusCondition mutates that entry in place, so reading prev
	// afterwards would compare the new status against itself and a changed
	// reason on an already-False condition would never log.
	prevStatus, prevReason, hadPrev := prevCondState(cb, typ)
	setCond(cb, typ, metav1.ConditionFalse, reason, msg)
	if !hadPrev || prevStatus != metav1.ConditionFalse || prevReason != reason {
		log.FromContext(ctx).Info(logMsg, keysAndValues...)
	}
}

// setCondTrueLogRecovered sets a True detail condition and Info-logs only when
// the condition recovers from a previously False state. setCondFalseLogOnce
// covers entering a failure; without this the recovery is invisible in default
// logs, so a Degraded alert that clears leaves no breadcrumb pairing the
// resolution with the earlier failure line. Steady True re-asserts stay silent.
func setCondTrueLogRecovered(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, typ, reason, msg, logMsg string, keysAndValues ...any) {
	prevStatus, _, hadPrev := prevCondState(cb, typ)
	setCond(cb, typ, metav1.ConditionTrue, reason, msg)
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
// detail conditions (ClusterOperator-style rollups). now is the reconciler's
// clock reading, so every grace period here is measured on the injected clock
// rather than the wall clock. The install-stall grace is the one comparison
// whose other side is not: meta.SetStatusCondition stamps a new
// LastTransitionTime from the wall clock, so a simulated run measures the CO
// condition's real transition time against its virtual now.
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
	coStuck := co != nil && co.Status == metav1.ConditionFalse &&
		(co.Reason == "Installing" || co.Reason == "CSVNotReady") &&
		!co.LastTransitionTime.IsZero() &&
		now.Sub(co.LastTransitionTime.Time) > coInstallGrace
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
