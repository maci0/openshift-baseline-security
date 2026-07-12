package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

func TestBindingName(t *testing.T) {
	for _, key := range []baselinev1alpha1.ProfileKey{"cis", "pci-dss", "e8", ""} {
		got := bindingName(key)
		want := "baseline-" + string(key)
		if got != want {
			t.Errorf("bindingName(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestNotIn pins set semantics shared with sortedDiff: unique sorted members
// of a that are absent from b (duplicate names in a count once).
func TestNotIn(t *testing.T) {
	empty := map[string]bool{}
	got := notIn([]string{"a", "a", "b", "c", "b"}, empty)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("unique empty-b = %v, want [a b c]", got)
	}
	got = notIn([]string{"c", "a", "a", "b"}, map[string]bool{"a": true, "d": true})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("unique with b = %v, want [b c]", got)
	}
	if got := notIn(nil, empty); len(got) != 0 {
		t.Fatalf("nil a = %v, want empty", got)
	}
	if got := notIn([]string{"x", "x"}, map[string]bool{"x": true}); len(got) != 0 {
		t.Fatalf("all excluded = %v, want empty", got)
	}
}

func TestOwnedSuites(t *testing.T) {
	if len(ownedSuites(&baselinev1alpha1.ClusterBaseline{})) != 0 {
		t.Fatal("empty profiles should yield empty suites")
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles:         []baselinev1alpha1.ProfileKey{"cis", "stig"},
			TailoredProfiles: []string{"custom", "cis-local"},
		},
	}
	s := ownedSuites(cb)
	// Built-in + tailored suites feed aggregate, batch eligibility, and scanconfig.
	if !s["baseline-cis"] || !s["baseline-stig"] ||
		!s["baseline-tp-custom"] || !s["baseline-tp-cis-local"] || len(s) != 4 {
		t.Fatalf("%v", s)
	}
	// Tailored-only baseline still owns its suites (no built-in profiles).
	tpOnly := ownedSuites(&baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{TailoredProfiles: []string{"only"}},
	})
	if !tpOnly["baseline-tp-only"] || len(tpOnly) != 1 {
		t.Fatalf("tailored-only ownedSuites = %v", tpOnly)
	}
}

func TestProfileKeyFromSuite(t *testing.T) {
	cases := []struct {
		suite string
		key   baselinev1alpha1.ProfileKey
		ok    bool
	}{
		{"baseline-cis", "cis", true},
		{"baseline-pci-dss", "pci-dss", true},
		{"baseline-", "", false},          // prefix alone is not a key
		{"baseline-tp-custom", "", false}, // tailored suites are not profile keys
		{"other-suite", "", false},
		{"", "", false},
		{"Baseline-cis", "", false}, // case-sensitive
	}
	for _, c := range cases {
		key, ok := profileKeyFromSuite(c.suite)
		if ok != c.ok || (ok && key != c.key) {
			t.Errorf("profileKeyFromSuite(%q) = (%q,%v), want (%q,%v)", c.suite, key, ok, c.key, c.ok)
		}
	}
}

// TestSuiteRoutingDualMap: aggregate routes tailored first (tailoredNameFromSuite
// before profileKeyFromSuite). A baseline-tp-* suite must never also parse as a
// profile key, or dual byProfile/byTailored maps would double-count / clobber.
func TestSuiteRoutingDualMap(t *testing.T) {
	// Tailored suite: only the tailored parser accepts it.
	name, tok := tailoredNameFromSuite("baseline-tp-custom")
	key, pok := profileKeyFromSuite("baseline-tp-custom")
	if !tok || name != "custom" {
		t.Fatalf("tailoredNameFromSuite(baseline-tp-custom) = (%q,%v), want (custom,true)", name, tok)
	}
	if pok {
		t.Fatalf("profileKeyFromSuite(baseline-tp-custom) ok, would clobber tailored bucket with key %q", key)
	}
	// Built-in suite: only the profile parser accepts it.
	name, tok = tailoredNameFromSuite("baseline-cis")
	key, pok = profileKeyFromSuite("baseline-cis")
	if tok {
		t.Fatalf("tailoredNameFromSuite(baseline-cis) ok with name %q, want reject", name)
	}
	if !pok || key != "cis" {
		t.Fatalf("profileKeyFromSuite(baseline-cis) = (%q,%v), want (cis,true)", key, pok)
	}
	// Empty / malformed prefixes reject both sides (no silent default bucket).
	for _, suite := range []string{"", "baseline-", "baseline-tp-", "other"} {
		if _, ok := tailoredNameFromSuite(suite); ok {
			t.Errorf("tailoredNameFromSuite(%q) unexpectedly ok", suite)
		}
		if _, ok := profileKeyFromSuite(suite); ok {
			t.Errorf("profileKeyFromSuite(%q) unexpectedly ok", suite)
		}
	}
	// Dual ownership map: both suite labels coexist without key collision.
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles:         []baselinev1alpha1.ProfileKey{"cis"},
			TailoredProfiles: []string{"custom"},
		},
	}
	owned := ownedSuites(cb)
	if !owned["baseline-cis"] || !owned["baseline-tp-custom"] {
		t.Fatalf("ownedSuites = %v, want baseline-cis and baseline-tp-custom", owned)
	}
	if len(owned) != 2 {
		t.Fatalf("ownedSuites size = %d, want 2 (no clobber)", len(owned))
	}
}

