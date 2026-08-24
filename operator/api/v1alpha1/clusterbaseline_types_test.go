package v1alpha1

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// The doc comment on AllProfileKeys pins its length to the CRD Enum cardinality
// and the Profiles MaxItems marker (8), and the constants require lockstep with
// the console PROFILE_KEYS list. These tests fail when a profile is added to one
// place but not the others, so admission and the console cannot drift apart.

func TestAllProfileKeysMatchesEnumCardinality(t *testing.T) {
	want := []ProfileKey{
		ProfileCIS, ProfilePCIDSS, ProfileNISTModerate, ProfileNISTHigh,
		ProfileSTIG, ProfileNERCCIP, ProfileE8, ProfileBSI,
	}
	got := AllProfileKeys()
	if len(got) != len(want) {
		t.Fatalf("AllProfileKeys = %v (%d entries), want %d (Enum cardinality and Profiles MaxItems)",
			got, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllProfileKeys[%d] = %q, want %q (display order is API contract)", i, got[i], want[i])
		}
	}
	seen := make(map[ProfileKey]bool, len(got))
	for _, k := range got {
		if seen[k] {
			t.Fatalf("duplicate ProfileKey %q in AllProfileKeys", k)
		}
		seen[k] = true
	}
}

func TestEveryProfileKeyBindsComplianceOperatorProfiles(t *testing.T) {
	for _, k := range AllProfileKeys() {
		if !k.Known() {
			t.Fatalf("%q not Known despite being an enum constant", k)
		}
		names := k.ProfileNames()
		if len(names) == 0 {
			t.Fatalf("%q binds no Compliance Operator profiles", k)
		}
		seen := make(map[string]bool, len(names))
		for _, n := range names {
			if seen[n] {
				t.Fatalf("%q binds duplicate profile name %q", k, n)
			}
			seen[n] = true
			if errs := validation.IsDNS1123Subdomain(n); len(errs) > 0 {
				t.Fatalf("%q binds invalid profile name %q: %v", k, n, errs)
			}
		}
	}
}

func TestUnknownProfileKeyFailsClosed(t *testing.T) {
	for _, k := range []ProfileKey{"", "CIS", "bogus"} {
		if k.Known() {
			t.Fatalf("Known(%q) = true, want false", k)
		}
		if names := k.ProfileNames(); names != nil {
			t.Fatalf("ProfileNames(%q) = %v, want nil", k, names)
		}
	}
}

// ProfileNames feeds ScanSettingBinding names; every bound name must be usable
// inside a k8s name without further transformation.
func TestProfileNamesFitLabelValueBudget(t *testing.T) {
	for _, k := range AllProfileKeys() {
		for _, n := range k.ProfileNames() {
			if len(n) > validation.LabelValueMaxLength {
				t.Fatalf("%q name %q exceeds label value budget", k, n)
			}
			if strings.ContainsAny(n, " _") {
				t.Fatalf("%q name %q contains whitespace or underscore", k, n)
			}
		}
	}
}
