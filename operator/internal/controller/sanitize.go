// Sanitizing layer: every value the reconciler writes into status is first
// clamped to the CRD schema (MaxItems, MaxLength, Enum, Pattern, Minimum) and
// to an aggregate serialized-size budget. A hostile, hand-edited, or
// restore-from-etcd status that reaches Status().Update unclamped fails
// admission and freezes every later write, which wedges reconcile outright.
// The clamp helpers (dedupeStable, clampSetList, jsonStringLen) and the
// CRD-bound constants are here, not in conditions.go, because every
// sanitize* entry point depends on them and none of them writes a condition.
//
// Every bound below mirrors a kubebuilder marker on ClusterBaselineStatus or
// its items; changing one means changing the other, and the CRD copy in
// bundle/manifests/ with it.
package controller

import (
	"encoding/json"
	"regexp"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"

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
	// conditionsSizeBudget bounds the aggregate serialized size of
	// status.conditions. Per-entry clamps alone leave the LIST unbounded: enough
	// hand-written pattern-valid types with 32 KiB messages push the object past
	// the ~1.5 MiB apiserver/etcd cap and permanently freeze Status().Update
	// (same failure mode failureListsSizeBudget prevents for the failure lists;
	// 768 KiB there + 256 KiB here leaves ample headroom for the rest). Entries
	// are accounted at their actual JSON-marshaled size, so multi-byte runes and
	// escape inflation cannot slip past the budget. The operator's own 7 types
	// always fit: their messages are re-clamped to the 1024-byte condMessage cap
	// the operator itself writes under, ~3 KiB serialized each worst case.
	conditionsSizeBudget = 256 * 1024
)

// operatorConditionTypes are the condition types this operator writes. They are
// always kept under conditionsSizeBudget; only foreign (hand-edited / other
// controller) types are dropped when the budget is exceeded.
var operatorConditionTypes = map[string]struct{}{
	"Available": {}, "Progressing": {}, "Degraded": {},
	"ComplianceOperatorReady": {}, "ScanConfigured": {},
	"ScanStorageReady": {}, "ConsolePluginReady": {},
}

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
		// Operator-owned types never legitimately exceed the condMessage byte
		// cap the operator itself writes under; re-clamping here keeps the
		// budget's reservation for them small even if hand-edited. Reconcile
		// overwrites these with real content anyway; only an early-error path
		// could otherwise carry a hand-edited 32 KiB message into the write.
		if _, own := operatorConditionTypes[c.Type]; own {
			c.Message = condMessage(c.Message)
		}
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
	// Aggregate-size budget: operator-owned types are reserved first, then
	// foreign types keep their original order while the budget allows. Under
	// normal operation everything fits and the list is unchanged.
	condSize := func(c *metav1.Condition) int {
		// Actual marshaled size: an additive estimate undercounts multi-byte
		// runes and JSON escaping (control chars inflate up to 6x).
		if b, err := json.Marshal(c); err == nil {
			return len(b)
		}
		return len(c.Type) + len(c.Reason) + len(c.Message) + 128
	}
	used := 0
	for i := range out {
		if _, own := operatorConditionTypes[out[i].Type]; own {
			used += condSize(&out[i])
		}
	}
	kept := out[:0]
	for i := range out {
		if _, own := operatorConditionTypes[out[i].Type]; own {
			kept = append(kept, out[i])
			continue
		}
		s := condSize(&out[i])
		if used+s > conditionsSizeBudget {
			continue
		}
		used += s
		kept = append(kept, out[i])
	}
	cb.Status.Conditions = kept
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
	b.PauseOwner = clampString(b.PauseOwner, objectRefFieldMaxLen)
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
// a DNS-1123 / object-ref name bounded at 253 in the CRD.
func clampStringList(in []string, maxItems int) []string {
	return clampSetList(in, objectRefFieldMaxLen, maxItems, true)
}

