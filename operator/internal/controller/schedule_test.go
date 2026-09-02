package controller

import (
	"fmt"
	"sync"
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
		_, _, err := normalizeAndParseSchedule(c.in)
		if (err == nil) != c.ok {
			t.Errorf("normalizeAndParseSchedule(%q): ok=%v, want %v (err=%v)", c.in, err == nil, c.ok, err)
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
	// blank CR field does not Degrade (matches normalizeAndParseSchedule).
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
	// US spring-forward 2026-03-08 02:00→03:00 Eastern (07:00 UTC). A local-zone
	// cron would skip or double-fire that hour; UTC daily 01:00 is unchanged.
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation America/New_York: %v", err)
	}
	// 06:30 UTC is 01:30 EST, still before the jump; next 01:00 UTC is 09 Mar.
	spring := time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC).In(eastern)
	springNext := nextScanTime("0 1 * * *", spring)
	springWant := time.Date(2026, 3, 9, 1, 0, 0, 0, time.UTC)
	if springNext == nil || !springNext.Time.Equal(springWant) {
		t.Fatalf("spring-forward next = %v, want UTC %v", springNext, springWant)
	}
	// US fall-back 2026-11-01 02:00→01:00 Eastern (06:00 UTC). Next 01:00 UTC
	// after 05:30 UTC (01:30 EDT, in the repeated hour) is still 02 Nov 01:00 UTC.
	fall := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC).In(eastern)
	fallNext := nextScanTime("0 1 * * *", fall)
	fallWant := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	if fallNext == nil || !fallNext.Time.Equal(fallWant) {
		t.Fatalf("fall-back next = %v, want UTC %v", fallNext, fallWant)
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
		// Irregular cadence: the reported interval must be the LARGEST gap
		// (Fri->Mon 72h), not the midweek 24h one, or ComplianceScanStale
		// false-pages every weekend at the 1.5x threshold.
		{"0 1 * * 1-5", 259200},  // weekdays -> weekend gap
		{"0 1 1,2 * *", 2592000}, // 1st+2nd monthly -> largest 2nd->1st gap (30d)
		// Dense-plus-sparse mix: ~85k five-minute fires precede the first
		// weekend gap, so a fire-capped walk would under-report 300s and
		// false-page. Max gap is Fri 23:55 -> Mon 00:00 = 2d + 5m.
		{"*/5 * * * 1-5", 173100},
		{"", 86400},        // empty -> default daily
		{"not a cron", 0},  // invalid
		{"@daily", 0},      // descriptor rejected (five-field only)
		{"*/7 , 1 1 0", 0}, // parseable but never fires
	}
	for _, c := range cases {
		if got := scanIntervalSeconds(c.schedule, now); got != c.want {
			t.Fatalf("scanIntervalSeconds(%q) = %v, want %v", c.schedule, got, c.want)
		}
	}
}

func resetScanIntervalCache(t *testing.T) {
	t.Helper()
	scanIntervalMu.Lock()
	clear(scanIntervalCache)
	clear(scanIntervalInflight)
	scanIntervalMu.Unlock()
	t.Cleanup(func() {
		scanIntervalMu.Lock()
		clear(scanIntervalCache)
		clear(scanIntervalInflight)
		scanIntervalMu.Unlock()
	})
}

func scanIntervalCacheLen(t *testing.T) int {
	t.Helper()
	scanIntervalMu.Lock()
	defer scanIntervalMu.Unlock()
	return len(scanIntervalCache)
}

// Max gap is a property of the cron, so a later `now` must reuse the memoized
// value rather than walk a different 14-month window.
func TestScanIntervalSecondsCachedAcrossNow(t *testing.T) {
	resetScanIntervalCache(t)
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	first := scanIntervalSeconds("0 1 * * *", now)
	if first != 86400 {
		t.Fatalf("first = %v, want 86400", first)
	}
	later := scanIntervalSeconds("0 1 * * *", now.AddDate(0, 6, 0))
	if later != first {
		t.Fatalf("cached interval = %v, want %v", later, first)
	}
	if n := scanIntervalCacheLen(t); n != 1 {
		t.Fatalf("cache entries = %d, want 1", n)
	}
}

func TestScanIntervalSecondsInvalidNotCached(t *testing.T) {
	resetScanIntervalCache(t)
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	if got := scanIntervalSeconds("not a cron", now); got != 0 {
		t.Fatalf("invalid = %v, want 0", got)
	}
	if n := scanIntervalCacheLen(t); n != 0 {
		t.Fatalf("invalid schedule cached: %d entries", n)
	}
}

func TestScanIntervalCacheBounded(t *testing.T) {
	resetScanIntervalCache(t)
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	for i := 0; i < scanIntervalCacheMax+20; i++ {
		sched := fmt.Sprintf("%d %d * * *", i%60, (i/60)%24)
		if got := scanIntervalSeconds(sched, now); got != 86400 {
			t.Fatalf("scanIntervalSeconds(%q) = %v, want 86400", sched, got)
		}
	}
	if n := scanIntervalCacheLen(t); n > scanIntervalCacheMax {
		t.Fatalf("cache size = %d, want <= %d", n, scanIntervalCacheMax)
	} else if n < scanIntervalCacheMax {
		t.Fatalf("cache size = %d, want cap %d after %d unique keys", n, scanIntervalCacheMax, scanIntervalCacheMax+20)
	}
}

func TestScanIntervalSecondsConcurrent(t *testing.T) {
	resetScanIntervalCache(t)
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	got := make([]float64, n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			got[i] = scanIntervalSeconds("0 1 * * 1-5", now)
		}()
	}
	wg.Wait()
	for i, v := range got {
		if v != 259200 {
			t.Fatalf("goroutine %d = %v, want 259200", i, v)
		}
	}
	if n := scanIntervalCacheLen(t); n != 1 {
		t.Fatalf("cache entries after concurrent fill = %d, want 1", n)
	}
}
