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

const (
	// scanIntervalCacheMax is well above one live spec.schedule (the singleton
	// CR has one cron). Extra slots cover process-lifetime edits and tests.
	// At the cap, one arbitrary entry is dropped so a burst of unique keys
	// cannot flush the working set and recompute every schedule at once.
	scanIntervalCacheMax = 100
	// scanIntervalWalkMax backstops a cron.Next that never crosses the
	// 14-month horizon. Five-field cron cannot exceed ~620k minute fires
	// over that window.
	scanIntervalWalkMax = 1_000_000
)

// scanIntervalCache memoizes scanIntervalSeconds per normalized schedule: the
// full-horizon walk below costs ~0.3s for a per-minute cron, too much for every
// metrics publish but fine once per distinct schedule per process lifetime.
// Max gap is a property of the cron, not of `now`, so the key is the normalized
// expression. A truncated walk is not stored: an underestimate would false-page
// ComplianceScanStale at 1.5x. Inflight channels collapse concurrent misses of
// the same key so a per-minute cron cannot be walked N times at once.
var (
	scanIntervalMu       sync.Mutex
	scanIntervalCache    = map[string]float64{}
	scanIntervalInflight = map[string]chan struct{}{}
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
	for {
		scanIntervalMu.Lock()
		if v, ok := scanIntervalCache[norm]; ok {
			scanIntervalMu.Unlock()
			return v
		}
		if wait, busy := scanIntervalInflight[norm]; busy {
			scanIntervalMu.Unlock()
			<-wait
			continue
		}
		done := make(chan struct{})
		scanIntervalInflight[norm] = done
		scanIntervalMu.Unlock()

		maxGap, complete := computeScanInterval(sched, now)

		scanIntervalMu.Lock()
		delete(scanIntervalInflight, norm)
		if complete {
			if _, exists := scanIntervalCache[norm]; !exists && len(scanIntervalCache) >= scanIntervalCacheMax {
				for k := range scanIntervalCache {
					delete(scanIntervalCache, k)
					break
				}
			}
			scanIntervalCache[norm] = maxGap
		}
		close(done)
		scanIntervalMu.Unlock()
		return maxGap
	}
}

// computeScanInterval walks consecutive fires from now. complete is false when
// the iteration cap is hit before the horizon, so the caller must not cache
// maxGap (it may under-report the true weekend/month gap).
func computeScanInterval(sched cron.Schedule, now time.Time) (float64, bool) {
	prev := sched.Next(now.UTC())
	if prev.IsZero() {
		return 0, true
	}
	horizon := prev.AddDate(1, 2, 0)
	var maxGap float64
	for i := 0; i < scanIntervalWalkMax; i++ {
		next := sched.Next(prev)
		if next.IsZero() {
			return maxGap, true
		}
		if gap := next.Sub(prev).Seconds(); gap > maxGap {
			maxGap = gap
		}
		prev = next
		if prev.After(horizon) {
			return maxGap, true
		}
	}
	return maxGap, false
}
