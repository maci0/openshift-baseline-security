package v1alpha1

import (
	"errors"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// A new profile has to land in four places: the ProfileKey constants, the
// kubebuilder Enum marker (which is what admission validates against), the
// AllProfileKeys display order, and the console PROFILE_KEYS list. These tests
// cover the first three, including reading the marker out of this package's own
// source, since it is hand-maintained and nothing else compares it to the
// constants. The console half is not reachable from Go: make
// verify-product-lockstep (hack/verify-product-lockstep.sh) diffs the constants
// against console-plugin/src/models.ts.

const profileKeyEnumMarker = "+kubebuilder:validation:Enum="

// TestProfileKeyEnumMarkerMatchesConstants keeps the hand-written Enum marker
// and the ProfileKey constants in step. A profile in the constants but not the
// marker is rejected by admission on a CR that the Go side and the console both
// consider valid.
func TestProfileKeyEnumMarkerMatchesConstants(t *testing.T) {
	enum, err := profileKeyEnumValues()
	if err != nil {
		t.Fatalf("reading the ProfileKey Enum marker: %v", err)
	}
	got := AllProfileKeys()
	if len(enum) != len(got) {
		t.Fatalf("Enum marker lists %d values (%v), AllProfileKeys has %d (%v)", len(enum), enum, len(got), got)
	}
	for i, v := range enum {
		if ProfileKey(v) != got[i] {
			t.Fatalf("Enum[%d] = %q, AllProfileKeys[%d] = %q (order is API contract)", i, v, i, got[i])
		}
	}
}

var errProfileKeyEnumMarkerNotFound = errors.New("no Enum marker line on the ProfileKey type declaration")

// profileKeyEnumValues reads the values out of the Enum marker on the line
// above `type ProfileKey string`. Parsed from the source rather than the
// generated CRD so the check runs in the plain unit suite with no build step.
func profileKeyEnumValues() ([]string, error) {
	src, err := os.ReadFile("clusterbaseline_types.go")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "type ProfileKey string" {
			continue
		}
		if i == 0 {
			break
		}
		marker, ok := strings.CutPrefix(strings.TrimSpace(lines[i-1]), "// "+profileKeyEnumMarker)
		if !ok {
			break
		}
		return strings.Split(marker, ";"), nil
	}
	return nil, errProfileKeyEnumMarkerNotFound
}

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
