package controller

import (
	"testing"
	"time"
)

// TestNormalizedScheduleTable pins the five-field robfig parser's accept/reject
// decisions on the same token set the console isValidCron validator screens, so
// the two stay in lockstep: a schedule the UI saves must not later Degrade the
// operator with InvalidSchedule (and vice versa). Field placement matters:
// named months/weekdays are only valid in the Month/Dow fields.
func TestNormalizedScheduleTable(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		// '?' is accepted in every field (both parsers treat it as wildcard).
		{"? ? ? ? ?", true},
		{"? * * * *", true},
		{"0 0 ? * ?", true},
		{"0 2 * * ?", true},
		// Named months / weekdays, upper and lower case.
		{"0 0 * jan mon", true},
		{"0 0 * JAN MON", true},
		{"0 0 * * sun", true},
		{"0 0 * * SUN", true},
		{"0 0 * * SUN-SAT", true},
		// Named and numeric ranges with a step, in their correct fields.
		{"0 0 1 JAN-JUN/2 *", true},
		{"0 0 1 jan-jun/2 *", true},
		{"0 0 1 1-12/3 *", true},
		{"0 0 * * mon-fri/2", true},
		// Comma lists.
		{"0,15,30 * * * *", true},
		// Parseable but never-fires (Feb 31): normalize accepts; nextScanTime nils.
		{"0 0 31 2 *", true},
		// Reversed ranges reject.
		{"0 0 1 dec-jan *", false},
		{"5-1 * * * *", false},
		// Out-of-range values reject.
		{"60 * * * *", false},
		{"* * * * 7", false},
		{"*/0 * * * *", false},
		// A step that overflows int64 rejects (strconv.Atoi out of range). The
		// console's isValidCron rejects the same string so the UI never reports a
		// schedule saved that then Degrades the CR. Lockstep with cron.test.ts.
		{"*/99999999999999999999 * * * *", false},
		{"0 0 1 1 1/99999999999999999999", false},
		// Quartz-only and Jenkins-only tokens reject (robfig standard parser).
		{"0 0 L * *", false},
		{"0 0 * * 1#2", false},
		{"H H * * *", false},
		// Descriptors reject: a spec.schedule cannot request @every 1s scan storms.
		{"@weekly", false},
		{"@daily", false},
		{"@every 1s", false},
		// Wrong field count rejects.
		{"* * * *", false},
		{"* * * * * *", false},
	}
	for _, c := range cases {
		_, err := normalizedSchedule(c.in)
		if (err == nil) != c.ok {
			t.Errorf("normalizedSchedule(%q): ok=%v, want %v (err=%v)", c.in, err == nil, c.ok, err)
		}
	}
}

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

func TestScanIntervalSeconds(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		schedule string
		want     float64
	}{
		{"0 1 * * *", 86400},  // daily
		{"0 1 * * 0", 604800}, // weekly (Sunday)
		{"0 * * * *", 3600},   // hourly
		{"", 86400},           // empty -> default daily
		{"not a cron", 0},     // invalid
		{"@daily", 0},         // descriptor rejected (five-field only)
		{"*/7 , 1 1 0", 0},    // parseable but never fires
	}
	for _, c := range cases {
		if got := scanIntervalSeconds(c.schedule, now); got != c.want {
			t.Fatalf("scanIntervalSeconds(%q) = %v, want %v", c.schedule, got, c.want)
		}
	}
}