func TestMatchesAnyProfile(t *testing.T) {
	profiles := map[string]bool{"ocp4-cis": true, "ocp4-cis-node": true, "custom": true, "ocp4": true}
	for name, want := range map[string]bool{
		"ocp4-cis":             true,
		"ocp4-cis-node-worker": true,
		"ocp4-cis-node-master": true,
		"custom-worker":        true,
		"ocp4-worker":          true,
		// Ambiguous base must not swallow foreign profile PVCs.
		"ocp4-cis-extra": false,
		"ocp4-pci-dss":   false,
		"ocp4-cisx":      false,
		"custom-extra":   false,
		"":               false,
	} {
		if got := matchesAnyProfile(name, profiles); got != want {
			t.Errorf("matchesAnyProfile(%q) = %v, want %v", name, got, want)
		}
	}
	if matchesAnyProfile("x", nil) || matchesAnyProfile("x", map[string]bool{}) {
		t.Fatal("empty profiles must never match")
	}
}

func TestWithoutPlugin(t *testing.T) {
	in := []string{"a", pluginName, "b", pluginName}
	got := withoutPlugin(in, pluginName)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
	// Input must not be mutated.
	if len(in) != 4 {
		t.Fatalf("input mutated: %v", in)
	}
	if len(withoutPlugin([]string{"x"}, pluginName)) != 1 {
		t.Fatal("untouched when absent")
	}
	if len(withoutPlugin(nil, pluginName)) != 0 {
		t.Fatal("nil input")
	}
}

func TestAppendHistoryRing(t *testing.T) {
	var hist []baselinev1alpha1.ScoreSnapshot
	for i := 0; i < 35; i++ {
		hist = appendHistoryRing(hist, metav1.NewTime(time.Unix(int64(i), 0)), int32(i), 30)
	}
	if len(hist) != 30 {
		t.Fatalf("len = %d", len(hist))
	}
	if hist[0].Score != 5 || hist[29].Score != 34 {
		t.Fatalf("ring contents: first=%d last=%d", hist[0].Score, hist[29].Score)
	}
	// max <= 0 means no trim
	h := appendHistoryRing(nil, metav1.Now(), 1, 0)
	if len(h) != 1 {
		t.Fatal(h)
	}
}

func TestClampHistory(t *testing.T) {
	var hist []baselinev1alpha1.ScoreSnapshot
	for i := 0; i < 40; i++ {
		hist = append(hist, baselinev1alpha1.ScoreSnapshot{
			Time: metav1.NewTime(time.Unix(int64(i), 0)), Score: int32(i),
		})
	}
	got := clampHistory(hist, 30)
	if len(got) != 30 || got[0].Score != 10 || got[29].Score != 39 {
		t.Fatalf("clampHistory = len %d first=%d last=%d", len(got), got[0].Score, got[29].Score)
	}
	if len(clampHistory(hist[:5], 30)) != 5 {
		t.Fatal("short history should be unchanged")
	}
	// Out-of-range scores must be clamped so CRD Maximum/Minimum cannot lock
	// out Status().Update (same class of bug as over-length history).
	bad := []baselinev1alpha1.ScoreSnapshot{
		{Score: -5}, {Score: 150}, {Score: 50},
	}
	fixed := clampHistory(bad, 30)
	if fixed[0].Score != 0 || fixed[1].Score != 100 || fixed[2].Score != 50 {
		t.Fatalf("score sanitize = %v,%v,%v", fixed[0].Score, fixed[1].Score, fixed[2].Score)
	}
}

// TestClearHistoryRings: Flat <-> SeverityWeighted flips must drop all score
// rings so MiniTrend / charts never mix formulas (ADR-008). Integration
// coverage exists on the reconcile path; pin the pure helper so a no-op
// regression cannot hide behind status-update mocks.
func TestClearHistoryRings(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{
		Status: baselinev1alpha1.ClusterBaselineStatus{
			History: []baselinev1alpha1.ScoreSnapshot{{Score: 90}, {Score: 91}},
			Profiles: []baselinev1alpha1.ProfileStatus{{
				Key:     "cis",
				History: []baselinev1alpha1.ScoreSnapshot{{Score: 80}},
			}},
			TailoredProfiles: []baselinev1alpha1.TailoredProfileStatus{{
				Name:    "custom",
				History: []baselinev1alpha1.ScoreSnapshot{{Score: 70}, {Score: 71}},
			}},
		},
	}
	clearHistoryRings(cb)
	if cb.Status.History != nil {
		t.Fatalf("overall history still set: %v", cb.Status.History)
	}
	if len(cb.Status.Profiles) != 1 || cb.Status.Profiles[0].History != nil {
		t.Fatalf("profile history not cleared: %+v", cb.Status.Profiles)
	}
	if len(cb.Status.TailoredProfiles) != 1 || cb.Status.TailoredProfiles[0].History != nil {
		t.Fatalf("tailored history not cleared: %+v", cb.Status.TailoredProfiles)
	}
	// Empty / nil rings are a no-op (must not panic on first-scan baselines).
	clearHistoryRings(&baselinev1alpha1.ClusterBaseline{})
	clearHistoryRings(cb)
}

