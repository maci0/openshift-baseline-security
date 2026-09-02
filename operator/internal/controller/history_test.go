package controller

import (
	"testing"
	"time"
)

func TestParseScanEndTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	ok, valid := parseScanEndTimestamp("2026-07-09T01:00:00Z", now)
	if !valid || !ok.Equal(time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("basic RFC3339: %v %v", ok, valid)
	}
	// Fractional seconds parse but are truncated to whole seconds: LastScanTime
	// and history snapshots persist as metav1.Time (RFC3339, second precision), so
	// a sub-second value must not recompute larger than its stored form and defeat
	// equal-scan dedup (duplicate history point every reconcile).
	frac, valid := parseScanEndTimestamp("2026-07-09T01:00:00.123456789Z", now)
	if !valid || !frac.Equal(time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("fractional seconds should parse and truncate to the second: %v %v", frac, valid)
	}
	if _, valid = parseScanEndTimestamp("", now); valid {
		t.Fatal("empty should fail")
	}
	if _, valid = parseScanEndTimestamp("not-a-time", now); valid {
		t.Fatal("garbage should fail")
	}
	// Far future must not pin LastScanTime.
	far := now.Add(48 * time.Hour).UTC().Format(time.RFC3339)
	if _, valid = parseScanEndTimestamp(far, now); valid {
		t.Fatal("far-future endTimestamp must be rejected")
	}
	// Modest skew still accepted.
	skew := now.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	if _, valid = parseScanEndTimestamp(skew, now); !valid {
		t.Fatal("near-future within 1h should be accepted")
	}
	// Pre-epoch (corrupt/skewed clock) must be rejected: a negative Unix value
	// would pin LastScanTime and poison the ComplianceScanStale age alert.
	for _, pre := range []string{"0001-01-01T00:00:01Z", "1950-01-01T00:00:00Z", "1969-12-31T23:59:59Z"} {
		if _, valid = parseScanEndTimestamp(pre, now); valid {
			t.Fatalf("pre-epoch endTimestamp %q must be rejected", pre)
		}
	}
	// The Unix epoch itself and after are accepted (real scans are post-1970).
	if _, valid = parseScanEndTimestamp("1970-01-01T00:00:00Z", now); !valid {
		t.Fatal("epoch endTimestamp should be accepted")
	}
}

// FuzzParseScanEndTimestamp: ComplianceScan status.endTimestamp is untrusted
// cluster data. Must never panic; accept only parseable times within 1h skew.
func FuzzParseScanEndTimestamp(f *testing.F) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for _, seed := range []string{
		"", "not-a-time", "2026-07-09T01:00:00Z", "2026-07-09T01:00:00.123456789Z",
		now.Add(30 * time.Minute).UTC().Format(time.RFC3339),
		now.Add(48 * time.Hour).UTC().Format(time.RFC3339),
		"2026-07-10T12:00:00+00:00", "0001-01-01T00:00:00Z",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, ts string) {
		got, ok := parseScanEndTimestamp(ts, now)
		if !ok {
			return
		}
		if got.After(now.Add(time.Hour)) {
			t.Fatalf("accepted far-future timestamp %q -> %v", ts, got)
		}
		// Re-parse must agree (canonical RFC3339 forms only).
		if _, ok2 := parseScanEndTimestamp(got.UTC().Format(time.RFC3339Nano), now); !ok2 {
			t.Fatalf("accepted %q but reformatted value rejected", ts)
		}
	})
}
