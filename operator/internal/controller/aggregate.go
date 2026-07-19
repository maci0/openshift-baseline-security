package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func (r *ClusterBaselineReconciler) aggregateStatus(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	byProfile := map[baselinev1alpha1.ProfileKey]*baselinev1alpha1.ProfileStatus{}
	for _, key := range cb.Spec.Profiles {
		byProfile[key] = &baselinev1alpha1.ProfileStatus{Key: key, ProfileNames: key.ProfileNames()}
	}
	byTailored := map[string]*baselinev1alpha1.TailoredProfileStatus{}
	for _, name := range cb.Spec.TailoredProfiles {
		byTailored[name] = &baselinev1alpha1.TailoredProfileStatus{Name: name}
	}

	// List only ComplianceCheckResults for this baseline's suites. A full
	// namespace list pulls foreign scans (other bindings, leftover suites) and
	// multi-profile clusters often hold many thousands of CCRs; the suite
	// label is set by CO on every owned result.
	list := uList(checkResultGVK)
	suites := ownedSuites(cb)
	if len(suites) > 0 {
		suiteNames := slices.Sorted(maps.Keys(suites))
		req, err := labels.NewRequirement(suiteLabel, selection.In, suiteNames)
		if err != nil {
			return fmt.Errorf("building check-result suite selector: %w", err)
		}
		sel := labels.NewSelector().Add(*req)
		// Live apiserver LIST, not a cache read: unstructured objects bypass the
		// manager cache (cmd/main.go keeps the client default Unstructured=false),
		// so the label selector filters server-side and every reconcile pays one
		// fresh List. Do NOT flip the client to cached-unstructured to "fix"
		// this: MCPs/Subscriptions/Consoles are read via the same client with
		// get-only RBAC, and their informers could never start (no list/watch).
		if err := r.List(ctx, list, client.InNamespace(complianceNamespace),
			client.MatchingLabelsSelector{Selector: sel}); err != nil {
			if meta.IsNoMatchError(err) {
				// CRDs gone: do not leave a stale score/profile rollup on the CR.
				// Info once when we actually clear data (not every requeue while
				// CRDs stay missing): expected during CO uninstall, but a sudden
				// score drop must still be explainable from operator logs.
				hadRollup := cb.Status.Score != nil || len(cb.Status.Profiles) > 0 ||
					len(cb.Status.TailoredProfiles) > 0 || cb.Status.LastScanTime != nil ||
					len(cb.Status.History) > 0
				if hadRollup {
					log.FromContext(ctx).Info("compliance CRDs absent; cleared score/history rollup",
						"name", cb.Name)
				}
				cb.Status.Score = nil
				cb.Status.Profiles = nil
				cb.Status.TailoredProfiles = nil
				cb.Status.LastScanTime = nil
				cb.Status.NextScanTime = nil
				cb.Status.History = nil
				cb.Status.PreviousFailures = nil
				cb.Status.DiffBaseFailures = nil
				cb.Status.DiffBaseScanTime = nil
				cb.Status.NewlyFailed = nil
				cb.Status.Fixed = nil
				// Keep relatedObjects in sync with desired ownership even when CO is absent.
				cb.Status.RelatedObjects = relatedObjectsFromSuites(suites)
				return nil
			}
			return fmt.Errorf("listing ComplianceCheckResults in %s: %w", complianceNamespace, err)
		}
	}

	// Checks waived as accepted risk are pulled out of the pass/fail denominator
	// and reported in the Waived bucket, keyed by ComplianceCheckResult name.
	// Skip empty names so a corrupt entry cannot match every empty-named object.
	// An expired waiver no longer applies: the check is scored by its raw status.
	nowT := time.Now()
	waived := make(map[string]bool, len(cb.Spec.Waivers))
	for _, w := range cb.Spec.Waivers {
		if w.Name == "" {
			continue
		}
		if w.ExpiresAt != nil && !w.ExpiresAt.After(nowT) {
			continue
		}
		waived[w.Name] = true
	}

	var pass, fail int32
	var wPass, wFail int64 // severity-weighted totals (pooled)
	// Severity weights are only consumed when scoring.mode is SeverityWeighted;
	// skip per-result severity lookups and map churn on the default Flat path.
	weighted := cb.Spec.Scoring.Mode == baselinev1alpha1.ScoringSeverityWeighted
	var weights *scoreWeights
	if weighted {
		weights = &scoreWeights{
			profiles: make(map[baselinev1alpha1.ProfileKey]weightedSum, len(byProfile)),
			tailored: make(map[string]weightedSum, len(byTailored)),
		}
	}
	// tally routes one check result's status into the counts and the score.
	// INFO is counted (excluded from score) so Overview totals match Results.
	// SKIP is folded into NotApplicable (CO: check skipped for this system).
	// WAIVED is our synthetic status for accepted-risk checks (excluded from score).
	// Unknown/empty/corrupt status fails closed into ERROR so a CCR is never
	// silently dropped from ResultCounts (and metrics stay complete).
	tally := func(c *baselinev1alpha1.ResultCounts, status string) {
		switch status {
		case "PASS":
			c.Pass++
			pass++
		case "FAIL":
			c.Fail++
			fail++
		case "MANUAL":
			c.Manual++
		case "INFO":
			c.Info++
		case "ERROR":
			c.Error++
		case "INCONSISTENT":
			c.Inconsistent++
		case "WAIVED":
			c.Waived++
		case "SKIP", "NOT-APPLICABLE":
			c.NotApplicable++
		default:
			c.Error++
		}
	}
	// addWeight accumulates severity mass for the pooled score and the owning
	// profile/tailored bucket so per-profile history follows scoring.mode.
	// Only called when weighted is true (weights non-nil).
	addWeight := func(status string, item *unstructured.Unstructured, profileKey baselinev1alpha1.ProfileKey, tailoredName string, isTailored bool) {
		if status != "PASS" && status != "FAIL" {
			return
		}
		w := severityWeight(checkSeverity(item))
		pass := status == "PASS"
		if pass {
			wPass += w
		} else {
			wFail += w
		}
		if isTailored {
			s := weights.tailored[tailoredName]
			if pass {
				s.pass += w
			} else {
				s.fail += w
			}
			weights.tailored[tailoredName] = s
		} else {
			s := weights.profiles[profileKey]
			if pass {
				s.pass += w
			} else {
				s.fail += w
			}
			weights.profiles[profileKey] = s
		}
	}
	// Index range: avoid copying each Unstructured (map header + metadata) on
	// every iteration when multi-profile scans yield thousands of results.
	// Pre-size currentFails for typical fail rates so append does not thrash
	// under multi-profile FAIL-heavy scans (thousands of CCRs).
	currentFails := make([]string, 0, len(list.Items)/8+1)
	for i := range list.Items {
		item := &list.Items[i]
		// Single label key reads: GetLabels() copies the whole map per call and
		// would allocate once (or twice when weighted) per CCR each reconcile.
		suite := unstructuredLabel(item.Object, suiteLabel)
		// Route to the owning bucket first so weighting/regression only see owned checks.
		var rc *baselinev1alpha1.ResultCounts
		var profileKey baselinev1alpha1.ProfileKey
		var tailoredName string
		var isTailored bool
		if name, ok := tailoredNameFromSuite(suite); ok {
			if ts := byTailored[name]; ts != nil {
				rc = &ts.ResultCounts
				tailoredName = name
				isTailored = true
			}
		} else if key, ok := profileKeyFromSuite(suite); ok {
			if ps := byProfile[key]; ps != nil {
				rc = &ps.ResultCounts
				profileKey = key
			}
		}
		if rc == nil {
			continue
		}
		// Direct map read: NestedString path-walks every CCR (often thousands)
		// each reconcile. Wrong-type or missing status must not vanish from
		// counts (tally default maps empty/unknown to ERROR).
		status, _ := item.Object["status"].(string)
		// WAIVED is OUR synthetic status, assigned below for waived FAILs only.
		// A raw CCR claiming it (tampered object or a future CO status) must not
		// buy an accepted-risk slot with no spec.waivers entry: fail closed to
		// ERROR, matching the console's effectiveStatus fold for unknown tokens.
		if status == "WAIVED" {
			status = "ERROR"
		}
		// A check the Compliance Operator marks INCONSISTENT only because it does
		// not apply on some nodes (PASS where it applies, NOT-APPLICABLE elsewhere)
		// is benign; collapse it so it does not read as "review each". A real
		// PASS-vs-FAIL split stays INCONSISTENT.
		if status == "INCONSISTENT" {
			status = effectiveInconsistentStatus(item)
		}
		// Waivers apply to failing checks only: a waived FAIL is pulled out of
		// the pass/fail denominator into the Waived bucket. If a waived check later passes it
		// counts as PASS again (self-healing), so a stale waiver never silently
		// depresses the score; the admin can still remove it from the UI.
		//
		// Scan-diff (newlyFailed/fixed) still tracks the raw FAIL outcome: accepting
		// risk must not appear as Fixed or drop a regression. Score and ResultCounts
		// use the Waived bucket; previousFailures/diffBase use the FAIL name set.
		name := unstructuredName(item.Object)
		rawFail := status == "FAIL"
		if rawFail && name != "" && waived[name] {
			status = "WAIVED"
		}
		tally(rc, status)
		// Empty names never match waivers and must not enter scan-diff lists
		// (newlyFailed/fixed deep-links and alerts would be meaningless).
		if rawFail && name != "" {
			currentFails = append(currentFails, name)
		}
		if weighted {
			addWeight(status, item, profileKey, tailoredName, isTailored)
		}
	}
	slices.Sort(currentFails)

	// Preserve per-profile score history across the status.Profiles rebuild.
	profHist := map[baselinev1alpha1.ProfileKey][]baselinev1alpha1.ScoreSnapshot{}
	for _, p := range cb.Status.Profiles {
		profHist[p.Key] = p.History
	}
	tpHist := map[string][]baselinev1alpha1.ScoreSnapshot{}
	for _, tp := range cb.Status.TailoredProfiles {
		tpHist[tp.Name] = tp.History
	}
	cb.Status.Profiles = cb.Status.Profiles[:0]
	for _, key := range cb.Spec.Profiles {
		p := *byProfile[key]
		p.History = profHist[key]
		cb.Status.Profiles = append(cb.Status.Profiles, p)
	}
	cb.Status.TailoredProfiles = cb.Status.TailoredProfiles[:0]
	for _, name := range cb.Spec.TailoredProfiles {
		tp := *byTailored[name]
		tp.History = tpHist[name]
		cb.Status.TailoredProfiles = append(cb.Status.TailoredProfiles, tp)
	}
	// LastScanTime is tracked even when no score is computable (all MANUAL /
	// ERROR / NOT-APPLICABLE results) so completed scans stay visible.
	if weighted {
		cb.Status.Score = score64(wPass, wFail)
	} else {
		cb.Status.Score = score(pass, fail)
	}
	// Fill deterministic status fields before history so a scan-list failure
	// still leaves a coherent rollup on the error-path status update.
	// Reuse suites from the CCR selector (avoids a second ownedSuites alloc).
	cb.Status.RelatedObjects = relatedObjectsFromSuites(suites)
	// No profiles and no tailored profiles: scanning is intentionally off.
	// Clear live regression display and next-scan (nothing will fire without
	// bindings). Keep History, LastScanTime, and PreviousFailures so re-enable
	// does not invent a false regression storm against an empty baseline.
	if len(suites) == 0 {
		cb.Status.Score = nil
		cb.Status.NextScanTime = nil
		cb.Status.NewlyFailed = nil
		cb.Status.Fixed = nil
		return nil
	}
	cb.Status.NextScanTime = nextScanTime(cb.Spec.Schedule, time.Now())
	return r.recordHistory(ctx, cb, cb.Status.Score, currentFails, weights, suites)
}
