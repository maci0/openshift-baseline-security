package controller

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FuzzCompletedSuiteTimes: ComplianceSuite status.phase, scanStatuses[].phase,
// and endTimestamp are untrusted cluster data. Nested type confusion and hostile
// timestamps must never panic; accepted runs keep earliest <= latest and within
// the same clock-skew bound as parseScanEndTimestamp.
func FuzzCompletedSuiteTimes(f *testing.F) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	f.Add("DONE", "DONE", "2026-07-09T01:00:00Z", "DONE", "2026-07-09T02:00:00Z")
	f.Add("DONE", "DONE", "2026-07-09T02:00:00Z", "DONE", "2026-07-09T01:00:00Z")
	f.Add("RUNNING", "DONE", "2026-07-09T01:00:00Z", "DONE", "2026-07-09T02:00:00Z")
	f.Add("DONE", "RUNNING", "2026-07-09T01:00:00Z", "DONE", "2026-07-09T02:00:00Z")
	f.Add("DONE", "DONE", "not-a-time", "DONE", "2026-07-09T02:00:00Z")
	f.Add("DONE", "DONE", "", "DONE", "")
	f.Add("DONE", "DONE", now.Add(48*time.Hour).UTC().Format(time.RFC3339), "DONE", "2026-07-09T01:00:00Z")
	f.Add("", "", "", "", "")
	f.Fuzz(func(t *testing.T, suitePhase, p0, ts0, p1, ts1 string) {
		// Bound string work so NestedString paths stay cheap under the fuzzer.
		const max = 512
		if len(suitePhase) > max {
			suitePhase = suitePhase[:max]
		}
		if len(p0) > max {
			p0 = p0[:max]
		}
		if len(p1) > max {
			p1 = p1[:max]
		}
		if len(ts0) > max {
			ts0 = ts0[:max]
		}
		if len(ts1) > max {
			ts1 = ts1[:max]
		}

		suite := &unstructured.Unstructured{Object: map[string]any{
			"status": map[string]any{
				"phase": suitePhase,
				"scanStatuses": []any{
					map[string]any{"phase": p0, "endTimestamp": ts0},
					map[string]any{"phase": p1, "endTimestamp": ts1},
				},
			},
		}}
		run, ok := completedSuiteTimes(suite, now)
		if !ok {
			// Wrong-type / empty slice / non-DONE paths must reject, not panic.
			if !run.earliest.IsZero() || !run.latest.IsZero() {
				t.Fatalf("rejected run still carried times: %+v", run)
			}
			return
		}
		if run.latest.IsZero() {
			t.Fatal("accepted run with zero latest")
		}
		if run.earliest.After(run.latest) {
			t.Fatalf("earliest %v after latest %v", run.earliest, run.latest)
		}
		// Same far-future guard as parseScanEndTimestamp.
		if run.latest.After(now.Add(time.Hour)) || run.earliest.After(now.Add(time.Hour)) {
			t.Fatalf("accepted far-future suite times: %+v", run)
		}
	})
}

// FuzzCompletedSuiteTimesTypeConfusion: status.scanStatuses may not be a slice of
// maps (hostile / partial CO objects). Must never panic; always reject.
func FuzzCompletedSuiteTimesTypeConfusion(f *testing.F) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	f.Add(byte(0))
	f.Add(byte(1))
	f.Add(byte(2))
	f.Add(byte(3))
	f.Fuzz(func(t *testing.T, kind byte) {
		var statuses any
		switch kind % 5 {
		case 0:
			statuses = "not-a-slice"
		case 1:
			statuses = []any{"string-entry", 42, true, nil}
		case 2:
			statuses = []any{map[string]any{"phase": "DONE", "endTimestamp": 12345}}
		case 3:
			statuses = []any{}
		default:
			statuses = nil
		}
		suite := &unstructured.Unstructured{Object: map[string]any{
			"status": map[string]any{
				"phase":        "DONE",
				"scanStatuses": statuses,
			},
		}}
		run, ok := completedSuiteTimes(suite, now)
		if ok {
			t.Fatalf("type-confused scanStatuses accepted: kind=%d run=%+v", kind, run)
		}
	})
}
