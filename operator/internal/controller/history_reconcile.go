package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// stampHistoryScoringMode records the scoring mode that wrote history ring
// points. Status-only updates do not persist metadata, so this patches
// annotations after a history write. Always updates the in-memory object;
// API patch is best-effort when the CR is named (unit tests often omit Name).
//
// RetryOnConflict + re-Get: a concurrent console patch (waiver, schedule,
// batch-apply) must not fail history stamping after rings were advanced in
// memory; without a stable mode stamp, late CCR refresh can rewrite snapshots
// under a flipped Flat/SeverityWeighted formula.
func (r *ClusterBaselineReconciler) stampHistoryScoringMode(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	mode := string(scoringMode(cb))
	if cb.Annotations[historyScoringModeAnn] == mode {
		return nil
	}
	// In-memory stamp first so this reconcile's historyModeMatches sees the mode
	// even when Name is empty (unit tests) or the API patch is skipped.
	if cb.Annotations == nil {
		cb.Annotations = map[string]string{historyScoringModeAnn: mode}
	} else {
		cb.Annotations = maps.Clone(cb.Annotations)
		cb.Annotations[historyScoringModeAnn] = mode
	}
	if cb.Name == "" || r.Client == nil {
		return nil
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &baselinev1alpha1.ClusterBaseline{}
		if err := r.Get(ctx, types.NamespacedName{Name: cb.Name}, latest); err != nil {
			return err
		}
		ann := maps.Clone(latest.GetAnnotations())
		if ann == nil {
			ann = map[string]string{}
		}
		if ann[historyScoringModeAnn] == mode {
			// Keep RV aligned for the end-of-reconcile Status().Update.
			cb.SetAnnotations(ann)
			cb.SetResourceVersion(latest.GetResourceVersion())
			return nil
		}
		before := latest.DeepCopy()
		ann[historyScoringModeAnn] = mode
		latest.SetAnnotations(ann)
		// OptimisticLock: concurrent console annotation patches (waiver/schedule/
		// batch-apply) must 409 so RetryOnConflict re-Gets rather than merging
		// onto a stale ResourceVersion and silently dropping the mode stamp.
		if err := r.Patch(ctx, latest, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
		cb.SetAnnotations(ann)
		cb.SetResourceVersion(latest.GetResourceVersion())
		return nil
	})
	if err != nil {
		// NotFound: mid-delete; in-memory stamp still guides this pass.
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return fmt.Errorf("stamping history scoring mode annotation: %w", err)
	}
	return nil
}

type completedSuiteRun struct {
	earliest time.Time
	latest   time.Time
}

// completedSuiteTimes returns the member-scan completion range only when the
// suite and every status entry are complete. ComplianceSuite is the transaction
// boundary for a ScanSettingBinding (ADR-015); recording an individual scan
// would snapshot a partial multi-scan run.
//
// Untrusted cluster data: avoid unstructured.NestedSlice, which DeepCopyJSON-
// panics on non-JSON types (e.g. int) that can appear in hand-built or partially
// converted objects. Direct map reads (no NestedString path walks) type-assert
// each field; wrong types fail closed as incomplete.
func completedSuiteTimes(suite *unstructured.Unstructured, now time.Time) (completedSuiteRun, bool) {
	statusObj, _ := suite.Object["status"].(map[string]any)
	if statusObj == nil {
		return completedSuiteRun{}, false
	}
	phase, _ := statusObj["phase"].(string)
	if phase != "DONE" {
		return completedSuiteRun{}, false
	}
	raw, ok := statusObj["scanStatuses"]
	if !ok {
		return completedSuiteRun{}, false
	}
	statuses, ok := raw.([]any)
	if !ok || len(statuses) == 0 {
		return completedSuiteRun{}, false
	}
	var run completedSuiteRun
	for _, entry := range statuses {
		status, ok := entry.(map[string]any)
		if !ok {
			return completedSuiteRun{}, false
		}
		memberPhase, _ := status["phase"].(string)
		if memberPhase != "DONE" {
			return completedSuiteRun{}, false
		}
		ts, _ := status["endTimestamp"].(string)
		completed, ok := parseScanEndTimestamp(ts, now)
		if !ok {
			return completedSuiteRun{}, false
		}
		if run.earliest.IsZero() || completed.Before(run.earliest) {
			run.earliest = completed
		}
		if completed.After(run.latest) {
			run.latest = completed
		}
	}
	return run, !run.latest.IsZero()
}

