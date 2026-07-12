package controller

import (
	"testing"
	"time"
)

func TestNextScanTime(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	// Daily at 01:00 -> next is tomorrow 01:00.
	next := nextScanTime("0 1 * * *", now)
	if next == nil {
		t.Fatal("nil for valid schedule")
	}
	want := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	if !next.Time.Equal(want) {
		t.Fatalf("next = %v, want %v", next.Time, want)
	}
	if nextScanTime("not a cron", now) != nil {
		t.Fatal("invalid schedule should yield nil")
	}
	// robfig ParseStandard accepts descriptors, but ScanSetting is intentionally
	// constrained to five-field cron so an annotation cannot request @every 1s.
	if nextScanTime("@every 1s", now) != nil {
		t.Fatal("cron descriptor should be rejected")
	}
	if nextScanTime("@daily", now) != nil {
		t.Fatal("@daily descriptor should be rejected")
	}
	// Parseable but never-firing schedule (fuzz-found): Next returns the zero
	// time; must be nil, not a year-0001 timestamp.
	if got := nextScanTime("*/7 , 1 1 0", now); got != nil {
		t.Fatalf("degenerate schedule should yield nil, got %v", got.Time)
	}
	// Empty / whitespace-only fall back to the default daily schedule so a
	// blank CR field does not Degrade (matches normalizedSchedule).
	for _, empty := range []string{"", "   ", "\t"} {
		got := nextScanTime(empty, now)
		if got == nil || !got.Time.Equal(want) {
			t.Fatalf("empty schedule %q next = %v, want default next %v", empty, got, want)
		}
	}
	// Whitespace-normalized five-field form is accepted.
	if got := nextScanTime("  0  1  *  *  *  ", now); got == nil || !got.Time.Equal(want) {
		t.Fatalf("whitespace-padded schedule next = %v, want %v", got, want)
	}
	// Process-local TZ must not shift NextScanTime: CO ScanSettings fire in UTC.
	// 03:00 in UTC-5 is 08:00 UTC on the same calendar day; next 01:00 UTC fire
	// is still tomorrow 01:00 UTC (not a local-zone-shifted hour).
	loc := time.FixedZone("UTC-5", -5*60*60)
	localNow := time.Date(2026, 7, 10, 3, 0, 0, 0, loc)
	got := nextScanTime("0 1 * * *", localNow)
	if got == nil || !got.Time.Equal(want) {
		t.Fatalf("local-zone now next = %v, want UTC %v", got, want)
	}
}
