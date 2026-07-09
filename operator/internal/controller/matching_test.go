package controller

import (
	"strings"
	"testing"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func TestLongestProfileMatch(t *testing.T) {
	m := map[string]baselinev1alpha1.ProfileKey{
		"ocp4-cis":      "cis",
		"ocp4-cis-node": "cis",
		"rhcos4-stig":   "stig",
	}
	cases := []struct {
		scan string
		key  baselinev1alpha1.ProfileKey
		ok   bool
	}{
		{"ocp4-cis", "cis", true},
		{"ocp4-cis-node-master", "cis", true}, // longest match, not swallowed by ocp4-cis
		{"ocp4-cis-node", "cis", true},
		{"rhcos4-stig-worker", "stig", true},
		{"ocp4-cisx", "", false}, // prefix must be on a "-" boundary
		{"ocp4", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		key, ok := longestProfileMatch(m, c.scan)
		if ok != c.ok || key != c.key {
			t.Errorf("longestProfileMatch(%q) = (%q,%v), want (%q,%v)", c.scan, key, ok, c.key, c.ok)
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

// Scan names, suite labels, and PVC names come from cluster objects the
// Compliance Operator (or anything else with API access) writes: untrusted.
func FuzzMatchesAnyProfile(f *testing.F) {
	profiles := map[string]bool{"ocp4-cis": true, "ocp4-cis-node": true, "rhcos4-e8": true}
	for _, seed := range []string{"ocp4-cis", "ocp4-cis-node-master", "ocp4-cisx", "", "-", "ocp4-cis-"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := matchesAnyProfile(name, profiles)
		// Invariant: got is true iff an exact or "-"-boundary prefix match exists.
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

func FuzzLongestProfileMatch(f *testing.F) {
	m := map[string]baselinev1alpha1.ProfileKey{
		"ocp4-cis":      "cis",
		"ocp4-cis-node": "cis",
		"ocp4-pci-dss":  "pci-dss",
	}
	for _, seed := range []string{"ocp4-cis-node-master", "ocp4-cis", "x", "", "ocp4-pci-dss-node"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, scan string) {
		key, ok := longestProfileMatch(m, scan)
		if !ok {
			if key != "" {
				t.Fatalf("no match must return empty key, got %q", key)
			}
			return
		}
		// Invariant: some profile name maps to key and matches scan on a boundary.
		found := false
		for p, k := range m {
			if k == key && (scan == p || strings.HasPrefix(scan, p+"-")) {
				found = true
			}
		}
		if !found {
			t.Fatalf("longestProfileMatch(%q) returned key %q with no matching profile", scan, key)
		}
	})
}
