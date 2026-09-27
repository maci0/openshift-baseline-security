package controller

import (
	"context"
	"time"
)

// clock is the operator's wall-clock source. Every production read of "now"
// (requeue cadence, waiver expiry, scan schedule, batch grace timer, history
// timestamps, gauge freshness, log rate limits) goes through it, so a
// deterministic simulation can drive the reconcile loop from a virtual clock
// and replay a run from its seed. Production wires the nil default (real
// clock); a struct literal in a test behaves the same. The one exception is
// sanitize.go stamping a LastTransitionTime on a condition whose value is
// required to be non-zero: nothing compares that stamp against the clock, so
// it cannot make a simulation non-reproducible.
//
// Sleep carries the other half of time: the retry waits in the watch and
// bootstrap runnables. Reading a virtual Now() but blocking a real timer would
// leave those loops running in wall time a simulated run cannot skip, so a
// virtual clock advances its own time there instead of sleeping.
type clock interface {
	Now() time.Time
	// Sleep waits out d, or returns ctx.Err() if ctx is done first.
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

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

// sleep waits out a retry delay on the injected clock, so a simulated run
// spends simulated time on the wait and none of its own.
func (r *ClusterBaselineReconciler) sleep(ctx context.Context, d time.Duration) error {
	if r.Clock == nil {
		return realClock{}.Sleep(ctx, d)
	}
	return r.Clock.Sleep(ctx, d)
}

func (l *lazyComplianceWatch) sleep(ctx context.Context, d time.Duration) error {
	if l.clock == nil {
		return realClock{}.Sleep(ctx, d)
	}
	return l.clock.Sleep(ctx, d)
}
