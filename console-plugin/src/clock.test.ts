import { clearTimer, now, nowMs, resetClock, setClock, setTimer, setTimers } from './clock';
import { localDateInputValue } from './dates';
import { activeWaivedNames, waiverExpired } from './waivers';
import { Waiver } from './models';

// The plugin's wall clock is the last input a deterministic run has to control.
// These tests pin the seam itself: a frozen instant decides the same way on
// every run, and resetClock hands the real clock back.
const FROZEN = '2026-07-13T00:00:00.000Z';

const waiver = (expiresAt: string): Waiver => ({ name: 'check', expiresAt });

afterEach(() => {
  resetClock();
});

describe('clock seam', () => {
  it('reads the real clock until a source is injected', () => {
    const before = Date.now();
    const read = nowMs();
    const viaDate = now().getTime();
    const after = Date.now();
    // Bracket both reads in one window instead of asserting the two calls landed
    // in the same millisecond: that equality is a race, not evidence, and the
    // real clock (not a frozen instant) is what this pins.
    expect(read).toBeGreaterThanOrEqual(before);
    expect(read).toBeLessThanOrEqual(after);
    expect(viaDate).toBeGreaterThanOrEqual(before);
    expect(viaDate).toBeLessThanOrEqual(after);
  });

  it('freezes every read at the injected instant', () => {
    const fixed = new Date(FROZEN);
    setClock(() => fixed);
    expect(now().toISOString()).toBe(FROZEN);
    expect(nowMs()).toBe(fixed.getTime());
  });

  it('advances when the source is a function rather than a fixed instant', () => {
    let ms = Date.parse(FROZEN);
    setClock(() => new Date(ms));
    expect(nowMs()).toBe(ms);
    ms += 60_000;
    expect(nowMs()).toBe(ms);
  });

  it('restores the real clock on reset', () => {
    setClock(() => new Date(FROZEN));
    resetClock();
    expect(now().toISOString()).not.toBe(FROZEN);
  });
});

describe('timer seam', () => {
  it('routes a scheduled wait through an injected scheduler', () => {
    // The waiver-expiry countdown schedules its tick here. A virtual clock
    // that cannot fire it would leave every simulated run waiting in real time.
    const pending = new Map<number, { at: number; fn: () => void }>();
    const fired: number[] = [];
    let nextId = 1;
    setTimers({
      schedule: (ms, fn) => {
        const id = nextId++;
        pending.set(id, { at: nowMs() + ms, fn });
        return id;
      },
      cancel: (id) => {
        pending.delete(id);
      },
    });
    let ms = Date.parse(FROZEN);
    setClock(() => new Date(ms));

    setTimer(60_000, () => fired.push(ms));
    expect(fired).toEqual([]);
    ms += 60_000;
    // Snapshot before firing: a timer callback may schedule another timer, and
    // this loop must run only the timers that were due when it started.
    for (const [key, timer] of Array.from(pending)) {
      if (timer.at <= ms) {
        pending.delete(key);
        timer.fn();
      }
    }
    expect(fired).toEqual([Date.parse(FROZEN) + 60_000]);

    const stale = setTimer(60_000, () => fired.push(-1));
    clearTimer(stale);
    expect(pending.size).toBe(0);
  });

  it('stops routing to an injected scheduler after reset', () => {
    const scheduled: number[] = [];
    setTimers({
      schedule: (ms) => {
        scheduled.push(ms);
        return 1;
      },
      cancel: () => undefined,
    });
    resetClock();
    try {
      setTimer(0, () => undefined);
    } catch {
      // The default is window.setTimeout and the tests run under the node
      // environment, so reaching the real scheduler throws. Only the absence
      // of a call on the injected one is the assertion.
    }
    expect(scheduled).toEqual([]);
  });
});

describe('helpers that default to the injected instant', () => {
  it('decides waiver expiry from the frozen clock, not wall time', () => {
    setClock(() => new Date(FROZEN));
    expect(waiverExpired(waiver('2026-07-14T00:00:00.000Z'))).toBe(false);
    expect(waiverExpired(waiver('2026-07-12T00:00:00.000Z'))).toBe(true);
    // Date-only expiresAt means end of that local day, so 2026-07-13 has not
    // ended at 00:00 and the waiver is still active.
    expect(waiverExpired(waiver('2026-07-13'))).toBe(false);
  });

  it('reports the same active waiver set on a replay of the same input', () => {
    const waivers = [waiver('2026-07-14T00:00:00.000Z'), waiver('2026-07-01T00:00:00.000Z')];
    setClock(() => new Date(FROZEN));
    const first = [...activeWaivedNames(waivers)].sort();
    resetClock();
    setClock(() => new Date(FROZEN));
    expect([...activeWaivedNames(waivers)].sort()).toEqual(first);
  });

  it('defaults the date input to the injected instant', () => {
    setClock(() => new Date(FROZEN));
    expect(localDateInputValue()).toBe(localDateInputValue(new Date(FROZEN)));
  });
});
