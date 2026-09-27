package controller

import "time"

// clock is the operator's only wall-clock source. Every production read of
// "now" (requeue cadence, waiver expiry, scan schedule, batch grace timer,
// history timestamps, gauge freshness, log rate limits) goes through it, so a
// deterministic simulation can drive the reconcile loop from a virtual clock
// and replay a run from its seed. Production wires the nil default (real
// clock); a struct literal in a test behaves the same.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// now reads the injected clock, defaulting to the real one when unset, so no
// call site has to distinguish "no clock" from "no time".
func (r *ClusterBaselineReconciler) now() time.Time {
	if r.Clock == nil {
		return realClock{}.Now()
	}
	return r.Clock.Now()
}

// elapsed reports clock time passed since start, so a duration in a log or a
// rate-limit window comes from the same clock as the decision it describes.
func (r *ClusterBaselineReconciler) elapsed(start time.Time) time.Duration {
	return r.now().Sub(start)
}

// now mirrors the reconciler's clock for the lazy watch runnable, which shares
// the virtual time so its retry rate limit matches the loop it feeds.
func (l *lazyComplianceWatch) now() time.Time {
	if l.clock == nil {
		return realClock{}.Now()
	}
	return l.clock.Now()
}
