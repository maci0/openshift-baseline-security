package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// pickComplianceOperatorCSV decides which installed CSV the reconciler follows,
// so the cases below pin every filter it applies: the csvNamePrefix, the
// namespace scope, the phase filter, and the version ordering (see
// compliance_version_test.go). The winner is a deep copy, so a caller cannot
// mutate the caller's slice.
func TestPickComplianceOperatorCSV(t *testing.T) {
	t.Parallel()
	csv := func(name, ns, phase string) unstructured.Unstructured {
		c := u(csvGVK)
		c.SetName(name)
		c.SetNamespace(ns)
		if phase != "" {
			c.Object["status"] = map[string]any{"phase": phase}
		}
		return *c
	}
	const (
		ns      = "openshift-compliance"
		otherNS = "somewhere-else"
	)

	t.Run("picks the highest version among succeeded CSVs", func(t *testing.T) {
		t.Parallel()
		items := []unstructured.Unstructured{
			csv("compliance-operator.v1.0.0", ns, "Succeeded"),
			csv("compliance-operator.v1.10.0", ns, "Succeeded"),
			csv("compliance-operator.v1.9.0", ns, "Succeeded"),
		}
		got := pickComplianceOperatorCSV(t.Context(), items, "", true)
		if got == nil || got.GetName() != "compliance-operator.v1.10.0" {
			t.Fatalf("picked %v, want compliance-operator.v1.10.0", nameOf(got))
		}
	})

	t.Run("order of the input does not change the winner", func(t *testing.T) {
		t.Parallel()
		first := []unstructured.Unstructured{
			csv("compliance-operator.v1.0.0", ns, "Succeeded"),
			csv("compliance-operator.v1.10.0", ns, "Succeeded"),
			csv("compliance-operator.v1.9.0", ns, "Succeeded"),
		}
		second := []unstructured.Unstructured{first[2], first[0], first[1]}
		a, b := pickComplianceOperatorCSV(t.Context(), first, "", true), pickComplianceOperatorCSV(t.Context(), second, "", true)
		if a == nil || b == nil || a.GetName() != b.GetName() {
			t.Fatalf("winner depends on input order: %v vs %v", nameOf(a), nameOf(b))
		}
	})

	t.Run("phase filter separates the failed install from the good one", func(t *testing.T) {
		t.Parallel()
		items := []unstructured.Unstructured{
			csv("compliance-operator.v2.0.0", ns, "Failed"),
			csv("compliance-operator.v1.0.0", ns, "Succeeded"),
		}
		if got := pickComplianceOperatorCSV(t.Context(), items, "", true); got == nil || got.GetName() != "compliance-operator.v1.0.0" {
			t.Errorf("succeededOnly picked %v, want compliance-operator.v1.0.0", nameOf(got))
		}
		if got := pickComplianceOperatorCSV(t.Context(), items, "", false); got == nil || got.GetName() != "compliance-operator.v2.0.0" {
			t.Errorf("non-succeeded picked %v, want compliance-operator.v2.0.0", nameOf(got))
		}
		// A phase the API reports that is neither Succeeded nor anything known
		// is still a candidate on the non-succeeded side.
		mixed := []unstructured.Unstructured{csv("compliance-operator.v3.0.0", ns, "Replacing")}
		if got := pickComplianceOperatorCSV(t.Context(), mixed, "", true); got != nil {
			t.Errorf("succeededOnly picked %v from an unknown phase, want nil", got.GetName())
		}
		if got := pickComplianceOperatorCSV(t.Context(), mixed, "", false); got == nil || got.GetName() != "compliance-operator.v3.0.0" {
			t.Errorf("non-succeeded picked %v from an unknown phase, want compliance-operator.v3.0.0", nameOf(got))
		}
	})

	t.Run("namespace scope is honoured in both directions", func(t *testing.T) {
		t.Parallel()
		items := []unstructured.Unstructured{
			csv("compliance-operator.v1.9.0", otherNS, "Succeeded"),
			csv("compliance-operator.v1.0.0", ns, "Succeeded"),
		}
		if got := pickComplianceOperatorCSV(t.Context(), items, ns, true); got == nil || got.GetName() != "compliance-operator.v1.0.0" {
			t.Errorf("ns filter picked %v, want compliance-operator.v1.0.0", nameOf(got))
		}
		if got := pickComplianceOperatorCSV(t.Context(), items, otherNS, true); got == nil || got.GetName() != "compliance-operator.v1.9.0" {
			t.Errorf("ns filter picked %v, want compliance-operator.v1.9.0", nameOf(got))
		}
		if got := pickComplianceOperatorCSV(t.Context(), items, "absent", true); got != nil {
			t.Errorf("absent namespace picked %v, want nil", got.GetName())
		}
	})

	t.Run("CSVs of other operators and a missing phase are ignored", func(t *testing.T) {
		t.Parallel()
		items := []unstructured.Unstructured{
			csv("other-operator.v99.0.0", ns, "Succeeded"),
			csv("compliance-operator.v1.0.0", ns, ""),
		}
		// No phase is not Succeeded, so it is only a candidate off the
		// succeeded path.
		if got := pickComplianceOperatorCSV(t.Context(), items, "", true); got != nil {
			t.Errorf("succeededOnly picked %v, want nil", got.GetName())
		}
		if got := pickComplianceOperatorCSV(t.Context(), items, "", false); got == nil || got.GetName() != "compliance-operator.v1.0.0" {
			t.Errorf("non-succeeded picked %v, want compliance-operator.v1.0.0", nameOf(got))
		}
	})

	t.Run("no candidate yields nil", func(t *testing.T) {
		t.Parallel()
		if got := pickComplianceOperatorCSV(t.Context(), nil, "", false); got != nil {
			t.Errorf("empty list picked %v, want nil", nameOf(got))
		}
	})

	t.Run("the winner is a copy, not the caller's object", func(t *testing.T) {
		t.Parallel()
		items := []unstructured.Unstructured{csv("compliance-operator.v1.0.0", ns, "Succeeded")}
		got := pickComplianceOperatorCSV(t.Context(), items, "", true)
		if got == nil {
			t.Fatal("picked nil")
		}
		if got == &items[0] {
			t.Fatal("picked the caller's object instead of a deep copy")
		}
		got.SetName("mutated")
		if items[0].GetName() != "compliance-operator.v1.0.0" {
			t.Errorf("caller's CSV renamed to %q by the returned copy", items[0].GetName())
		}
	})
}