func TestSanitizeStatusForUpdate(t *testing.T) {
	badScore := int32(200)
	cb := &baselinev1alpha1.ClusterBaseline{
		Status: baselinev1alpha1.ClusterBaselineStatus{
			Score: &badScore,
			History: []baselinev1alpha1.ScoreSnapshot{
				{Score: -1}, {Score: 999},
			},
			// Per-profile history shares MaxItems/score bounds; omit sanitize
			// and a hand-edit bricks Status().Update the same way.
			Profiles: []baselinev1alpha1.ProfileStatus{{
				Key: "cis",
				History: []baselinev1alpha1.ScoreSnapshot{
					{Score: -3}, {Score: 150},
				},
			}},
			TailoredProfiles: []baselinev1alpha1.TailoredProfileStatus{{
				Name: "custom",
				History: []baselinev1alpha1.ScoreSnapshot{
					{Score: 200},
				},
			}},
		},
	}
	// Pad history past MaxItems.
	for i := 0; i < 40; i++ {
		cb.Status.History = append(cb.Status.History, baselinev1alpha1.ScoreSnapshot{Score: int32(i % 50)})
		cb.Status.Profiles[0].History = append(cb.Status.Profiles[0].History,
			baselinev1alpha1.ScoreSnapshot{Score: int32(i % 50)})
	}
	// Pad failure-name lists past CRD MaxItems=4096.
	for i := 0; i < failureListMax+10; i++ {
		name := fmt.Sprintf("chk-%d", i)
		cb.Status.NewlyFailed = append(cb.Status.NewlyFailed, name)
		cb.Status.Fixed = append(cb.Status.Fixed, name)
		cb.Status.PreviousFailures = append(cb.Status.PreviousFailures, name)
		cb.Status.DiffBaseFailures = append(cb.Status.DiffBaseFailures, name)
	}
	sanitizeStatusForUpdate(cb)
	if cb.Status.Score == nil || *cb.Status.Score != 100 {
		t.Fatalf("score = %v, want 100", cb.Status.Score)
	}
	if len(cb.Status.History) != historyMax {
		t.Fatalf("history len = %d, want %d", len(cb.Status.History), historyMax)
	}
	for _, h := range cb.Status.History {
		if h.Score < 0 || h.Score > 100 {
			t.Fatalf("history score out of range: %d", h.Score)
		}
	}
	if len(cb.Status.Profiles[0].History) != historyMax {
		t.Fatalf("profile history len = %d, want %d", len(cb.Status.Profiles[0].History), historyMax)
	}
	for _, h := range cb.Status.Profiles[0].History {
		if h.Score < 0 || h.Score > 100 {
			t.Fatalf("profile history score out of range: %d", h.Score)
		}
	}
	if h := cb.Status.TailoredProfiles[0].History; len(h) != 1 || h[0].Score != 100 {
		t.Fatalf("tailored history = %+v, want score 100", h)
	}
	for _, list := range [][]string{
		cb.Status.NewlyFailed, cb.Status.Fixed,
		cb.Status.PreviousFailures, cb.Status.DiffBaseFailures,
	} {
		if len(list) != failureListMax {
			t.Fatalf("failure list len = %d, want %d", len(list), failureListMax)
		}
	}
}

func TestParseScanEndTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	ok, valid := parseScanEndTimestamp("2026-07-09T01:00:00Z", now)
	if !valid || !ok.Equal(time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("basic RFC3339: %v %v", ok, valid)
	}
	_, valid = parseScanEndTimestamp("2026-07-09T01:00:00.123456789Z", now)
	if !valid {
		t.Fatal("fractional seconds should parse")
	}
	if _, valid = parseScanEndTimestamp("", now); valid {
		t.Fatal("empty should fail")
	}
	if _, valid = parseScanEndTimestamp("not-a-time", now); valid {
		t.Fatal("garbage should fail")
	}
	// Far future must not pin LastScanTime.
	far := now.Add(48 * time.Hour).UTC().Format(time.RFC3339)
	if _, valid = parseScanEndTimestamp(far, now); valid {
		t.Fatal("far-future endTimestamp must be rejected")
	}
	// Modest skew still accepted.
	skew := now.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	if _, valid = parseScanEndTimestamp(skew, now); !valid {
		t.Fatal("near-future within 1h should be accepted")
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

func TestProfileNames(t *testing.T) {
	keys := baselinev1alpha1.AllProfileKeys()
	// Profiles MaxItems=8 and the kubebuilder Enum must stay in lockstep with AllProfileKeys.
	if len(keys) != 8 {
		t.Fatalf("AllProfileKeys len = %d, want 8 (Profiles MaxItems)", len(keys))
	}
	for _, k := range keys {
		if !k.Known() {
			t.Errorf("AllProfileKeys entry %q not Known()", k)
		}
		names := k.ProfileNames()
		if len(names) == 0 {
			t.Errorf("ProfileNames(%q) empty", k)
		}
		for _, n := range names {
			if !strings.HasPrefix(n, "ocp4-") && !strings.HasPrefix(n, "rhcos4-") {
				t.Errorf("ProfileNames(%q): %q has unexpected prefix", k, n)
			}
		}
	}
	if baselinev1alpha1.ProfileKey("nope").ProfileNames() != nil {
		t.Error("unknown key should return nil")
	}
	if baselinev1alpha1.ProfileKey("nope").Known() {
		t.Error("unknown key should not be Known()")
	}
	if baselinev1alpha1.HistoryMax != 30 {
		t.Fatalf("HistoryMax = %d, want 30 (CRD MaxItems)", baselinev1alpha1.HistoryMax)
	}
	// CRD kubebuilder defaults must stay in lockstep with API constants used at reconcile.
	if baselinev1alpha1.DefaultScanSchedule != "0 1 * * *" {
		t.Fatalf("DefaultScanSchedule = %q, want CRD schedule default", baselinev1alpha1.DefaultScanSchedule)
	}
	if baselinev1alpha1.DefaultComplianceCatalogSource != "redhat-operators" {
		t.Fatalf("DefaultComplianceCatalogSource = %q, want CRD catalog default", baselinev1alpha1.DefaultComplianceCatalogSource)
	}
	if baselinev1alpha1.RemediationBatchPhaseApplying != "Applying" {
		t.Fatalf("RemediationBatchPhaseApplying = %q", baselinev1alpha1.RemediationBatchPhaseApplying)
	}
}

func FuzzBindingNameRoundTrip(f *testing.F) {
	for _, seed := range []string{"cis", "pci-dss", "", "x", "baseline-cis"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, key string) {
		suite := bindingName(baselinev1alpha1.ProfileKey(key))
		if !strings.HasPrefix(suite, "baseline-") {
			t.Fatalf("bindingName(%q) = %q", key, suite)
		}
		got, ok := profileKeyFromSuite(suite)
		// Empty profile keys are invalid; profileKeyFromSuite rejects "baseline-".
		if key == "" {
			if ok {
				t.Fatal("empty key must not round-trip")
			}
			return
		}
		if !ok || string(got) != key {
			t.Fatalf("round-trip %q -> %q -> (%q,%v)", key, suite, got, ok)
		}
	})
}

