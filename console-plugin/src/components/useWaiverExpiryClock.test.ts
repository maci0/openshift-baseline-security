import { waiversContentKey } from './useWaiverExpiryClock';

const NUL = String.fromCharCode(0);
const SOH = String.fromCharCode(1);

describe('waiversContentKey', () => {
  it('is empty for missing or empty waivers', () => {
    expect(waiversContentKey(undefined)).toBe('');
    expect(waiversContentKey([])).toBe('');
  });

  it('is stable across reallocations of the same waivers', () => {
    const a = [
      { name: 'rule_a', expiresAt: '2026-01-01T00:00:00Z' },
      { name: 'rule_b' },
    ];
    const b = a.map((w) => ({ ...w }));
    expect(waiversContentKey(a)).toBe(waiversContentKey(b));
  });

  it('changes when a name, an expiry, or the count changes', () => {
    const base = [
      { name: 'rule_a', expiresAt: '2026-01-01T00:00:00Z' },
      { name: 'rule_b' },
    ];
    expect(waiversContentKey(base)).not.toBe(
      waiversContentKey([{ name: 'rule_z', expiresAt: '2026-01-01T00:00:00Z' }, base[1]]),
    );
    expect(waiversContentKey(base)).not.toBe(
      waiversContentKey([{ ...base[0], expiresAt: '2026-02-01T00:00:00Z' }, base[1]]),
    );
    expect(waiversContentKey(base)).not.toBe(waiversContentKey(base.slice(0, 1)));
  });

  it('does not let a waiver name forge another waiver set key', () => {
    // The old NUL/SOH interpolation gave both sets the same key, so the expiry
    // effect never rescheduled and an expiring waiver kept its old label.
    const two = [{ name: 'a', expiresAt: 'b' }, { name: 'c' }];
    const forged = [{ name: `a${NUL}b${SOH}c` }];
    expect(waiversContentKey(two)).not.toBe(waiversContentKey(forged));
  });
});
