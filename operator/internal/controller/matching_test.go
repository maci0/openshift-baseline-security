package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func TestOwnedSuites(t *testing.T) {
	if len(ownedSuites(&baselinev1alpha1.ClusterBaseline{})) != 0 {
		t.Fatal("empty profiles should yield empty suites")
	}
	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{
			Profiles: []baselinev1alpha1.ProfileKey{"cis", "stig"},
		},
	}
	s := ownedSuites(cb)
	if !s["baseline-cis"] || !s["baseline-stig"] || len(s) != 2 {
		t.Fatalf("%v", s)
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

func TestScore(t *testing.T) {
	if score(0, 0) != nil || score(-1, 0) != nil || score(1, -1) != nil {
		t.Fatal("zero/negative countable should be nil")
	}
	if s := score(2, 1); s == nil || *s != 66 {
		t.Fatalf("score(2,1) = %v, want 66", s)
	}
	if s := score(1, 0); s == nil || *s != 100 {
		t.Fatalf("score(1,0) = %v, want 100", s)
	}
	if s := score(0, 5); s == nil || *s != 0 {
		t.Fatalf("score(0,5) = %v, want 0", s)
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

func TestClampScore(t *testing.T) {
	if clampScore(nil) != nil {
		t.Fatal("nil stays nil")
	}
	neg := int32(-1)
	if s := clampScore(&neg); s == nil || *s != 0 {
		t.Fatalf("neg = %v", s)
	}
	hi := int32(101)
	if s := clampScore(&hi); s == nil || *s != 100 {
		t.Fatalf("hi = %v", s)
	}
	ok := int32(77)
	if s := clampScore(&ok); s == nil || *s != 77 {
		t.Fatalf("ok = %v", s)
	}
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
	keys := []baselinev1alpha1.ProfileKey{
		"cis", "pci-dss", "nist-moderate", "nist-high", "stig", "nerc-cip", "e8", "bsi",
	}
	for _, k := range keys {
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
	f.Fuzz(func(t *testing.T, name string) {
		got := matchesAnyProfile(name, profiles)
		want := false
		for p := range profiles {
			if name == p {
				want = true
				break
			}
			if rest, ok := strings.CutPrefix(name, p+"-"); ok && scanRoleSuffix(rest) {
				want = true
				break
			}
		}
		if got != want {
			t.Fatalf("matchesAnyProfile(%q) = %v, want %v", name, got, want)
		}
	})
}

func FuzzScore(f *testing.F) {
	f.Add(int32(0), int32(0))
	f.Add(int32(1), int32(0))
	f.Add(int32(0), int32(1))
	f.Add(int32(2), int32(1))
	f.Add(int32(-1), int32(5))
	f.Add(int32(2147483647), int32(0)) // int32-overflow regression seed
	f.Fuzz(func(t *testing.T, pass, fail int32) {
		s := score(pass, fail)
		// Oracle must use int64 sums (same as score) so int32 overflow is not expected nil.
		if pass < 0 || fail < 0 || int64(pass)+int64(fail) == 0 {
			if s != nil {
				t.Fatalf("expected nil for pass=%d fail=%d", pass, fail)
			}
			return
		}
		if s == nil {
			t.Fatal("expected non-nil")
		}
		if *s < 0 || *s > 100 {
			t.Fatalf("score %d out of range", *s)
		}
		want := int32(int64(pass) * 100 / (int64(pass) + int64(fail)))
		if *s != want {
			t.Fatalf("got %d want %d", *s, want)
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

// FuzzCompareComplianceCSVVersion pins the ordering as a total order: reflexive
// and antisymmetric for any two arbitrary CSV name strings. A bug in the version
// or prerelease comparison (non-antisymmetric) fails here.
func FuzzCompareComplianceCSVVersion(f *testing.F) {
	f.Add("compliance-operator.v1.6.0", "compliance-operator.v1.6.1")
	f.Add("compliance-operator.v1.6.0-rc1", "compliance-operator.v1.6.0")
	f.Add("compliance-operator.v1.6.0", "compliance-operator.v1.6.0+b2")
	f.Add("junk", "compliance-operator.v1.0.0")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, a, b string) {
		if c := compareComplianceCSVVersion(a, a); c != 0 {
			t.Fatalf("compare(a,a)=%d for %q, want 0", c, a)
		}
		if ab, ba := compareComplianceCSVVersion(a, b), compareComplianceCSVVersion(b, a); sign(ab) != -sign(ba) {
			t.Fatalf("not antisymmetric: compare(%q,%q)=%d vs compare(b,a)=%d", a, b, ab, ba)
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
