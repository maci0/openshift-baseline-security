package controller

import (
	"strings"
	"testing"
	"time"

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
	profiles := map[string]bool{"ocp4-cis": true, "ocp4-cis-node": true}
	for name, want := range map[string]bool{
		"ocp4-cis":             true,
		"ocp4-cis-node-worker": true,
		"ocp4-cisx":            false,
		"ocp4-pci-dss":         false,
		"":                     false,
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
	for _, seed := range []string{"ocp4-cis", "ocp4-cis-node-master", "ocp4-cisx", "", "-", "ocp4-cis-"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := matchesAnyProfile(name, profiles)
		want := false
		for p := range profiles {
			if name == p || strings.HasPrefix(name, p+"-") {
				want = true
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