func FuzzProfileKeyFromSuite(f *testing.F) {
	for _, seed := range []string{"baseline-cis", "baseline-", "other", "", "baseline-baseline-x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, suite string) {
		key, ok := profileKeyFromSuite(suite)
		// Must match production: reject empty remainder and tailored "tp-" prefix.
		rest, has := strings.CutPrefix(suite, "baseline-")
		wantOK := has && rest != "" && !strings.HasPrefix(rest, "tp-")
		if ok != wantOK {
			t.Fatalf("ok = %v want %v for %q", ok, wantOK, suite)
		}
		if ok && "baseline-"+string(key) != suite {
			t.Fatalf("key %q does not round-trip to %q", key, suite)
		}
		if ok && key == "" {
			t.Fatal("empty key must not be ok")
		}
	})
}

func FuzzMatchesAnyProfile(f *testing.F) {
	profiles := map[string]bool{"ocp4-cis": true, "ocp4-cis-node": true, "rhcos4-e8": true}
	for _, seed := range []string{"ocp4-cis", "ocp4-cis-node-master", "ocp4-cisx", "", "-", "ocp4-cis-", "ocp4-cis-extra"} {
		f.Add(seed)
	}
	// Independent oracle: enumerate the exact suffixes accepted after "<profile>-".
	// Do NOT call scanRoleSuffix here, or a wrong allow-list would agree on both
	// sides and pass. Keep this set in sync with scanRoleSuffix in matching.go.
	validSuffix := map[string]bool{
		"worker": true, "master": true, "control-plane": true, "infra": true, "node": true,
		"node-worker": true, "node-master": true, "node-control-plane": true, "node-infra": true,
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := matchesAnyProfile(name, profiles)
		want := false
		for p := range profiles {
			if name == p {
				want = true
				break
			}
			if rest, ok := strings.CutPrefix(name, p+"-"); ok && validSuffix[rest] {
				want = true
				break
			}
		}
		if got != want {
			t.Fatalf("matchesAnyProfile(%q) = %v, want %v", name, got, want)
		}
	})
}

func FuzzWithoutPlugin(f *testing.F) {
	f.Add("a,b,c", "b")
	f.Add("", "x")
	f.Add("x", "x")
	f.Fuzz(func(t *testing.T, csv, drop string) {
		var in []string
		if csv != "" {
			in = strings.Split(csv, ",")
		}
		origLen := len(in)
		got := withoutPlugin(in, drop)
		if len(in) != origLen {
			t.Fatal("input mutated")
		}
		for _, p := range got {
			if p == drop {
				t.Fatalf("drop %q still present in %v", drop, got)
			}
		}
		for _, p := range got {
			found := false
			for _, o := range in {
				if o == p {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("extra element %q", p)
			}
		}
	})
}

func FuzzAppendHistoryRing(f *testing.F) {
	f.Add(int32(1), 30, 40)
	f.Add(int32(0), 0, 5)
	f.Add(int32(99), 1, 100)
	f.Add(int32(-5), 30, 10) // out-of-range scores must clamp
	f.Add(int32(150), 30, 10)
	f.Fuzz(func(t *testing.T, s int32, max, n int) {
		if n < 0 {
			n = -n
		}
		n = n % 200 // keep runtime bounded
		if max < 0 {
			max = -max
		}
		max = max % 100
		var hist []baselinev1alpha1.ScoreSnapshot
		for i := 0; i < n; i++ {
			hist = appendHistoryRing(hist, metav1.NewTime(time.Unix(int64(i), 0)), s, max)
		}
		if max > 0 && len(hist) > max {
			t.Fatalf("len %d > max %d", len(hist), max)
		}
		if max <= 0 && len(hist) != n {
			t.Fatalf("no-trim len %d want %d", len(hist), n)
		}
		// clampHistory always sanitizes scores so Status().Update admission cannot
		// fail on a hand-edited or buggy out-of-range value.
		for _, h := range hist {
			if h.Score < 0 || h.Score > 100 {
				t.Fatalf("history score %d out of [0,100]", h.Score)
			}
		}
	})
}

func FuzzProfileNames(f *testing.F) {
	for _, seed := range []string{"cis", "nope", "", "stig", "pci-dss"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, key string) {
		names := baselinev1alpha1.ProfileKey(key).ProfileNames()
		// Must never panic; unknown keys return nil.
		for _, n := range names {
			if n == "" {
				t.Fatal("empty profile name")
			}
		}
	})
}

// sign collapses a comparator result to -1/0/1 for property assertions.
func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

// FuzzComplianceCSVVersion feeds arbitrary CSV names into the version parser:
// it must never panic, and a parsed version's parts must all be non-negative.
func FuzzComplianceCSVVersion(f *testing.F) {
	for _, seed := range []string{
		"", "compliance-operator.v1.6.0", "compliance-operator.v1.6.0-rc1",
		"compliance-operator.v1.6.0+build.9", "compliance-operator.v", "junk",
		"compliance-operator.v-1", "compliance-operator.v99999999999999999999.0",
		"compliance-operator.v1.2.3.4.5", "compliance-operator.v1..2",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		v, ok := complianceCSVVersion(name)
		if !ok {
			return
		}
		for _, p := range v.parts {
			if p < 0 {
				t.Fatalf("negative version part %d from %q", p, name)
			}
		}
	})
}

