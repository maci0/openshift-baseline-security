import { now, nowMs, resetClock, setClock } from './clock';
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
    expect(read).toBeGreaterThanOrEqual(before);
    expect(read).toBeLessThanOrEqual(Date.now());
    expect(now().getTime()).toBe(read);
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
