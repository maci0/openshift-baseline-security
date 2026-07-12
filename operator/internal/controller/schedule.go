package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultScanSchedule matches the CRD default for ClusterBaselineSpec.schedule.
const defaultScanSchedule = "0 1 * * *"

var scanScheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func normalizedSchedule(schedule string) (string, error) {
	if schedule == "" {
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
		return "", err
	}
	return schedule, nil
}

// nextScanTime computes the next cron fire after now, or nil on an invalid or
// empty schedule.
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
	nextTime := sched.Next(now)
	if nextTime.IsZero() {
		return nil
	}
	next := metav1.NewTime(nextTime)
	return &next
}