// FuzzCompareComplianceCSVVersion pins the ordering as a total order: reflexive,
// antisymmetric, and transitive for arbitrary CSV name strings. Transitivity is
// the invariant the max-selection at clusterbaseline_controller.go:429 relies on;
// a non-transitive comparator can pin the operator to the wrong CSV version.
func FuzzCompareComplianceCSVVersion(f *testing.F) {
	f.Add("compliance-operator.v1.6.0", "compliance-operator.v1.6.1", "compliance-operator.v1.6.2")
	f.Add("compliance-operator.v1.6.0-rc1", "compliance-operator.v1.6.0", "compliance-operator.v1.6.0+b2")
	f.Add("junk", "compliance-operator.v1.0.0", "compliance-operator.v2.0.0")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		if got := compareComplianceCSVVersion(a, a); got != 0 {
			t.Fatalf("compare(a,a)=%d for %q, want 0", got, a)
		}
		if ab, ba := compareComplianceCSVVersion(a, b), compareComplianceCSVVersion(b, a); sign(ab) != -sign(ba) {
			t.Fatalf("not antisymmetric: compare(%q,%q)=%d vs compare(b,a)=%d", a, b, ab, ba)
		}
		ab := sign(compareComplianceCSVVersion(a, b))
		bc := sign(compareComplianceCSVVersion(b, c))
		ac := sign(compareComplianceCSVVersion(a, c))
		if ab <= 0 && bc <= 0 && ac > 0 {
			t.Fatalf("not transitive: %q<=%q<=%q but compare(a,c)=%d", a, b, c, ac)
		}
		if ab >= 0 && bc >= 0 && ac < 0 {
			t.Fatalf("not transitive: %q>=%q>=%q but compare(a,c)=%d", a, b, c, ac)
		}
	})
}

// FuzzTailoredNameFromSuite: never panics, and a name it accepts round-trips
// through tailoredBindingName.
func FuzzTailoredNameFromSuite(f *testing.F) {
	for _, seed := range []string{"", "baseline-tp-", "baseline-tp-x", "baseline-cis", "baseline-tp-a-b", "tp-x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, suite string) {
		name, ok := tailoredNameFromSuite(suite)
		if !ok {
			return
		}
		if name == "" {
			t.Fatalf("accepted empty tailored name from %q", suite)
		}
		if got := tailoredBindingName(name); got != suite {
			t.Fatalf("round-trip: tailoredBindingName(%q)=%q, want %q", name, got, suite)
		}
	})
}

// FuzzNextScanTime: an arbitrary (untrusted spec.schedule) string must never
// panic; it either parses to a future time or returns nil.
func FuzzNextScanTime(f *testing.F) {
	for _, seed := range []string{"", "0 1 * * *", "*/5 * * * *", "@daily", "not a cron", "0 1 * * * * *", "61 0 * * *"} {
		f.Add(seed)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, schedule string) {
		next := nextScanTime(schedule, now)
		if next != nil && next.Time.Before(now) {
			t.Fatalf("nextScanTime(%q) returned a past time %v", schedule, next.Time)
		}
	})
}

// FuzzParseScanEndTimestamp: ComplianceScan status.endTimestamp is untrusted
// cluster data. Must never panic; accept only parseable times within 1h skew.
func FuzzParseScanEndTimestamp(f *testing.F) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for _, seed := range []string{
		"", "not-a-time", "2026-07-09T01:00:00Z", "2026-07-09T01:00:00.123456789Z",
		now.Add(30 * time.Minute).UTC().Format(time.RFC3339),
		now.Add(48 * time.Hour).UTC().Format(time.RFC3339),
		"2026-07-10T12:00:00+00:00", "0001-01-01T00:00:00Z",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, ts string) {
		got, ok := parseScanEndTimestamp(ts, now)
		if !ok {
			return
		}
		if got.After(now.Add(time.Hour)) {
			t.Fatalf("accepted far-future timestamp %q -> %v", ts, got)
		}
		// Re-parse must agree (canonical RFC3339 forms only).
		if _, ok2 := parseScanEndTimestamp(got.UTC().Format(time.RFC3339Nano), now); !ok2 {
			t.Fatalf("accepted %q but reformatted value rejected", ts)
		}
	})
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

// FuzzClampFailureList: hand-edited or pathologically large newlyFailed/fixed/
// previousFailures/diffBaseFailures lists must stay <= CRD MaxItems=4096 so
// Status().Update cannot fail admission and freeze reconciliation.
func FuzzClampFailureList(f *testing.F) {
	f.Add("", 0)
	f.Add("a,b,c", 3)
	f.Add(strings.Repeat("x,", 5000), 5000)
	f.Add(",,,", 0)
	f.Fuzz(func(t *testing.T, csv string, n int) {
		// Build a list either from CSV fields or a bounded count of synthetic names.
		var in []string
		if n < 0 {
			n = -n
		}
		n = n % (failureListMax + 64) // exercise over-limit without OOM
		if n > 0 {
			in = make([]string, n)
			for i := range in {
				in[i] = fmt.Sprintf("chk-%d", i)
			}
		} else {
			in = splitCSV(csv)
		}
		// Capture prefix before clamp so we can assert no mutation of over-limit input.
		var prefixCopy []string
		if len(in) > failureListMax {
			prefixCopy = append([]string(nil), in[:failureListMax]...)
		}
		got := clampFailureList(in)
		if len(got) > failureListMax {
			t.Fatalf("len %d > failureListMax %d", len(got), failureListMax)
		}
		if len(in) <= failureListMax {
			if len(got) != len(in) {
				t.Fatalf("under-limit len %d want %d", len(got), len(in))
			}
			for i := range got {
				if got[i] != in[i] {
					t.Fatalf("under-limit mutated index %d", i)
				}
			}
			return
		}
		// Truncation keeps the prefix and must not alias the input backing array.
		if len(got) != failureListMax {
			t.Fatalf("over-limit len %d want %d", len(got), failureListMax)
		}
		for i := range got {
			if got[i] != prefixCopy[i] {
				t.Fatalf("truncated prefix mismatch at %d", i)
			}
		}
		if len(in) > 0 && len(got) > 0 {
			in[0] = "mutated"
			if got[0] == "mutated" {
				t.Fatal("clampFailureList must not alias input after truncation")
			}
		}
	})
}

