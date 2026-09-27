// Fuzz coverage for the sanitize layer: the only place where text read off a
// cluster object is turned into a status the apiserver will accept. Two
// targets, both differential against the real encoder rather than against a
// restatement of the clamp logic:
//
//   - FuzzJSONStringLenMatchesMarshal pins jsonStringLen to encoding/json byte
//     for byte. The failure-list budget is computed from it, so any drift is an
//     over-budget Status().Update, which freezes reconcile until an admin
//     hand-edits the object.
//   - FuzzSanitizeStatusUntrustedText drives the whole entry point with a hostile
//     status and asserts the CRD bounds sanitize exists to enforce. Asserting
//     them is what turns an admission rejection (a liveness bug the fuzzer can
//     see) into a caught failure.
package controller

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// FuzzJSONStringLenMatchesMarshal: jsonStringLen is a hand-written model of
// encoding/json's string encoder, used to size the failure-list budget without
// marshaling. A fuzzer that only ever saw valid UTF-8 would miss the escape
// classes and the U+FFFD coercion, so the seeds below walk every one of them and
// the input is raw bytes (ill-formed sequences included).
func FuzzJSONStringLenMatchesMarshal(f *testing.F) {
	for _, seed := range []string{
		"", "rule_1",
		"a&b<c>d", `quote"backslash\`,
		"tab\tnewline\ncr\rff\fbs\b", "nul\x00bell\x07esc\x1b",
		"sep\u2028para\u2029",
		"世界", "café", "café", "\U0001F600",
		"\xff", "a\xffb", "\xc3", "\xed\xa0\x80", "\xe2\x80", "\xf0\x9f",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal(%q) failed: %v", s, err)
		}
		if got, want := jsonStringLen(s), len(b); got != want {
			t.Fatalf("jsonStringLen(%q) = %d, json.Marshal wrote %d (%q)", s, got, want, b)
		}
	})
}

// Stream reader over the fuzz input: each field pulls one chunk, so a mutation
// in one field of the status does not reshuffle every other one.
type fuzzStream struct {
	data []byte
}

func (s *fuzzStream) take(n int) []byte {
	if len(s.data) == 0 {
		return nil
	}
	if n > len(s.data) {
		n = len(s.data)
	}
	out := s.data[:n]
	s.data = s.data[n:]
	return out
}

// one string per chunk, cut at NUL so arbitrary bytes stay in a single field.
func (s *fuzzStream) str() string {
	chunk := s.take(1 + len(s.data)/2)
	if i := bytes.IndexByte(chunk, 0); i >= 0 {
		chunk = chunk[:i]
	}
	return string(chunk)
}

func (s *fuzzStream) strs(maxItems int) []string {
	var items []string
	for len(s.data) > 0 && len(items) < maxItems {
		items = append(items, s.str())
	}
	return items
}

func (s *fuzzStream) count(max int) int {
	if len(s.data) == 0 {
		return 0
	}
	return int(s.take(1)[0]) % max
}

// hostileStatus builds a status whose every field is either a fuzzed string or
// a value the CRD forbids (negative counts, out-of-range scores, duplicate set
// members), the shape a hand-edited or restore-from-etcd object has.
func hostileStatus(s *fuzzStream) *baselinev1alpha1.ClusterBaseline {
	cb := &baselinev1alpha1.ClusterBaseline{}
	st := &cb.Status

	keys := []baselinev1alpha1.ProfileKey{
		"cis", "pci-dss", "nist-moderate", "nist-high", "stig", "nerc-cip",
		"e8", "bsi", "", "CIS", "cis ", baselinev1alpha1.ProfileKey(strings.Repeat("x", 60)),
	}
	for range s.count(statusProfilesMax*2 + 1) {
		st.Profiles = append(st.Profiles, baselinev1alpha1.ProfileStatus{
			Key:          keys[s.count(len(keys))],
			ProfileNames: s.strs(profileNamesMaxItems * 2),
			ResultCounts: hostileCounts(s),
			History:      hostileHistory(s),
		})
	}
	for range s.count(statusTailoredMax + 1) {
		st.TailoredProfiles = append(st.TailoredProfiles, baselinev1alpha1.TailoredProfileStatus{
			Name:         s.str(),
			ResultCounts: hostileCounts(s),
			History:      hostileHistory(s),
		})
	}
	for range s.count(relatedObjectsMax + 1) {
		st.RelatedObjects = append(st.RelatedObjects, baselinev1alpha1.ObjectRef{
			Group: s.str(), Resource: s.str(), Name: s.str(), Namespace: s.str(),
		})
	}
	st.NewlyFailed = s.strs(64)
	st.Fixed = s.strs(64)
	st.PreviousFailures = s.strs(64)
	st.DiffBaseFailures = s.strs(64)
	st.ComplianceOperatorVersion = s.str()
	if v := int32(s.count(150)) - 10; s.count(2) == 0 {
		st.Score = &v
	}
	for range s.count(statusConditionsFuzzMax) {
		st.Conditions = append(st.Conditions, metav1.Condition{
			Type:               s.str(),
			Status:             metav1.ConditionStatus(string(s.take(1))),
			Reason:             s.str(),
			Message:            s.str(),
			ObservedGeneration: int64(s.count(200)) - 20,
		})
	}
	if s.count(2) == 0 {
		st.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
			Phase:        s.str(),
			Pools:        s.strs(64),
			PauseOwner:   s.str(),
			Remediations: s.strs(64),
		}
	}
	return cb
}

// statusConditionsFuzzMax keeps the fuzzed condition list large enough to reach
// the conditionsSizeBudget trim without making a single iteration slow.
const statusConditionsFuzzMax = 12

func hostileCounts(s *fuzzStream) baselinev1alpha1.ResultCounts {
	n := func() int32 { return int32(s.count(255)) - 10 }
	return baselinev1alpha1.ResultCounts{
		Pass: n(), Fail: n(), Manual: n(), Info: n(), Error: n(),
		Inconsistent: n(), Waived: n(), NotApplicable: n(),
	}
}

func hostileHistory(s *fuzzStream) []baselinev1alpha1.ScoreSnapshot {
	out := make([]baselinev1alpha1.ScoreSnapshot, 0, s.count(baselinev1alpha1.HistoryMax+1))
	for range cap(out) {
		out = append(out, baselinev1alpha1.ScoreSnapshot{Score: int32(s.count(200)) - 20})
	}
	return out
}

// FuzzSanitizeStatusUntrustedText drives the whole status through the clamp
// layer with fuzzed strings in every name-shaped field (profileNames, tailored
// names, object refs, the four failure lists, condition type/reason/message,
// the remediation batch) and asserts the CRD bounds the apiserver enforces.
// FuzzSanitizeStatusForUpdate in matching_test.go covers the numeric side
// (scores, counts, ring lengths); this one covers the text side, plus the
// idempotence a second sanitize pass must have. Each assertion is one the
// sanitized object must satisfy for Status().Update to be accepted; a duplicate
// set member or an over-length name fails here rather than wedging reconcile on
// a live cluster.
func FuzzSanitizeStatusUntrustedText(f *testing.F) {
	f.Add(strings.Repeat("a", 600), "cis", "cluster", "rule_1")
	f.Add("", "", "", "")
	f.Add("a&b<c>d", "not-a-key", "\x00\x01", strings.Repeat("&", 253))

	f.Fuzz(func(t *testing.T, version, profileKey, name, entry string) {
		s := &fuzzStream{data: []byte(version + "\x00" + profileKey + "\x00" + name + "\x00" + entry)}
		cb := hostileStatus(s)
		// Pin the seed-driven fields the fuzzer cannot shape by itself.
		cb.Status.ComplianceOperatorVersion = version
		if cb.Status.Profiles == nil {
			cb.Status.Profiles = []baselinev1alpha1.ProfileStatus{{Key: baselinev1alpha1.ProfileKey(profileKey)}}
		}
		cb.Status.NewlyFailed = append(cb.Status.NewlyFailed, name, entry)

		sanitizeStatusForUpdate(cb)
		assertStatusAdmissible(t, cb)
		// A second pass over a copy must be a no-op: the reconciler sanitizes on
		// every write, so a clamp that is not a fixed point would either drop
		// data on each reconcile or keep rewriting the same object.
		again := cb.DeepCopy()
		sanitizeStatusForUpdate(again)
		firstJSON, err := json.Marshal(cb.Status)
		if err != nil {
			t.Fatalf("marshal sanitized status: %v", err)
		}
		secondJSON, err := json.Marshal(again.Status)
		if err != nil {
			t.Fatalf("marshal re-sanitized status: %v", err)
		}
		if string(firstJSON) != string(secondJSON) {
			t.Fatalf("sanitize is not idempotent:\n%s\n%s", firstJSON, secondJSON)
		}
	})
}

// assertStatusAdmissible encodes the CRD bounds sanitize.go exists to hold.
func assertStatusAdmissible(t *testing.T, cb *baselinev1alpha1.ClusterBaseline) {
	t.Helper()
	st := cb.Status

	if st.Score != nil && (*st.Score < 0 || *st.Score > 100) {
		t.Errorf("status.score = %d, outside the CRD 0-100 bounds", *st.Score)
	}
	if n := utf8.RuneCountInString(st.ComplianceOperatorVersion); n > complianceOperatorVersionMax {
		t.Errorf("status.complianceOperatorVersion is %d runes, over the 128 cap", n)
	}
	if len(st.History) > baselinev1alpha1.HistoryMax {
		t.Errorf("status.history has %d entries, over the 30 cap", len(st.History))
	}
	for i, snap := range st.History {
		if snap.Score < 0 || snap.Score > 100 {
			t.Errorf("status.history[%d].score = %d, outside the CRD 0-100 bounds", i, snap.Score)
		}
	}

	if len(st.Profiles) > statusProfilesMax {
		t.Errorf("status.profiles has %d rows, over the 16 cap", len(st.Profiles))
	}
	seenKeys := map[baselinev1alpha1.ProfileKey]struct{}{}
	for i, p := range st.Profiles {
		if !p.Key.Known() {
			t.Errorf("status.profiles[%d].key = %q, not in the CRD enum", i, p.Key)
		}
		if _, dup := seenKeys[p.Key]; dup {
			t.Errorf("status.profiles[%d] repeats key %q; listType=map rejects that", i, p.Key)
		}
		seenKeys[p.Key] = struct{}{}
		if len(p.ProfileNames) > profileNamesMaxItems {
			t.Errorf("status.profiles[%d].profileNames has %d entries, over the 16 cap", i, len(p.ProfileNames))
		}
		assertSetList(t, "profileNames", p.ProfileNames)
		if len(p.History) > baselinev1alpha1.HistoryMax {
			t.Errorf("status.profiles[%d].history has %d entries, over the 30 cap", i, len(p.History))
		}
		assertCounts(t, "profiles["+string(p.Key)+"]", p.ResultCounts)
	}

	if len(st.TailoredProfiles) > statusTailoredMax {
		t.Errorf("status.tailoredProfiles has %d rows, over the 32 cap", len(st.TailoredProfiles))
	}
	seenTailored := map[string]struct{}{}
	for i, tp := range st.TailoredProfiles {
		if tp.Name == "" || len(tp.Name) > tailoredNameMaxLen || len(utilvalidation.IsDNS1123Subdomain(tp.Name)) > 0 {
			t.Errorf("status.tailoredProfiles[%d].name = %q fails the CRD pattern or MaxLength", i, tp.Name)
		}
		if _, dup := seenTailored[tp.Name]; dup {
			t.Errorf("status.tailoredProfiles[%d] repeats name %q; listType=map rejects that", i, tp.Name)
		}
		seenTailored[tp.Name] = struct{}{}
		if len(tp.History) > baselinev1alpha1.HistoryMax {
			t.Errorf("status.tailoredProfiles[%d].history has %d entries, over the 30 cap", i, len(tp.History))
		}
		assertCounts(t, "tailoredProfiles["+tp.Name+"]", tp.ResultCounts)
	}

	if len(st.RelatedObjects) > relatedObjectsMax {
		t.Errorf("status.relatedObjects has %d entries, over the 64 cap", len(st.RelatedObjects))
	}
	for i, ref := range st.RelatedObjects {
		if ref.Resource == "" || ref.Name == "" {
			t.Errorf("status.relatedObjects[%d] has an empty required field", i)
		}
		for field, v := range map[string]int{
			"group": utf8.RuneCountInString(ref.Group), "resource": utf8.RuneCountInString(ref.Resource),
			"name": utf8.RuneCountInString(ref.Name), "namespace": utf8.RuneCountInString(ref.Namespace),
		} {
			max := objectRefFieldMaxLen
			if field == "namespace" {
				max = objectRefNSMaxLen
			}
			if v > max {
				t.Errorf("status.relatedObjects[%d].%s is %d runes, over the %d cap", i, field, v, max)
			}
		}
	}

	for field, list := range map[string][]string{
		"newlyFailed": st.NewlyFailed, "fixed": st.Fixed,
		"previousFailures": st.PreviousFailures, "diffBaseFailures": st.DiffBaseFailures,
	} {
		if len(list) > baselinev1alpha1.FailureListMax {
			t.Errorf("status.%s has %d entries, over the 4096 cap", field, len(list))
		}
		assertSetList(t, field, list)
	}

	seenTypes := map[string]struct{}{}
	for i, c := range st.Conditions {
		if c.Type == "" || len(c.Type) > conditionTypeMaxLen || !conditionTypePattern.MatchString(c.Type) {
			t.Errorf("status.conditions[%d].type = %q fails the CRD pattern or MaxLength", i, c.Type)
		}
		if _, dup := seenTypes[c.Type]; dup {
			t.Errorf("status.conditions[%d] repeats type %q; listMapKey=type rejects that", i, c.Type)
		}
		seenTypes[c.Type] = struct{}{}
		if c.Reason == "" || !conditionReasonPattern.MatchString(c.Reason) {
			t.Errorf("status.conditions[%d].reason = %q fails the CRD pattern", i, c.Reason)
		}
		if n := utf8.RuneCountInString(c.Reason); n > conditionReasonMaxLen {
			t.Errorf("status.conditions[%d].reason is %d runes, over the 1024 cap", i, n)
		}
		if n := utf8.RuneCountInString(c.Message); n > conditionMessageMaxLen {
			t.Errorf("status.conditions[%d].message is %d runes, over the 32768 cap", i, n)
		}
		switch c.Status {
		case metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown:
		default:
			t.Errorf("status.conditions[%d].status = %q is not in the CRD enum", i, c.Status)
		}
		if c.ObservedGeneration < 0 {
			t.Errorf("status.conditions[%d].observedGeneration = %d, under the CRD minimum", i, c.ObservedGeneration)
		}
		if c.LastTransitionTime.IsZero() {
			t.Errorf("status.conditions[%d].lastTransitionTime is zero; date-time is required", i)
		}
	}

	if b := cb.Status.RemediationBatch; b != nil {
		if b.Phase != baselinev1alpha1.RemediationBatchPhaseApplying {
			t.Errorf("status.remediationBatch.phase = %q is not in the CRD enum", b.Phase)
		}
		if b.StartedAt.IsZero() {
			t.Error("status.remediationBatch.startedAt is zero; date-time is required")
		}
		if len(b.Pools) > batchMaxPools {
			t.Errorf("status.remediationBatch.pools has %d entries, over the %d cap", len(b.Pools), batchMaxPools)
		}
		if len(b.Remediations) > batchMaxRemediations {
			t.Errorf("status.remediationBatch.remediations has %d entries, over the %d cap",
				len(b.Remediations), batchMaxRemediations)
		}
		assertSetList(t, "remediationBatch.pools", b.Pools)
		assertSetList(t, "remediationBatch.remediations", b.Remediations)
		if n := utf8.RuneCountInString(b.PauseOwner); n > objectRefFieldMaxLen {
			t.Errorf("status.remediationBatch.pauseOwner is %d runes, over the 253 cap", n)
		}
	}
}

// assertSetList pins the two listType=set bounds the apiserver enforces on a
// hand-written status: the per-item MaxLength and the absence of duplicates.
func assertSetList(t *testing.T, field string, list []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(list))
	for i, item := range list {
		if utf8.RuneCountInString(item) > objectRefFieldMaxLen {
			t.Errorf("status.%s[%d] is %d runes, over the 253 cap", field, i, utf8.RuneCountInString(item))
		}
		if _, dup := seen[item]; dup {
			t.Errorf("status.%s[%d] repeats %q; listType=set rejects that", field, i, item)
		}
		seen[item] = struct{}{}
	}
}

func assertCounts(t *testing.T, field string, c baselinev1alpha1.ResultCounts) {
	t.Helper()
	for name, v := range map[string]int32{
		"pass": c.Pass, "fail": c.Fail, "manual": c.Manual, "info": c.Info,
		"error": c.Error, "inconsistent": c.Inconsistent, "waived": c.Waived,
		"notApplicable": c.NotApplicable,
	} {
		if v < 0 {
			t.Errorf("status.%s.%s = %d, under the CRD Minimum=0", field, name, v)
		}
	}
}
