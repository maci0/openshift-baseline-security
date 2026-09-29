import { pickAccountControl, Rect } from './shot';

// Viewport rect with the right edge derived, so a case reads as coordinates.
const rect = (x: number, width: number, height = 30): Rect => ({
  x,
  y: 0,
  width,
  height,
  right: x + width,
});

describe('pickAccountControl', () => {
  it('picks the right-most control, the account chip ahead of the help menu', () => {
    // Masthead order in a console build: hamburger, then help, then the account.
    const controls = [rect(20, 40), rect(1400, 40), rect(1480, 100)];
    expect(pickAccountControl(controls)).toBe(2);
  });

  it('picks by right edge, not by list order', () => {
    // React can mount the account chip before its neighbours.
    const controls = [rect(1480, 100), rect(20, 40), rect(1400, 40)];
    expect(pickAccountControl(controls)).toBe(0);
  });

  it('keeps the first of two controls sharing the right edge', () => {
    const controls = [rect(1480, 100), rect(1400, 180)];
    expect(pickAccountControl(controls)).toBe(0);
  });

  it('ignores a collapsed or hidden control that reports a far right edge', () => {
    const controls = [rect(1480, 0), rect(1480, 100, 0), rect(20, 40)];
    expect(pickAccountControl(controls)).toBe(2);
  });

  it('returns -1 when the masthead has no control to hide', () => {
    expect(pickAccountControl([])).toBe(-1);
  });

  it('returns -1 when every control is collapsed', () => {
    expect(pickAccountControl([rect(10, 0), rect(20, 40, 0)])).toBe(-1);
  });
});