// recordHistory advances score history and scan-diff state when every owned
// suite has a completed run. weights may be nil (Flat mode, or unit tests that
// only care about overall history); per-profile rings then use pass/fail counts.
// suites may be nil (rebuild via ownedSuites); aggregateStatus passes its map so
// reconcile does not allocate the suite set twice.
//
// Suites are fetched by name (not a full namespace List) so foreign CO suites
// never enter the hot path; expected set size is profiles + tailored (small).
func (r *ClusterBaselineReconciler) recordHistory(
	ctx context.Context,
	cb *baselinev1alpha1.ClusterBaseline,
	s *int32,
	currentFails []string,
	// currentChecks is every evaluated check name this scan (any status), sorted.
	// The diff base is scoped to it so a check no longer scanned (a deselected
	// profile) is not reported as Fixed. Nil skips scoping (legacy/test callers).
	currentChecks []string,
	weights *scoreWeights,
	suites map[string]bool,
) error {
	expectedSuites := suites
	if expectedSuites == nil {
		expectedSuites = ownedSuites(cb)
	}
	if len(expectedSuites) == 0 {
		return nil
	}
	now := time.Now()
	var latest time.Time
	completedSuites := make(map[string]completedSuiteRun, len(expectedSuites))
	for name := range expectedSuites {
		item := u(suiteGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: complianceNamespace}, item); err != nil {
			if meta.IsNoMatchError(err) {
				// CRDs absent mid-history (partial CO uninstall). CCR list may
				// still have succeeded. Log only when a prior scan exists so a
				// frozen LastScanTime is explainable without requeue spam when
				// CO was never installed.
				if cb.Status.LastScanTime != nil {
					log.FromContext(ctx).Info("compliance suite CRDs absent; history not advanced",
						"suite", name, "name", cb.Name)
				}
				return nil
			}
			// Suite not created yet: wait for a full completed generation.
			// After a scan has completed, a missing suite freezes LastScanTime
			// and can page ComplianceScanStale; without this log there is no
			// operator-side marker (binding deleted, suite GC'd, name drift).
			// Pre-first-scan NotFound stays quiet (normal CO create lag).
			if apierrors.IsNotFound(err) {
				// Once LastScanTime is set, a missing suite freezes history and can
				// page ComplianceScanStale with no default-level marker. Rate-limit
				// Info (see logHistoryStall) so requeue spam stays off while still
				// leaving a production breadcrumb before the 36h alert.
				if cb.Status.LastScanTime != nil {
					r.logHistoryStall(ctx, "ComplianceSuite not found; history not advanced",
						"suite", name, "name", cb.Name,
						"lastScanTime", cb.Status.LastScanTime.UTC().Format(time.RFC3339))
				}
				return nil
			}
			return fmt.Errorf("getting ComplianceSuite %s/%s for history: %w", complianceNamespace, name, err)
		}
		completed, ok := completedSuiteTimes(item, now)
		if !ok {
			// DONE with unreadable/missing/far-future endTimestamps never
			// advances LastScanTime; ComplianceScanStale then pages with no
			// log marker. Incomplete (still running) suites stay quiet.
			// DONE with bad endTimestamps freezes LastScanTime until CO rewrites
			// status. Rate-limit Info so production sees the stall without
			// burying other signals on every requeue.
			if statusObj, _ := item.Object["status"].(map[string]any); statusObj != nil {
				if phase, _ := statusObj["phase"].(string); phase == "DONE" {
					r.logHistoryStall(ctx, "ComplianceSuite DONE but scan endTimestamps incomplete or invalid; history not advanced",
						"suite", name, "name", cb.Name)
				}
			}
			return nil
		}
		completedSuites[name] = completed
		if completed.latest.After(latest) {
			latest = completed.latest
		}
	}
	// Wait until every selected binding has a completed suite. This prevents a
	// fast profile from advancing global history while another is still running.
	if len(completedSuites) != len(expectedSuites) || latest.IsZero() {
		return nil
	}
	last := metav1.NewTime(latest)
	if cb.Status.LastScanTime != nil && !last.After(cb.Status.LastScanTime.Time) {
		// Never rewind LastScanTime when the suite with the newest endTimestamp
		// is dropped (profile/tp removed). On equal end time:
		// - refresh the latest history score when late results change the rollup
		// - append a first history point when an earlier pass had score=nil
		//   (all MANUAL/INFO) and a countable score appears for the same scan
		if last.Equal(cb.Status.LastScanTime) {
			// Late CCR rollups may rewrite the latest history point under the same
			// scoring formula. A Flat <-> SeverityWeighted flip must not recompute
			// completed-scan snapshots under the new weights (status.score still
			// updates in aggregateStatus; the next completed scan appends fresh).
			if historyModeMatches(cb) {
				cb.Status.History = syncHistorySnapshot(cb.Status.History, last, s)
				syncProfileHistory(cb, last, weights)
				if err := r.stampHistoryScoringMode(ctx, cb); err != nil {
					return err
				}
			}
			// Keep the baseline for the next scan current when CheckResults arrive
			// after endTimestamp, and correct this scan's diff against its retained
			// prior-scan baseline. Failure sets are mode-independent.
			if cb.Status.DiffBaseScanTime != nil && last.Equal(cb.Status.DiffBaseScanTime) {
				cb.Status.DiffBaseFailures = scopeToEvaluated(cb.Status.DiffBaseFailures, currentChecks)
				syncFailureDiff(cb, currentFails, cb.Status.DiffBaseFailures)
			}
			cb.Status.PreviousFailures = slices.Clone(currentFails)
		}
		return nil
	}
	if cb.Status.LastScanTime != nil {
		// A DONE suite may still represent the previous scheduled run while another
		// suite has already completed the next one. Advance only after every suite's
		// oldest (earliest) member scan is newer than the prior global snapshot, so
		// every member scan of every suite has moved past it.
		for _, completed := range completedSuites {
			if !completed.earliest.After(cb.Status.LastScanTime.Time) {
				return nil
			}
		}
	}
	hadPreviousScan := cb.Status.LastScanTime != nil
	cb.Status.LastScanTime = &last
	// Mode flip since rings were written: drop prior points so charts never mix
	// Flat and SeverityWeighted values. Late equal-scan refresh already refuses
	// rewrite when the stamp mismatches; without this, one new scan would stamp
	// the new mode and hide historyScoringModeMismatch while older points remain.
	if !historyModeMatches(cb) {
		clearHistoryRings(cb)
	}
	cb.Status.History = syncHistorySnapshot(cb.Status.History, last, s)
	// A new scan completed: compute regressions vs the previous scan's failures,
	// then snapshot the current failures for next time, and append a per-profile
	// history point so each benchmark can be trended.
	if hadPreviousScan {
		// Scope the base to still-evaluated checks: a check whose profile was
		// deselected since the prior scan is no longer scanned, and must not be
		// reported as Fixed (the disable-all path clears the diff entirely; this
		// is the partial-deselect analogue).
		cb.Status.DiffBaseFailures = scopeToEvaluated(cb.Status.PreviousFailures, currentChecks)
		cb.Status.DiffBaseScanTime = &last
		syncFailureDiff(cb, currentFails, cb.Status.DiffBaseFailures)
	} else {
		// There is no previous completed scan to compare against. Reporting every
		// initial failure as a regression is misleading and triggers a false alert.
		cb.Status.NewlyFailed = nil
		cb.Status.Fixed = nil
		cb.Status.DiffBaseFailures = nil
		cb.Status.DiffBaseScanTime = nil
	}
	cb.Status.PreviousFailures = slices.Clone(currentFails)
	syncProfileHistory(cb, last, weights)
	// Info once per completed generation (typically daily): default log level has
	// no other marker that LastScanTime advanced or that regressions were set.
	// Correlates ComplianceRegressions / ComplianceScanStale with operator logs.
	var scoreVal any
	if s != nil {
		scoreVal = *s
	}
	log.FromContext(ctx).Info("compliance scan completed",
		"name", cb.Name,
		"lastScanTime", last.UTC().Format(time.RFC3339),
		"score", scoreVal,
		"fails", len(currentFails),
		"newlyFailed", len(cb.Status.NewlyFailed),
		"fixed", len(cb.Status.Fixed),
		"firstScan", !hadPreviousScan,
	)
	// Stamp the mode that produced these snapshots so a later mode flip cannot
	// rewrite them via the equal-LastScanTime late-refresh path.
	return r.stampHistoryScoringMode(ctx, cb)
}

// historyStallLogInterval is how often default-level Info may repeat for a
// frozen LastScanTime (suite missing / bad endTimestamps). Shorter than the
// ComplianceScanStale 36h alert so on-call has breadcrumbs; longer than the
// 1m requeue so logs stay usable.
const historyStallLogInterval = 30 * time.Minute

// logHistoryStall always emits V(1), and Info at most every historyStallLogInterval.
// Use when history cannot advance after a completed scan so production default
// logs are not silent until ComplianceScanStale pages.
func (r *ClusterBaselineReconciler) logHistoryStall(ctx context.Context, msg string, keysAndValues ...any) {
	logger := log.FromContext(ctx)
	logger.V(1).Info(msg, keysAndValues...)
	if !r.lastHistoryStallLog.IsZero() && time.Since(r.lastHistoryStallLog) < historyStallLogInterval {
		return
	}
	r.lastHistoryStallLog = time.Now()
	logger.Info(msg, keysAndValues...)
}