// FuzzNormalizedSchedule: spec.schedule is untrusted CR text. Must never panic;
// empty uses the default five-field cron; descriptors and non-5-field forms fail.
func FuzzNormalizedSchedule(f *testing.F) {
	for _, seed := range []string{
		"", "   ", "\t", "0 1 * * *", "*/5 * * * *", "@daily", "@every 1s",
		"not a cron", "0 1 * * * *", "61 0 * * *", "  0  1  *  *  *  ",
		"0 1 * * mon", "*/7 , 1 1 0",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, schedule string) {
		got, err := normalizedSchedule(schedule)
		if strings.TrimSpace(schedule) == "" {
			if err != nil {
				t.Fatalf("empty schedule err: %v", err)
			}
			if got != defaultScanSchedule {
				t.Fatalf("empty schedule = %q, want default %q", got, defaultScanSchedule)
			}
			return
		}
		fields := strings.Fields(schedule)
		if len(fields) != 5 {
			if err == nil {
				t.Fatalf("non-5-field %q accepted as %q", schedule, got)
			}
			return
		}
		if err != nil {
			// Rejected by robfig (out of range, junk tokens, never-firing, etc.).
			return
		}
		// Accepted schedules are exactly five whitespace-normalized fields.
		gotFields := strings.Fields(got)
		if len(gotFields) != 5 {
			t.Fatalf("accepted schedule %q has %d fields", got, len(gotFields))
		}
		if got != strings.Join(fields, " ") {
			t.Fatalf("normalization %q -> %q", schedule, got)
		}
	})
}

// FuzzSyncFailureDiff: current/base failure name lists come from cluster check
// results. newlyFailed ⊆ current\base and fixed ⊆ base\current; never panics.
func FuzzSyncFailureDiff(f *testing.F) {
	f.Add("a,b,c", "b,d")
	f.Add("", "")
	f.Add("a", "a")
	f.Add("x,y", "")
	f.Add("", "x,y")
	f.Add("a,a,b", "b,b,c")
	f.Fuzz(func(t *testing.T, currentCSV, baseCSV string) {
		// Bound work: huge CSV would dominate fuzz time without more coverage.
		if len(currentCSV) > 4096 {
			currentCSV = currentCSV[:4096]
		}
		if len(baseCSV) > 4096 {
			baseCSV = baseCSV[:4096]
		}
		current := splitCSV(currentCSV)
		base := splitCSV(baseCSV)
		cb := &baselinev1alpha1.ClusterBaseline{}
		syncFailureDiff(cb, current, base)
		baseSet := map[string]bool{}
		for _, n := range base {
			baseSet[n] = true
		}
		currentSet := map[string]bool{}
		for _, n := range current {
			currentSet[n] = true
		}
		for _, n := range cb.Status.NewlyFailed {
			if baseSet[n] {
				t.Fatalf("newlyFailed %q still in base", n)
			}
			if !currentSet[n] {
				t.Fatalf("newlyFailed %q not in current", n)
			}
		}
		for _, n := range cb.Status.Fixed {
			if currentSet[n] {
				t.Fatalf("fixed %q still in current", n)
			}
			if !baseSet[n] {
				t.Fatalf("fixed %q not in base", n)
			}
		}
		// Sorted unique (notIn / sortedDiff set semantics).
		seenNew := map[string]bool{}
		for i := 0; i < len(cb.Status.NewlyFailed); i++ {
			n := cb.Status.NewlyFailed[i]
			if seenNew[n] {
				t.Fatalf("newlyFailed duplicate %q in %v", n, cb.Status.NewlyFailed)
			}
			seenNew[n] = true
			if i > 0 && cb.Status.NewlyFailed[i-1] > n {
				t.Fatalf("newlyFailed unsorted: %v", cb.Status.NewlyFailed)
			}
		}
		seenFixed := map[string]bool{}
		for i := 0; i < len(cb.Status.Fixed); i++ {
			n := cb.Status.Fixed[i]
			if seenFixed[n] {
				t.Fatalf("fixed duplicate %q in %v", n, cb.Status.Fixed)
			}
			seenFixed[n] = true
			if i > 0 && cb.Status.Fixed[i-1] > n {
				t.Fatalf("fixed unsorted: %v", cb.Status.Fixed)
			}
		}
	})
}

// TestSyncHistorySnapshot pins late-CCR refresh and nil-score drop behavior
// (including the n==1 drop must return nil, not a shared empty slice that still
// aliases capacity at the removed point).
func TestSyncHistorySnapshot(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	other := metav1.NewTime(ts.Add(-time.Hour))
	score := int32(77)
	// First point for a new scan.
	got := syncHistorySnapshot(nil, ts, &score)
	if len(got) != 1 || got[0].Score != 77 || !got[0].Time.Equal(&ts) {
		t.Fatalf("append first = %+v", got)
	}
	// Same timestamp updates score in place.
	score = 88
	got = syncHistorySnapshot(got, ts, &score)
	if len(got) != 1 || got[0].Score != 88 {
		t.Fatalf("same-ts update = %+v", got)
	}
	// Nil score on sole matching point clears the ring (no alias of removed cap).
	got = syncHistorySnapshot(got, ts, nil)
	if got != nil {
		t.Fatalf("n==1 nil drop = %v, want nil", got)
	}
	// Nil score when the last point is a different scan leaves history alone.
	seed := []baselinev1alpha1.ScoreSnapshot{{Time: other, Score: 40}}
	got = syncHistorySnapshot(seed, ts, nil)
	if len(got) != 1 || got[0].Score != 40 {
		t.Fatalf("nil on new ts must not drop prior: %+v", got)
	}
	// Same-ts nil drop with prior points copies and leaves earlier history.
	two := []baselinev1alpha1.ScoreSnapshot{
		{Time: other, Score: 40},
		{Time: ts, Score: 50},
	}
	got = syncHistorySnapshot(two, ts, nil)
	if len(got) != 1 || got[0].Score != 40 || !got[0].Time.Equal(&other) {
		t.Fatalf("same-ts nil drop prior = %+v", got)
	}
	// Mutating the original two[1] must not affect got (copy after truncate).
	two[1].Score = 99
	if got[0].Score != 40 {
		t.Fatal("truncated history aliases input after nil drop")
	}
}

