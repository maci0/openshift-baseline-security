package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FuzzEffectiveInconsistentStatus: CO inconsistent-source / most-common-status
// annotations are untrusted cluster data. Must never panic; result is one of
// PASS | NOT-APPLICABLE | INCONSISTENT; FAIL/ERROR among nodes stay INCONSISTENT.
func FuzzEffectiveInconsistentStatus(f *testing.F) {
	for _, seed := range []struct{ src, mc string }{
		{"n0:PASS", "NOT-APPLICABLE"},
		{"n0:FAIL", "PASS"},
		{"n0:ERROR", "PASS"},
		{"n0:SKIP", "SKIP"},
		{"n0:FUTURE-STATE", "PASS"},
		{"garbage,,:", ""},
		{"", ""},
		{"n0:PASS,n1:FAIL", "PASS"},
		{" n0 : pass ", " not-applicable "},
	} {
		f.Add(seed.src, seed.mc)
	}
	allowed := map[string]bool{"PASS": true, "NOT-APPLICABLE": true, "INCONSISTENT": true}
	f.Fuzz(func(t *testing.T, src, mostCommon string) {
		u := &unstructured.Unstructured{}
		ann := map[string]string{}
		if src != "" {
			ann[inconsistentSourceAnn] = src
		}
		if mostCommon != "" {
			ann[mostCommonStatusAnn] = mostCommon
		}
		u.SetAnnotations(ann)
		got := effectiveInconsistentStatus(u)
		if !allowed[got] {
			t.Fatalf("unexpected status %q for src=%q mc=%q", got, src, mostCommon)
		}
		// FAIL or ERROR anywhere in the gathered states must fail closed.
		states := inconsistentStates(u)
		if (states["FAIL"] || states["ERROR"]) && got != "INCONSISTENT" {
			t.Fatalf("FAIL/ERROR must stay INCONSISTENT, got %q (states=%v)", got, states)
		}
		// Unknown states must fail closed even if PASS is also present.
		for st := range states {
			switch st {
			case "PASS", "FAIL", "ERROR", "NOT-APPLICABLE", "SKIP":
			default:
				if got != "INCONSISTENT" {
					t.Fatalf("unknown state %q must fail closed, got %q", st, got)
				}
			}
		}
	})
}
