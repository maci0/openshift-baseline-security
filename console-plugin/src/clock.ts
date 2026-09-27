// The plugin's only wall-clock source. Waiver expiry, the rescan token, the
// report timestamp, and the date-input default all read "now" through here, so
// a deterministic driver can freeze it and replay a run from its inputs. The
// wait that follows such a read goes through setTimer, so a driver advancing
// virtual time also fires the tick the wait was for. The operator has the same
// seam in internal/controller/clock.go; keep the two in step. Production leaves
// the source at the real clock, and a caller that wants a different instant
// passes one in (the `now: Date = now()` parameters).
//
// Injection exists for tests and simulation, which is its only caller: there is
// no production path that swaps the source, so a leaked override would be a bug
// in the test, not a feature.

type ClockSource = () => Date;

// A scheduler that only fires when its owner moves time. Paired with the clock
// on purpose: a virtual instant that never advances its own timers leaves the
// re-render waiting in real time, which is the same wall-clock leak the
// operator's clock.Sleep closes on the Go side.
interface Timers {
  schedule(ms: number, fn: () => void): number;
  cancel(id: number): void;
}

const realTimers: Timers = {
  schedule: (ms, fn) => window.setTimeout(fn, ms),
  cancel: (id) => window.clearTimeout(id),
};

const realClock: ClockSource = () => new Date();

let source: ClockSource = realClock;
let timers: Timers = realTimers;

export const now = (): Date => source();

// Epoch ms, for callers that compare against expiresAtMs or schedule a timer.
export const nowMs = (): number => source().getTime();

// setTimer/clearTimer are the only scheduling calls on a path a virtual clock
// drives. Anything else that waits (a download's object-URL revoke, a toast
// auto-dismiss) paints nothing and reconciles nothing, so it stays on the
// browser's own timers.
export const setTimer = (ms: number, fn: () => void): number => timers.schedule(ms, fn);

export const clearTimer = (id: number): void => timers.cancel(id);

// Point every later read at `s`. Pass a function, not a Date: a fixed Date would
// be a correct first step and a silent hang at the second one.
export const setClock = (s: ClockSource): void => {
  source = s;
};

// Swap the scheduler. A virtual clock that leaves this alone still ticks in real
// time, so a driver sets both.
export const setTimers = (t: Timers): void => {
  timers = t;
};

export const resetClock = (): void => {
  source = realClock;
  timers = realTimers;
};