// FuzzSyncHistorySnapshot: late-arriving same-timestamp score updates and
// nil-score drops must keep the ring bounded and scores admission-safe.
func FuzzSyncHistorySnapshot(f *testing.F) {
	f.Add(int64(1), int32(50), false, false)
	f.Add(int64(1), int32(50), true, false)  // same timestamp update
	f.Add(int64(1), int32(50), true, true)   // same timestamp nil drop
	f.Add(int64(2), int32(-5), false, false) // out-of-range score
	f.Add(int64(3), int32(150), false, false)
	f.Fuzz(func(t *testing.T, unix int64, scoreVal int32, sameTS, nilScore bool) {
		if unix < 0 {
			unix = -unix
		}
		unix = unix % (1 << 30)
		ts := metav1.NewTime(time.Unix(unix, 0).UTC())
		var hist []baselinev1alpha1.ScoreSnapshot
		// Seed a prior point so same-timestamp and append paths both run.
		prior := ts
		if !sameTS {
			prior = metav1.NewTime(ts.Add(-time.Hour))
		}
		hist = appendHistoryRing(hist, prior, 40, historyMax)
		var s *int32
		if !nilScore {
			s = &scoreVal
		}
		got := syncHistorySnapshot(hist, ts, s)
		if len(got) > historyMax {
			t.Fatalf("len %d > historyMax %d", len(got), historyMax)
		}
		for _, h := range got {
			if h.Score < 0 || h.Score > 100 {
				t.Fatalf("score %d out of [0,100]", h.Score)
			}
		}
		if sameTS && nilScore {
			// Dropping the matching last point leaves only earlier history.
			if n := len(hist); n > 0 && hist[n-1].Time.Equal(&ts) {
				if len(got) != n-1 {
					t.Fatalf("nil same-ts drop: len %d want %d", len(got), n-1)
				}
			}
			return
		}
		if sameTS && !nilScore {
			if len(got) == 0 {
				t.Fatal("same-ts update emptied history")
			}
			want := scoreVal
			if want < 0 {
				want = 0
			} else if want > 100 {
				want = 100
			}
			if last := got[len(got)-1]; !last.Time.Equal(&ts) || last.Score != want {
				t.Fatalf("same-ts update last=%+v want score %d at %v", last, want, ts)
			}
		}
	})
}

// FuzzSplitCSV: comma-separated annotation/list text is untrusted. Never panic;
// drop empties after trim; every non-empty trimmed field must appear (order free).
func FuzzSplitCSV(f *testing.F) {
	for _, seed := range []string{
		"", "a", "a,b", "a, a, b", ",,,", "  x  , y ", "a,b,a", "\t,\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			raw = raw[:4096]
		}
		got := splitCSV(raw)
		seen := map[string]int{}
		for _, n := range got {
			if n == "" {
				t.Fatal("empty item in splitCSV result")
			}
			if n != strings.TrimSpace(n) {
				t.Fatalf("untrimmed item %q", n)
			}
			seen[n]++
		}
		// Multiset: splitCSV keeps duplicates (unlike batchRemediationNames).
		wantCounts := map[string]int{}
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			wantCounts[p]++
		}
		total := 0
		for _, c := range wantCounts {
			total += c
		}
		if len(got) != total {
			t.Fatalf("len %d want %d for raw=%q got=%v", len(got), total, raw, got)
		}
		for p, c := range wantCounts {
			if seen[p] != c {
				t.Fatalf("field %q count %d want %d (raw=%q)", p, seen[p], c, raw)
			}
		}
	})
}

// FuzzClampHistory: hand-edited history rings must stay <= max and scores in
// [0,100] so Status().Update cannot fail CRD admission.
func FuzzClampHistory(f *testing.F) {
	f.Add(int32(50), 5, 30)
	f.Add(int32(-1), 40, 30)
	f.Add(int32(150), 0, 30)
	f.Add(int32(0), 1, 0)
	f.Add(int32(100), 35, -5)
	f.Fuzz(func(t *testing.T, scoreSeed int32, n, max int) {
		if n < 0 {
			n = -n
		}
		n = n % 64 // bound allocation
		if max < 0 {
			max = -max
		}
		max = max % 64
		in := make([]baselinev1alpha1.ScoreSnapshot, n)
		for i := range in {
			// Mix in-range and out-of-range scores from the seed.
			s := scoreSeed + int32(i)
			in[i] = baselinev1alpha1.ScoreSnapshot{
				Time:  metav1.NewTime(time.Unix(int64(i), 0).UTC()),
				Score: s,
			}
		}
		var prefixCopy []baselinev1alpha1.ScoreSnapshot
		if max > 0 && len(in) > max {
			prefixCopy = append([]baselinev1alpha1.ScoreSnapshot(nil), in[len(in)-max:]...)
		}
		got := clampHistory(in, max)
		if max > 0 && len(got) > max {
			t.Fatalf("len %d > max %d", len(got), max)
		}
		if max <= 0 && len(got) != len(in) {
			// max <= 0 means no length trim (only score clamp).
			t.Fatalf("max<=0 len %d want %d", len(got), len(in))
		}
		for _, h := range got {
			if h.Score < 0 || h.Score > 100 {
				t.Fatalf("score %d out of [0,100]", h.Score)
			}
		}
		if max > 0 && len(in) > max {
			if len(got) != max {
				t.Fatalf("truncated len %d want %d", len(got), max)
			}
			// Score clamp on the kept suffix; times must match the suffix.
			for i := range got {
				if !got[i].Time.Equal(&prefixCopy[i].Time) {
					t.Fatalf("suffix time mismatch at %d", i)
				}
				want := prefixCopy[i].Score
				if want < 0 {
					want = 0
				} else if want > 100 {
					want = 100
				}
				if got[i].Score != want {
					t.Fatalf("suffix score[%d]=%d want %d", i, got[i].Score, want)
				}
			}
			// Truncation must not alias the input backing array.
			if len(in) > 0 && len(got) > 0 {
				in[len(in)-1].Score = -999
				if got[len(got)-1].Score == -999 {
					t.Fatal("clampHistory must not alias input after truncation")
				}
			}
		}
	})
}

