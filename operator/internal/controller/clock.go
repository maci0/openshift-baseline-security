package controller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// clock is the operator's wall-clock source. Every production read of "now"
// (requeue cadence, waiver expiry, scan schedule, batch grace timer, history
// timestamps, gauge freshness, log rate limits) goes through it, so a
// deterministic simulation can drive the reconcile loop from a virtual clock
// and replay a run from its seed. Production wires the nil default (real
// clock); a struct literal in a test behaves the same. LastTransitionTime is
// stamped from it too (setCond, sanitize.go), because the install-stall and
// plugin-unavailable graces measure elapsed time against that stamp.
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
func (l *lazyComplianceWatch) sleep(ctx context.Context, d time.Duration) error {
	if l.clock == nil {
		return realClock{}.Sleep(ctx, d)
	}
	return l.clock.Sleep(ctx, d)
}

// Conflict-retry pacing, matching client-go's retry.DefaultRetry (5 attempts,
// 10ms apart) so the reconcile behaves as it did on the real backoff.
const (
	// conflictRetryAttempts is the number of tries a conflict gets, the last
	// one returning the 409 to the caller as a requeue.
	conflictRetryAttempts = 5
	// conflictRetryInterval separates two attempts. Below it a live apiserver
	// cannot have finished the racing write we collided with, so a shorter
	// wait only burns attempts; above it a transient conflict holds the
	// reconcile worker longer than the poll cadence needs.
	conflictRetryInterval = 10 * time.Millisecond
)

// retryOnConflict re-runs fn while the apiserver answers 409, and waits on the
// injected clock between attempts. client-go's retry.RetryOnConflict cannot be
// used here: its backoff sleeps in wall time through a globally seeded math/rand
// jitter, so a conflicting reconcile would consume real time and replay
// differently every run. Here the wait is simulated time and the pacing is
// fixed, so the attempt count for a given conflict sequence is the same on
// every replay.
//
// A cancelled ctx ends the wait with its error rather than starting another
// attempt against a dead reconcile, which is what the sleep would return to a
// real timer too.
func (r *ClusterBaselineReconciler) retryOnConflict(ctx context.Context, fn func() error) error {
	var err error
	for attempt := range conflictRetryAttempts {
		if err = fn(); err == nil || !apierrors.IsConflict(err) {
			return err
		}
		if attempt == conflictRetryAttempts-1 {
			break
		}
		if serr := r.sleep(ctx, conflictRetryInterval); serr != nil {
			return serr
		}
	}
	return err
}

// sleep waits out d on the reconciler's injected clock, defaulting to the real
// one when unset, mirroring now.
func (r *ClusterBaselineReconciler) sleep(ctx context.Context, d time.Duration) error {
	if r.Clock == nil {
		return realClock{}.Sleep(ctx, d)
	}
	return r.Clock.Sleep(ctx, d)
}
