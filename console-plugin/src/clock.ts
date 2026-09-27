// The plugin's only wall-clock source. Waiver expiry, the rescan token, the
// report timestamp, and the date-input default all read "now" through here, so
// a deterministic driver can freeze it and replay a run from its inputs. The
// operator has the same seam in internal/controller/clock.go; keep the two in
// step. Production leaves the source at the real clock, and a caller that wants
// a different instant passes one in (the `now: Date = now()` parameters).
//
// Injection exists for tests and simulation, which is its only caller: there is
// no production path that swaps the source, so a leaked override would be a bug
// in the test, not a feature.

type ClockSource = () => Date;

const realClock: ClockSource = () => new Date();

let source: ClockSource = realClock;

export const now = (): Date => source();

// Epoch ms, for callers that compare against expiresAtMs or schedule a timer.
export const nowMs = (): number => source().getTime();

// Point every later read at `s`. Pass a function, not a Date: a fixed Date would
// be a correct first step and a silent hang at the second one.
export const setClock = (s: ClockSource): void => {
  source = s;
};

export const resetClock = (): void => {
  source = realClock;
};
