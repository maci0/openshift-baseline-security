package controller

import (
	"testing"
	"time"
)

func TestGraceMinutes(t *testing.T) {
	// The grace constants these messages derive from are whole minutes, so the
	// rendering must be exact there. Every non-whole-minute case is the one
	// int(d.Minutes()) got wrong: it truncated toward zero and rendered a grace
	// SHORTER than the one the code applies, so an operator reading
	// "Pending >1m" for a 90s grace would check before the condition could fire.
	for _, tc := range []struct {
		name string
		d    time.Duration
		want int
	}{
		{"whole minutes are exact", 5 * time.Minute, 5},
		{"zero", 0, 0},
		{"negative is not a grace", -time.Minute, 0},
		{"one minute", time.Minute, 1},
		{"one second rounds up to a minute", time.Second, 1},
		{"sub-minute grace still reads as a minute", 90 * time.Second, 2},
		{"one nanosecond over a minute rounds up", time.Minute + 1, 2},
		{"one nanosecond under a minute stays", time.Minute - 1, 1},
		{"exactly on the boundary", 2 * time.Minute, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := graceMinutes(tc.d); got != tc.want {
				t.Errorf("graceMinutes(%v) = %d, want %d", tc.d, got, tc.want)
			}
		})
	}
	// The message must never round below the window it describes, for any input
	// a Duration can hold.
	for _, d := range []time.Duration{
		time.Nanosecond, 1500 * time.Millisecond, 89 * time.Second,
		119 * time.Second, 23*time.Hour + 59*time.Minute, 30 * 24 * time.Hour,
	} {
		if got, floor := graceMinutes(d), int(d.Minutes()); got < floor {
			t.Errorf("graceMinutes(%v) = %d, below the truncating floor %d", d, got, floor)
		}
		if got := graceMinutes(d); time.Duration(got)*time.Minute < d {
			t.Errorf("graceMinutes(%v) = %d, renders a window shorter than the grace", d, got)
		}
	}
}