// foldComplianceOperatorCSVs is the streaming form of
// pickComplianceOperatorCSV, used by the paged cluster-wide walk so pages do
// not have to stay resident. These cases pin that it picks the same CSV as the
// list form for every split of the same input, since a page-boundary-dependent
// winner would make the Compliance Operator version depend on how the apiserver
// chunked its response.
func TestFoldComplianceOperatorCSVs(t *testing.T) {
	t.Parallel()
	csv := func(name, ns, phase string) unstructured.Unstructured {
		c := u(csvGVK)
		c.SetName(name)
		c.SetNamespace(ns)
		if phase != "" {
			c.Object["status"] = map[string]any{"phase": phase}
		}
		return *c
	}
	items := []unstructured.Unstructured{
		csv("other-operator.v99.0.0", "ns", "Succeeded"),
		csv("compliance-operator.v1.0.0", "ns", "Succeeded"),
		csv("compliance-operator.v1.10.0", "ns", "Failed"),
		csv("compliance-operator.v1.9.0", "other", "Succeeded"),
		csv("compliance-operator.v2.0.0", "ns", "Succeeded"),
		csv("compliance-operator.v0.1.0", "ns", ""),
	}

	// Every split point, plus a page size larger than the whole list, must land
	// on the same two winners the single-shot list form picks.
	for split := 0; split <= len(items); split++ {
		var succ, other *unstructured.Unstructured
		if split > 0 {
			succ, other = foldComplianceOperatorCSVs(t.Context(), items[:split], nil, nil)
		}
		succ, other = foldComplianceOperatorCSVs(t.Context(), items[split:], succ, other)
		for _, tc := range []struct {
			tier string
			got  *unstructured.Unstructured
			want string
		}{
			{"succeeded", succ, "compliance-operator.v2.0.0"},
			{"other", other, "compliance-operator.v1.10.0"},
		} {
			if nameOf(tc.got) != tc.want {
				t.Errorf("split %d: %s tier folded to %v, want %s",
					split, tc.tier, nameOf(tc.got), tc.want)
			}
		}
	}

	t.Run("an empty page leaves the incumbents alone", func(t *testing.T) {
		t.Parallel()
		seed := csv("compliance-operator.v3.0.0", "ns", "Succeeded")
		incumbent := seed.DeepCopy()
		succ, other := foldComplianceOperatorCSVs(t.Context(), nil, incumbent, nil)
		if succ != incumbent || other != nil {
			t.Errorf("empty page changed the incumbents: %v / %v", nameOf(succ), nameOf(other))
		}
	})

	t.Run("the winner is a copy, not the caller's object", func(t *testing.T) {
		t.Parallel()
		page := []unstructured.Unstructured{csv("compliance-operator.v1.0.0", "ns", "Succeeded")}
		succ, _ := foldComplianceOperatorCSVs(t.Context(), page, nil, nil)
		if succ == nil {
			t.Fatal("folded to nil")
		}
		if succ == &page[0] {
			t.Fatal("folded the caller's object instead of a deep copy")
		}
		succ.SetName("mutated")
		if page[0].GetName() != "compliance-operator.v1.0.0" {
			t.Errorf("caller's CSV renamed to %q by the returned copy", page[0].GetName())
		}
	})
}

func nameOf(o *unstructured.Unstructured) string {
	if o == nil {
		return "<nil>"
	}
	return o.GetName()
}
