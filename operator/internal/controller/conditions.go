package controller

import (
	"context"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// CRD patterns for metav1.Condition (status.conditions items). Hand-edits that
// violate these freeze every Status().Update until fixed.
var (
	conditionReasonPattern = regexp.MustCompile(`^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`)
	conditionTypePattern   = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`)
)

const (
	conditionReasonMaxLen  = 1024
	conditionMessageMaxLen = 32768
	conditionTypeMaxLen    = 316
)

// failureListMax aliases the API constant so clamps stay CRD-aligned (ADR-013).
const failureListMax = baselinev1alpha1.FailureListMax

// CRD MaxItems / MaxLength mirrors for status fields sanitized below. Keep in
// lockstep with the kubebuilder markers on ClusterBaselineStatus.
const (
	statusProfilesMax    = 16
	statusTailoredMax    = 32
	relatedObjectsMax    = 64
	profileNamesMaxItems = 16
	tailoredNameMaxLen   = 51
	objectRefFieldMaxLen = 253
	objectRefNSMaxLen    = 63
	failureNameMaxLen    = 253
)

// sanitizeStatusForUpdate applies admission-safe bounds to status fields the
// reconciler writes so a hostile or stale status cannot brick updates.
// Per-profile and tailored history share the CRD MaxItems=30 / score [0,100]
// bounds with the top-level history ring. Failure-name lists share MaxItems=4096
// and items:MaxLength=253. Profiles / tailoredProfiles / relatedObjects share
// their CRD MaxItems, Enum, Pattern, and field MaxLength bounds.
func sanitizeStatusForUpdate(cb *baselinev1alpha1.ClusterBaseline) {
	cb.Status.History = clampHistory(cb.Status.History, historyMax)
	cb.Status.Score = clampScore(cb.Status.Score)
	sanitizeStatusProfiles(cb)
	sanitizeStatusTailoredProfiles(cb)
	sanitizeRelatedObjects(cb)
	cb.Status.NewlyFailed = clampFailureList(cb.Status.NewlyFailed)
	cb.Status.Fixed = clampFailureList(cb.Status.Fixed)
	cb.Status.PreviousFailures = clampFailureList(cb.Status.PreviousFailures)
	cb.Status.DiffBaseFailures = clampFailureList(cb.Status.DiffBaseFailures)
	// Each list above is within MaxItems=4096, but four full lists of long
	// (253-char) names serialize to ~4 MiB, over the apiserver ~1.5 MiB object
	// limit: Status().Update would be rejected and freeze reconcile on a large,
	// heavily-failing cluster. Bound the four lists together so the whole status
	// stays well under the limit (per-list MaxItems is not enough).
	clampFailureListsToBudget(&cb.Status.NewlyFailed, &cb.Status.Fixed,
		&cb.Status.PreviousFailures, &cb.Status.DiffBaseFailures)
	// complianceOperatorVersion is derived from a CSV name (object names allow up
	// to 253 chars), but the CRD caps the field at 128; clamp so a pathologically
	// long CSV name cannot fail Status().Update admission and freeze reconcile.
	cb.Status.ComplianceOperatorVersion = clampString(cb.Status.ComplianceOperatorVersion, complianceOperatorVersionMax)
	// status.remediationBatch has CRD MaxItems on pools (32) and remediations
	// (256), MaxLength on pauseOwner and list items, and Enum on phase. Hand-edits
	// or an old bug that overfilled the object must not brick every Status().Update.
	sanitizeRemediationBatch(cb)
	// Conditions carry required reason/status/type patterns. A single hostile
	// hand-edited condition freezes Status().Update even when rollups rewrite
	// Available/Progressing/Degraded, because the rest of the list is preserved.
	sanitizeStatusConditions(cb)
}

// sanitizeStatusConditions clamps every status.conditions entry to the CRD
// schema (reason pattern/minLength/maxLength, message maxLength, status Enum,
// type pattern). Drops conditions that cannot be repaired (invalid type).
func sanitizeStatusConditions(cb *baselinev1alpha1.ClusterBaseline) {
	in := cb.Status.Conditions
	if len(in) == 0 {
		return
	}
	out := make([]metav1.Condition, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for i := range in {
		c := in[i]
		// Type is the list map key; invalid type cannot be admitted.
		if c.Type == "" || len(c.Type) > conditionTypeMaxLen || !conditionTypePattern.MatchString(c.Type) {
			continue
		}
		// listType=map listMapKey=type: keep first of each type.
		if _, dup := seen[c.Type]; dup {
			continue
		}
		seen[c.Type] = struct{}{}
		switch c.Status {
		case metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown:
			// ok
		default:
			c.Status = metav1.ConditionUnknown
		}
		c.Reason = clampString(c.Reason, conditionReasonMaxLen)
		if c.Reason == "" || !conditionReasonPattern.MatchString(c.Reason) {
			c.Reason = "Unknown"
		}
		c.Message = clampString(c.Message, conditionMessageMaxLen)
		if c.LastTransitionTime.IsZero() {
			// Required date-time; zero fails OpenAPI format validation.
			c.LastTransitionTime = metav1.Now()
		}
		if c.ObservedGeneration < 0 {
			c.ObservedGeneration = 0
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		cb.Status.Conditions = nil
		return
	}
	cb.Status.Conditions = out
}

// sanitizeStatusProfiles clamps status.profiles to CRD MaxItems=16, drops rows
// with unknown ProfileKey (Enum), clamps profileNames and non-negative counts,
// and clamps per-row history. Unknown keys would fail admission; oversize lists
// would freeze every Status().Update after a hand-edit or bug.
func sanitizeStatusProfiles(cb *baselinev1alpha1.ClusterBaseline) {
	in := cb.Status.Profiles
	if len(in) == 0 {
		return
	}
	out := make([]baselinev1alpha1.ProfileStatus, 0, min(len(in), statusProfilesMax))
	seen := make(map[baselinev1alpha1.ProfileKey]struct{}, len(in))
	for i := range in {
		if len(out) >= statusProfilesMax {
			break
		}
		p := in[i]
		if !p.Key.Known() {
			continue
		}
		// listType=map listMapKey=key: keep first row per key so a hostile
		// duplicate-key hand-edit cannot leave an invalid map list.
		if _, dup := seen[p.Key]; dup {
			continue
		}
		seen[p.Key] = struct{}{}
		p.ProfileNames = clampStringList(p.ProfileNames, profileNamesMaxItems)
		p.History = clampHistory(p.History, historyMax)
		clampResultCounts(&p.ResultCounts)
		out = append(out, p)
	}
	if len(out) == 0 {
		cb.Status.Profiles = nil
		return
	}
	cb.Status.Profiles = out
}

// sanitizeStatusTailoredProfiles clamps status.tailoredProfiles to MaxItems=32,
// drops rows with empty / oversize / non-DNS1123 names (CRD Pattern + MaxLength=51),
// clamps counts and history. Invalid names brick Status().Update admission.
func sanitizeStatusTailoredProfiles(cb *baselinev1alpha1.ClusterBaseline) {
	in := cb.Status.TailoredProfiles
	if len(in) == 0 {
		return
	}
	out := make([]baselinev1alpha1.TailoredProfileStatus, 0, min(len(in), statusTailoredMax))
	seen := make(map[string]struct{}, len(in))
	for i := range in {
		if len(out) >= statusTailoredMax {
			break
		}
		tp := in[i]
		name := tp.Name
		if name == "" || len(name) > tailoredNameMaxLen || len(utilvalidation.IsDNS1123Subdomain(name)) > 0 {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		tp.Name = name
		tp.History = clampHistory(tp.History, historyMax)
		clampResultCounts(&tp.ResultCounts)
		out = append(out, tp)
	}
	if len(out) == 0 {
		cb.Status.TailoredProfiles = nil
		return
	}
	cb.Status.TailoredProfiles = out
}

// sanitizeRelatedObjects clamps status.relatedObjects to MaxItems=64 and each
// ObjectRef field to its CRD MaxLength. Drops entries missing required resource
// or name (MinLength=1) so a hand-edit cannot fail status admission.
func sanitizeRelatedObjects(cb *baselinev1alpha1.ClusterBaseline) {
	in := cb.Status.RelatedObjects
	if len(in) == 0 {
		return
	}
	out := make([]baselinev1alpha1.ObjectRef, 0, min(len(in), relatedObjectsMax))
	for i := range in {
		if len(out) >= relatedObjectsMax {
			break
		}
		ref := in[i]
		ref.Group = clampString(ref.Group, objectRefFieldMaxLen)
		ref.Resource = clampString(ref.Resource, objectRefFieldMaxLen)
		ref.Name = clampString(ref.Name, objectRefFieldMaxLen)
		ref.Namespace = clampString(ref.Namespace, objectRefNSMaxLen)
		if ref.Resource == "" || ref.Name == "" {
			continue
		}
		out = append(out, ref)
	}
	if len(out) == 0 {
		cb.Status.RelatedObjects = nil
		return
	}
	cb.Status.RelatedObjects = out
}

// clampResultCounts enforces CRD Minimum=0 on every ResultCounts field so a
// hand-edited negative tally cannot fail Status().Update admission.
func clampResultCounts(c *baselinev1alpha1.ResultCounts) {
	if c.Pass < 0 {
		c.Pass = 0
	}
	if c.Fail < 0 {
		c.Fail = 0
	}
	if c.Manual < 0 {
		c.Manual = 0
	}
	if c.Info < 0 {
		c.Info = 0
	}
	if c.Error < 0 {
		c.Error = 0
	}
	if c.Inconsistent < 0 {
		c.Inconsistent = 0
	}
	if c.Waived < 0 {
		c.Waived = 0
	}
	if c.NotApplicable < 0 {
		c.NotApplicable = 0
	}
}

// sanitizeRemediationBatch clamps status.remediationBatch to the CRD schema so
// an oversize or invalid batch cannot fail status admission and freeze reconcile
// (pools would stay paused with no successful status write path).
func sanitizeRemediationBatch(cb *baselinev1alpha1.ClusterBaseline) {
	b := cb.Status.RemediationBatch
	if b == nil {
		return
	}
	// Only Applying is in the Enum; anything else (including empty) is rewritten
	// so the object still admits while the wait path can force-resume.
	if b.Phase != baselinev1alpha1.RemediationBatchPhaseApplying {
		b.Phase = baselinev1alpha1.RemediationBatchPhaseApplying
	}
	// startedAt is required (date-time). A zero value JSON-marshals as null and
	// fails admission. Do not use Now(): that would restart batchResumeGrace and
	// leave MCPs paused longer after a hand-edit/corrupt zero. Epoch is non-zero
	// for OpenAPI and past grace (batchPastGrace already treats IsZero as past).
	if b.StartedAt.IsZero() {
		b.StartedAt = metav1.NewTime(time.Unix(0, 0).UTC())
	}
	b.PauseOwner = clampString(b.PauseOwner, 253)
	b.Pools = clampStringList(b.Pools, batchMaxPools)
	b.Remediations = clampStringList(b.Remediations, batchMaxRemediations)
}

// dedupeStable removes duplicate strings, keeping the first occurrence and the
// original order. Returns in unchanged when already unique so the common path
// (the operator's own writes never duplicate) does not allocate. CRD status
// set-lists (x-kubernetes-list-type: set) reject duplicates at admission, so
// sanitize must strip any that reach it from restored or migrated etcd, or every
// Status().Update would fail and freeze reconcile.
func dedupeStable(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	dup := false
	for _, s := range in {
		if _, ok := seen[s]; ok {
			dup = true
			break
		}
		seen[s] = struct{}{}
	}
	if !dup {
		return in
	}
	clear(seen)
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// clampStringList truncates each entry to objectRefFieldMaxLen (253) runes,
// drops empties, dedupes, then keeps at most maxItems entries. Every status
// set-list item it clamps (profile names, MCP pool names, remediation names) is
// a DNS-1123 / object-ref name bounded at 253 in the CRD. The per-item clamp runs
// BEFORE the dedupe so two over-length names sharing a 253-rune prefix (only
// possible from corrupt/restored etcd) cannot collapse to the same string after
// dedup and re-introduce a set-list duplicate that fails admission.
func clampStringList(in []string, maxItems int) []string {
	if len(in) == 0 {
		return in
	}
	clamped := make([]string, 0, len(in))
	for _, s := range in {
		if s = clampString(s, objectRefFieldMaxLen); s != "" {
			clamped = append(clamped, s)
		}
	}
	clamped = dedupeStable(clamped)
	if maxItems > 0 && len(clamped) > maxItems {
		clamped = clamped[:maxItems]
	}
	if len(clamped) == 0 {
		return nil
	}
	return clamped
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

// clampFailureList clamps each name to CRD items:MaxLength=253, dedupes, then
// trims to failureListMax (keeps the prefix). nil stays nil. The per-item clamp
// runs BEFORE the dedupe so two over-length names sharing a 253-rune prefix (only
// possible from corrupt/restored etcd, which bypasses the CRD MaxLength) cannot
// truncate to the same string after dedup and re-introduce a set-list duplicate
// that would fail Status().Update admission.
func clampFailureList(in []string) []string {
	if len(in) == 0 {
		return in
	}
	clamped := make([]string, len(in))
	for i, s := range in {
		clamped[i] = clampString(s, failureNameMaxLen)
	}
	clamped = dedupeStable(clamped)
	if len(clamped) > failureListMax {
		clamped = clamped[:failureListMax]
	}
	return clamped
}

// failureListsSizeBudget bounds the combined serialized size of the four status
// failure-name lists. The apiserver/etcd object limit is ~1.5 MiB; reserve the
// rest of the budget for history rings, per-profile counts, relatedObjects,
// conditions, spec, and metadata.
const failureListsSizeBudget = 768 * 1024

// clampFailureListsToBudget trims the given failure-name lists together so their
// combined serialized size (name + JSON quoting/comma overhead) stays under
// failureListsSizeBudget. It repeatedly drops the tail of whichever list is
// currently largest, so no single list dominates and the whole status cannot
// exceed the apiserver object-size limit and freeze Status().Update. Truncating
// the tails degrades the diff on an extreme cluster (some regressions/fixes drop
// out) but keeps reconcile alive, which a frozen status write would not.
func clampFailureListsToBudget(lists ...*[]string) {
	const perEntryOverhead = 3 // two quotes + a comma
	sizes := make([]int, len(lists))
	total := 0
	for i, l := range lists {
		s := 0
		for _, name := range *l {
			s += len(name) + perEntryOverhead
		}
		sizes[i] = s
		total += s
	}
	for total > failureListsSizeBudget {
		largest := -1
		for i := range lists {
			if len(*lists[i]) > 0 && (largest < 0 || sizes[i] > sizes[largest]) {
				largest = i
			}
		}
		if largest < 0 {
			return // all empty; nothing left to trim
		}
		l := lists[largest]
		removed := len((*l)[len(*l)-1]) + perEntryOverhead
		*l = (*l)[:len(*l)-1]
		sizes[largest] -= removed
		total -= removed
	}
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
	// Reject pre-epoch timestamps (corrupt / skewed CO clock): no real scan
	// predates 1970, and a negative Unix value would pin LastScanTime and poison
	// the age-based ComplianceScanStale alert. Symmetric with the far-future guard.
	if t.Before(time.Unix(0, 0)) {
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
