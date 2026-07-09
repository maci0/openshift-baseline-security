package controller

import (
	"strings"
	"testing"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func TestProfileKeyFromSuite(t *testing.T) {
	cases := []struct {
		suite string
		key   baselinev1alpha1.ProfileKey
		ok    bool
	}{
		{"baseline-cis", "cis", true},
		{"baseline-pci-dss", "pci-dss", true},
		{"other-suite", "", false},
		{"", "", false},
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

// PVC names come from cluster objects anything with API access can create: untrusted.
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
