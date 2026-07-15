package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

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

// scanIntervalSeconds returns the approximate seconds between consecutive scans
// for the schedule (the gap between the next two fires after now), or 0 for an
// invalid/degenerate schedule. Exact for fixed cadences (daily/weekly/hourly);
// for calendar-variable schedules (e.g. monthly) it is the current gap, which
// the ComplianceScanStale alert's margin absorbs. Lets that alert scale its
// staleness threshold with the cadence instead of assuming a daily scan.
func scanIntervalSeconds(schedule string, now time.Time) float64 {
	_, sched, err := normalizeAndParseSchedule(schedule)
	if err != nil {
		return 0
	}
	t1 := sched.Next(now.UTC())
	if t1.IsZero() {
		return 0
	}
	t2 := sched.Next(t1)
	if t2.IsZero() {
		return 0
	}
	return t2.Sub(t1).Seconds()
}
