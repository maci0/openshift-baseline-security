package controller

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// errScheduleNeverFires marks a syntactically-valid cron whose next fire never
// resolves (an impossible calendar date, e.g. Feb 30 / April 31). ensureScanConfig
// treats it like a parse error: keep the last-good cron and Degrade, so it does
// not silently never scan while suppressing the stale-scan alert.
var errScheduleNeverFires = errors.New("schedule never fires (impossible calendar date)")

// defaultScanSchedule aliases the API constant so schedule normalize/ScanSetting
// writes stay aligned with the CRD default and console DEFAULT_SCAN_SCHEDULE.
const defaultScanSchedule = baselinev1alpha1.DefaultScanSchedule

var scanScheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// normalizeAndParseSchedule validates five-field cron and returns the
// whitespace-normalized expression plus its parsed schedule (one Parse).
// Whitespace-only is treated as unset so accidental "  " does not Degrade.
// Compliance ScanSettings use standard five-field cron; descriptors such as
// "@every 1s" are rejected so a spec.schedule value cannot create a scan storm.
func normalizeAndParseSchedule(schedule string) (string, cron.Schedule, error) {
	if strings.TrimSpace(schedule) == "" {
		schedule = defaultScanSchedule
	}
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return "", nil, fmt.Errorf("expected exactly 5 fields, found %d", len(fields))
	}
	schedule = strings.Join(fields, " ")
	sched, err := scanScheduleParser.Parse(schedule)
	if err != nil {
		return "", nil, fmt.Errorf("invalid cron schedule %q: %w", schedule, err)
	}
	return schedule, sched, nil
}

func normalizedSchedule(schedule string) (string, error) {
	s, _, err := normalizeAndParseSchedule(schedule)
	return s, err
}

// nextScanTime computes the next cron fire after now, or nil on an invalid
// schedule. An empty schedule normalizes to defaultScanSchedule, so it still
// yields a next-fire time.
//
// Cron is evaluated in UTC: Compliance Operator ScanSettings fire on the
// container clock (UTC by default), and status.nextScanTime must match that
// fire time. Using the process local zone would shift NextScanTime on a node
// with TZ set and disagree with the actual scan.
func nextScanTime(schedule string, now time.Time) *metav1.Time {
	_, sched, err := normalizeAndParseSchedule(schedule)
	if err != nil {
		return nil
	}
	// A degenerate-but-parseable schedule (e.g. an impossible day/month combo)
	// yields the zero time from Next; report no next scan rather than year 0001.
	nextTime := sched.Next(now.UTC())
	if nextTime.IsZero() {
		return nil
	}
	next := metav1.NewTime(nextTime)
	return &next
}

// scanIntervalCache memoizes scanIntervalSeconds per normalized schedule: the
// full-horizon walk below costs ~0.3s for a per-minute cron, too much for every
// metrics publish but fine once per distinct schedule per process lifetime.
// Guarded by a mutex for -race safety in tests; reconcile itself is
// single-threaded. Cleared wholesale if it ever grows past a sanity bound.
var (
	scanIntervalMu    sync.Mutex
	scanIntervalCache = map[string]float64{}
)

// scanIntervalSeconds returns the LARGEST gap between consecutive fires over
// the next ~14 months, or 0 for an invalid/degenerate schedule. The maximum
// (not the next) gap is what the ComplianceScanStale alert must scale by: a
// weekday-only cron's next-two-fires gap is 24h midweek, but the true
// Friday-to-Monday gap is 72h, and reporting 24h would false-page every
// weekend at the 1.5x threshold. The walk covers the WHOLE horizon (no fire
// cap): a dense-plus-sparse mix like "*/5 * * * 1-5" fires ~1.4k times before
// its first weekend gap (~88k over the horizon), so any small cap would
// silently under-report and resurrect the false pages. For fixed cadences
// the max gap equals the only
// gap, so daily/weekly/hourly stay exact; the horizon covers monthly and
// yearly schedules plus one Feb-29 cycle irregularity.
func scanIntervalSeconds(schedule string, now time.Time) float64 {
	norm, sched, err := normalizeAndParseSchedule(schedule)
	if err != nil {
		return 0
	}
	scanIntervalMu.Lock()
	defer scanIntervalMu.Unlock()
	if v, ok := scanIntervalCache[norm]; ok {
		return v
	}
	prev := sched.Next(now.UTC())
	if prev.IsZero() {
		return 0
	}
	horizon := prev.AddDate(1, 2, 0)
	// ~620k iterations for a per-minute cron over 14 months; the hard cap only
	// backstops a pathological parser edge, far above any real schedule.
	var maxGap float64
	for i := 0; i < 1_000_000; i++ {
		next := sched.Next(prev)
		if next.IsZero() {
			break
		}
		if gap := next.Sub(prev).Seconds(); gap > maxGap {
			maxGap = gap
		}
		prev = next
		if prev.After(horizon) {
			break
		}
	}
	if len(scanIntervalCache) > 100 {
		clear(scanIntervalCache)
	}
	scanIntervalCache[norm] = maxGap
	return maxGap
}
