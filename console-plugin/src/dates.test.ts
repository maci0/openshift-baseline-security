import {
  dateInputEndOfDayIso,
  expiresAtMs,
  safeLocale,
  formatLocalDate,
  formatLocalDateTime,
  formatCount,
  formatChartDate,
} from './dates';

describe('expiresAtMs date-only branch', () => {
  it('treats a bare YYYY-MM-DD expiry as end of the LOCAL calendar day', () => {
    const ms = expiresAtMs('2026-07-11');
    const d = new Date(ms);
    // Local end-of-day (setHours(23,59,59,999)), regardless of the runner TZ.
    expect(d.getFullYear()).toBe(2026);
    expect(d.getMonth()).toBe(6);
    expect(d.getDate()).toBe(11);
    expect(d.getHours()).toBe(23);
    expect(d.getMinutes()).toBe(59);
    // Strictly later than UTC midnight, the buggy `new Date('YYYY-MM-DD')` value
    // that would expire the waiver up to ~24h early for UTC+ users.
    expect(ms).toBeGreaterThan(Date.parse('2026-07-11T00:00:00Z'));
  });
  it('returns NaN for an unparseable date-only-shaped string', () => {
    expect(Number.isNaN(expiresAtMs('2026-02-31'))).toBe(true);
  });
});

// dates.ts parses untrusted strings: ISO timestamps from CRs / hand-edits and
// locale tags from the console document. The comments there claim these never
// throw (safeLocale swallows the RangeError that toLocale*String raises on a
// malformed tag; unparseable ISO falls back to the raw string). No unit test
// pins that, so this is a lightweight fuzz sweep: hammer every entry point with
// hostile inputs and assert the throw-safety and fallback invariants hold.

// A single corpus reused as both the ISO and the locale argument, so every
// string is exercised through both parse paths. Covers null bytes, control
// chars, mixed encodings, calendar overflow, huge/negative years, and the
// underscore locale form safeLocale is meant to normalize.
const HOSTILE = [
  '',
  ' ',
  '\0',
  '￿',
  '\u0000\u0001\u0002',
  'not-a-date',
  '2026-02-31', // invalid calendar day, must not overflow to March
  '2026-13-01',
  '2026-00-00',
  '9999-99-99',
  '0000-01-01',
  '-000001-01-01',
  '2026-2-3', // single digits: not YYYY-MM-DD
  '2026-02-03',
  '2026-02-03T23:59:59.999Z',
  '2026-02-03T25:61:61Z',
  '1970-01-01T00:00:00.000Z',
  '275760-09-13T00:00:00.000Z', // near the max representable Date
  '+275760-09-14', // one day past max: unrepresentable
  'en_US',
  'en-US',
  'EN_us',
  'zz-ZZ-invalid',
  'x'.repeat(1000),
  '日本語',
  '🙂',
  'i-klingon',
  '..-..',
  'true',
  'NaN',
  'Infinity',
];

const NUMBERS = [0, -0, 1, -1, 1e21, -1e21, Number.MAX_SAFE_INTEGER, 0.5, NaN, Infinity, -Infinity];

describe('dates throw-safety (fuzz sweep)', () => {
  for (const s of HOSTILE) {
    const label = JSON.stringify(s).slice(0, 40);

    it(`never throws for input ${label}`, () => {
      for (const loc of [undefined, ...HOSTILE]) {
        expect(() => safeLocale(loc)).not.toThrow();
        expect(() => formatLocalDate(s, loc)).not.toThrow();
        expect(() => formatLocalDateTime(s, loc)).not.toThrow();
        expect(() => formatChartDate(new Date(s), loc)).not.toThrow();
        expect(() => dateInputEndOfDayIso(s)).not.toThrow();
      }
    });
  }

  it('safeLocale only returns tags that are themselves safe to format with', () => {
    for (const loc of HOSTILE) {
      const canonical = safeLocale(loc);
      // A returned tag must not re-introduce the RangeError it exists to prevent.
      expect(() => (0).toLocaleString(canonical)).not.toThrow();
      expect(() => new Date(0).toLocaleDateString(canonical)).not.toThrow();
    }
  });

  it('dateInputEndOfDayIso rejects non-calendar-dates and end-of-days the rest', () => {
    for (const s of HOSTILE) {
      const iso = dateInputEndOfDayIso(s);
      if (iso === undefined) continue;
      // Only strict YYYY-MM-DD that round-trips to the same calendar day survives.
      expect(s).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      const back = new Date(iso);
      expect(Number.isNaN(back.getTime())).toBe(false);
      expect(back.getHours()).toBe(23);
      expect(back.getMinutes()).toBe(59);
      expect(back.getSeconds()).toBe(59);
    }
  });

  it('unparseable ISO falls back to the raw string, never "Invalid Date"', () => {
    for (const s of HOSTILE) {
      for (const fmt of [formatLocalDate, formatLocalDateTime]) {
        const out = fmt(s, 'en-US');
        expect(out).not.toContain('Invalid Date');
        // parseLocalDateOnly / new Date rejection path returns the input verbatim.
        if (out !== s) {
          // Non-fallback path must have produced a non-empty localized string.
          expect(out.length).toBeGreaterThan(0);
        }
      }
    }
  });

  it('formatCount never throws for non-finite numbers or hostile locales', () => {
    for (const n of NUMBERS) {
      for (const loc of [undefined, ...HOSTILE]) {
        expect(() => formatCount(n, loc)).not.toThrow();
      }
    }
    // Non-finite must not paint English "NaN"/"Infinity" in the UI.
    expect(formatCount(NaN)).toBe('');
    expect(formatCount(Infinity)).toBe('');
    expect(formatCount(-Infinity)).toBe('');
  });

  it('formatChartDate never throws and hides Invalid Date', () => {
    for (const loc of [undefined, ...HOSTILE]) {
      expect(() => formatChartDate(new Date(0), loc)).not.toThrow();
      expect(() => formatChartDate(Number.NaN, loc)).not.toThrow();
      expect(formatChartDate(new Date(NaN), loc)).toBe('');
    }
    expect(formatChartDate(new Date(2026, 6, 12), 'en-US')).toMatch(/2026/);
  });
});