// FuzzCheckSeverity: CCR severity field and check-severity label are untrusted
// cluster data. Prefer non-empty field; fall back to label; never panic.
func FuzzCheckSeverity(f *testing.F) {
	f.Add("high", "medium")
	f.Add("", "low")
	f.Add("unknown", "")
	f.Add("", "")
	f.Add("HIGH", "high")
	f.Add("info", "info")
	f.Fuzz(func(t *testing.T, field, label string) {
		// Bound string sizes so NestedString paths stay cheap.
		if len(field) > 256 {
			field = field[:256]
		}
		if len(label) > 256 {
			label = label[:256]
		}
		item := &unstructured.Unstructured{Object: map[string]any{}}
		if field != "" {
			_ = unstructured.SetNestedField(item.Object, field, "severity")
		}
		if label != "" {
			item.SetLabels(map[string]string{checkSeverityLabel: label})
		}
		got := checkSeverity(item)
		want := field
		if want == "" {
			want = label
		}
		if want == "" {
			want = "unknown"
		}
		if got != want {
			t.Fatalf("checkSeverity field=%q label=%q got %q want %q", field, label, got, want)
		}
	})
}

// FuzzSanitizeStatusForUpdate: hostile/stale status fields must be admission-safe
// (score [0,100] or nil, history rings <= historyMax with scores clamped,
// failure-name lists <= failureListMax).
func FuzzSanitizeStatusForUpdate(f *testing.F) {
	f.Add(int32(200), 40, 5000)
	f.Add(int32(-5), 0, 0)
	f.Add(int32(50), 5, 3)
	f.Add(int32(100), 30, 4096)
	f.Fuzz(func(t *testing.T, scoreVal int32, histN, failN int) {
		if histN < 0 {
			histN = -histN
		}
		histN = histN % 64
		if failN < 0 {
			failN = -failN
		}
		failN = failN % (failureListMax + 32)

		hist := make([]baselinev1alpha1.ScoreSnapshot, histN)
		for i := range hist {
			hist[i] = baselinev1alpha1.ScoreSnapshot{
				Time:  metav1.NewTime(time.Unix(int64(i), 0).UTC()),
				Score: scoreVal + int32(i),
			}
		}
		fails := make([]string, failN)
		for i := range fails {
			fails[i] = fmt.Sprintf("chk-%d", i)
		}
		s := scoreVal
		cb := &baselinev1alpha1.ClusterBaseline{
			Status: baselinev1alpha1.ClusterBaselineStatus{
				Score:            &s,
				History:          append([]baselinev1alpha1.ScoreSnapshot(nil), hist...),
				NewlyFailed:      append([]string(nil), fails...),
				Fixed:            append([]string(nil), fails...),
				PreviousFailures: append([]string(nil), fails...),
				DiffBaseFailures: append([]string(nil), fails...),
				Profiles: []baselinev1alpha1.ProfileStatus{{
					Key:     "cis",
					History: append([]baselinev1alpha1.ScoreSnapshot(nil), hist...),
				}},
				TailoredProfiles: []baselinev1alpha1.TailoredProfileStatus{{
					Name:    "custom",
					History: append([]baselinev1alpha1.ScoreSnapshot(nil), hist...),
				}},
			},
		}
		sanitizeStatusForUpdate(cb)

		if cb.Status.Score == nil {
			t.Fatal("non-nil score became nil")
		}
		if *cb.Status.Score < 0 || *cb.Status.Score > 100 {
			t.Fatalf("score %d out of [0,100]", *cb.Status.Score)
		}
		assertHistorySafe := func(h []baselinev1alpha1.ScoreSnapshot, label string) {
			t.Helper()
			if len(h) > historyMax {
				t.Fatalf("%s len %d > historyMax %d", label, len(h), historyMax)
			}
			for _, snap := range h {
				if snap.Score < 0 || snap.Score > 100 {
					t.Fatalf("%s score %d out of [0,100]", label, snap.Score)
				}
			}
		}
		assertHistorySafe(cb.Status.History, "history")
		if len(cb.Status.Profiles) > 0 {
			assertHistorySafe(cb.Status.Profiles[0].History, "profile history")
		}
		if len(cb.Status.TailoredProfiles) > 0 {
			assertHistorySafe(cb.Status.TailoredProfiles[0].History, "tailored history")
		}
		for name, list := range map[string][]string{
			"newlyFailed":      cb.Status.NewlyFailed,
			"fixed":            cb.Status.Fixed,
			"previousFailures": cb.Status.PreviousFailures,
			"diffBaseFailures": cb.Status.DiffBaseFailures,
		} {
			if len(list) > failureListMax {
				t.Fatalf("%s len %d > failureListMax", name, len(list))
			}
		}
	})
}
