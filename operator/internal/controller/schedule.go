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

func normalizedSchedule(schedule string) (string, error) {
	// Whitespace-only is treated as unset so accidental "  " does not Degrade.
	if strings.TrimSpace(schedule) == "" {
		schedule = defaultScanSchedule
	}
	// Compliance ScanSettings use standard five-field cron. ParseStandard also
	// accepts descriptors such as "@every 1s"; allowing those here would bypass
	// the intended cadence and can create an unbounded scan storm.
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return "", fmt.Errorf("expected exactly 5 fields, found %d", len(fields))
	}
	schedule = strings.Join(fields, " ")
	if _, err := scanScheduleParser.Parse(schedule); err != nil {
		return "", fmt.Errorf("invalid cron schedule %q: %w", schedule, err)
	}
	return schedule, nil
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
	normalized, err := normalizedSchedule(schedule)
	if err != nil {
		return nil
	}
	sched, err := scanScheduleParser.Parse(normalized)
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