// clampSetList is the shared core of clampStringList and clampFailureList:
// truncate each entry to nameMax runes, optionally drop empties, keep the first
// occurrence of duplicates in original order, then trim to maxItems. The
// per-item clamp runs BEFORE the dedupe so two over-length names sharing a
// nameMax-rune prefix (only possible from corrupt/restored etcd, which bypasses
// the CRD MaxLength) cannot collapse to the same string after truncation and
// re-introduce a set-list duplicate that fails admission.
func clampSetList(in []string, nameMax, maxItems int, dropEmpty bool) []string {
	if len(in) == 0 {
		return in
	}
	clamped := make([]string, 0, len(in))
	for _, s := range in {
		if s = clampString(s, nameMax); s != "" || !dropEmpty {
			clamped = append(clamped, s)
		}
	}
	clamped = dedupeStable(clamped)
	if maxItems > 0 && len(clamped) > maxItems {
		clamped = clamped[:maxItems]
	}
	if dropEmpty && len(clamped) == 0 {
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
// trims to failureListMax (keeps the prefix). nil stays nil. Unlike
// clampStringList, empty entries are kept: failure list items have a CRD
// MaxLength but no MinLength, so an empty name is admissible and dropping it
// would silently rewrite (not just clamp) a hostile status.
func clampFailureList(in []string) []string {
	return clampSetList(in, failureNameMaxLen, failureListMax, false)
}

// failureListsSizeBudget bounds the combined serialized size of the four status
// failure-name lists. The apiserver/etcd object limit is ~1.5 MiB; reserve the
// rest of the budget for history rings, per-profile counts, relatedObjects,
// conditions, spec, and metadata.
const failureListsSizeBudget = 768 * 1024

// failureListShareBudget is failureListsSizeBudget split four ways, the share
// one failure-name list may use without the other three pushing the status over
// the limit. The current FAIL set is truncated to it BEFORE it is diffed and
// stored as the baseline (see recordHistory): all four lists are subsets of that
// one truncated set, so the budget holds by construction and the scan diff can
// never report a check as a regression that the budget simply hid.
const failureListShareBudget = failureListsSizeBudget / 4

// jsonStringLen returns the exact number of bytes encoding/json writes for the
// string s, without allocating. It must stay equal to len(json.Marshal(s)) for
// a string under the Go toolchain go.mod pins, and
// FuzzJSONStringLenMatchesMarshal pins that against the real encoder over
// every escape class below, including ill-formed UTF-8, which the encoder
// coerces to U+FFFD rather than failing.
//
// An additive len(name)+constant estimate is wrong by up to 6x on names full of
// '&', '<', '>' or control characters, which are exactly the names a hostile or
// buggy upstream status carries: json.Marshal escapes them to the 6-byte
// \u00XX form, so a budget computed from raw byte length admits a list up to
// 6x over the limit, which is the apiserver-freeze case the budget exists to
// prevent. The same reasoning is spelled out for conditions in condSize.
func jsonStringLen(s string) int {
	n := len(s) + 2 // surrounding quotes
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == '\b':
			// Two-byte escape form (\", \\, \n, \r, \t, \f, \b) replaces one byte.
			// Every other control character takes the 6-byte \u00XX form below.
			n++
		case c < 0x20 || c == '<' || c == '>' || c == '&':
			// \u00XX: six bytes replace one.
			n += 5
		case c == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9):
			// U+2028 / U+2029 LINE SEPARATOR, PARAGRAPH SEPARATOR: valid JSON,
			// escaped by encoding/json because they terminate a JavaScript line.
			// Three bytes in, six out.
			n += 3
			i += 2
		case c >= utf8.RuneSelf:
			// A well-formed rune is copied verbatim, so skip its trailing bytes.
			// An ill-formed one is one byte in and the three-byte U+FFFD
			// replacement rune out: the encoder coerces rather than failing, and
			// a status restored from a protobuf backup can carry lone
			// continuation bytes, which a one-byte count under-budgets.
			// encoding/json used to escape these as the six-byte \ufffd form;
			// FuzzJSONStringLenMatchesMarshal pins this branch against the real
			// encoder, so a toolchain that changes the form again fails here
			// rather than under-budgeting the clamp by 3x.
			_, size := utf8.DecodeRuneInString(s[i:])
			if size == 1 {
				n += 2
				break
			}
			i += size - 1
		}
	}
	return n
}

// clampFailureListsToBudget trims the given failure-name lists together so their
// combined serialized size stays under failureListsSizeBudget.
func clampFailureListsToBudget(lists ...*[]string) {
	trimFailureListsToBudget(failureListsSizeBudget, lists...)
}

// clampFailureListToShare trims one failure-name list to the per-list share of
// failureListsSizeBudget. The scan-diff baseline is trimmed with this before it
// is used, so every list the diff writes is a subset of one already-trimmed set
// and the shared-budget trim above never has to drop a name the diff reported.
func clampFailureListToShare(list *[]string) {
	trimFailureListsToBudget(failureListShareBudget, list)
}

// trimFailureListsToBudget trims the given failure-name lists together so their
// combined serialized size (each name as json.Marshal would write it, plus the
// separating comma) stays under budget. It repeatedly drops the tail of whichever
// list is currently largest, so no single list dominates and the whole status
// cannot exceed the apiserver object-size limit and freeze Status().Update.
// Truncating the tails degrades the diff on an extreme cluster (some
// regressions/fixes drop out) but keeps reconcile alive, which a frozen status
// write would not.
func trimFailureListsToBudget(budget int, lists ...*[]string) {
	const perEntryOverhead = 1 // the comma separating entries
	sizes := make([]int, len(lists))
	total := 0
	for i, l := range lists {
		s := 0
		for _, name := range *l {
			s += jsonStringLen(name) + perEntryOverhead
		}
		sizes[i] = s
		total += s
	}
	for total > budget {
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
		// Same accounting as the size pass above: an escaped name releases more
		// than its byte length, and a raw len here would subtract too little and
		// trim entries the budget can still hold.
		removed := jsonStringLen((*l)[len(*l)-1]) + perEntryOverhead
		*l = (*l)[:len(*l)-1]
		sizes[largest] -= removed
		total -= removed
	}
}
