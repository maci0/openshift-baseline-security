import {
  dateInputEndOfDayIso,
  expiresAtMs,
  intlCached,
  safeLocale,
  formatLocalDate,
  formatLocalDateTime,
  formatCount,
  formatChartDate,
  localDateInputValue,
  parseInstant,
  textDirection,
} from './dates';
import { randomString } from './testing/fuzz';
import { isString } from './parse';

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
    expect(Number.isNaN(expiresAtMs('2026-02-31'))).toBeTruthy();
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

// Subset of HOSTILE that neither the date-only branch nor `new Date` can turn
// into a Date, so the fallback contract for them is exact: the input is
// returned verbatim. Listed explicitly rather than derived, so the assertion
// states the expectation instead of re-running the parse it checks. Iterated as
// its own list in the fuzz below, so the verbatim claim never sits behind an
// `if` over the wider corpus.
const UNPARSEABLE = new Set([
  '',
  ' ',
  '\0',
  '\u0000\u0001\u0002',
  '\uFFFF',
  'not-a-date',
  '2026-13-01',
  '2026-00-00',
  '9999-99-99',
  '+275760-09-14',
  '2026-02-03T25:61:61Z',
  '275760-09-13T00:00:00.000Z',
  '..-..',
  'true',
  'NaN',
  'Infinity',
  'x'.repeat(1000),
]);

describe('dates throw-safety (fuzz sweep)', () => {
  for (const s of HOSTILE) {
    const label = JSON.stringify(s).slice(0, 40);

    it(`never throws for input ${label}`, () => {
      for (const loc of [undefined, ...HOSTILE]) {
        expect(() => safeLocale(loc)).not.toThrow();
        expect(() => textDirection(loc)).not.toThrow();
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

  // safeLocale memoizes each tag so the formatters, collators, and list
  // formats below it pay one canonicalization per locale, not one per call. A
  // cached answer must equal the uncached one, including for a tag that is
  // invalid (cached as "no tag", not as a miss that recomputes).
  it('safeLocale returns the same answer for a repeated tag', () => {
    const tags = ['', 'en', 'en_US', 'en-US', 'tr', 'ar', 'de-DE', '!!bad!!', 'en_US_'];
    const first = tags.map((t) => safeLocale(t));
    expect(tags.map((t) => safeLocale(t))).toEqual(first);
    // Sweep well past the cache bound so entries are dropped and repopulated.
    for (let i = 0; i < 200; i++) {
      expect(safeLocale('en_US')).toBe('en-US');
      expect(safeLocale('!!bad!!')).toBeUndefined();
    }
  });

  it('dateInputEndOfDayIso rejects non-calendar-dates and end-of-days the rest', () => {
    for (const s of HOSTILE) {
      const iso = dateInputEndOfDayIso(s);
      if (iso === undefined) continue;
      // Only strict YYYY-MM-DD that round-trips to the same calendar day survives.
      expect(s).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      const back = new Date(iso);
      expect(Number.isNaN(back.getTime())).toBeFalsy();
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
      }
    }
    // The exact fallback contract for input the runtime cannot parse at all: the
    // string comes back verbatim rather than as a locale format of some other
    // date. Its own loop, so the claim is not conditional on the wider corpus.
    for (const s of UNPARSEABLE) {
      for (const fmt of [formatLocalDate, formatLocalDateTime]) {
        expect(fmt(s, 'en-US')).toBe(s);
      }
    }
  });

  it('a strict YYYY-MM-DD is formatted, and an impossible one is echoed', () => {
    // The date-only branch parses a local calendar day, so a real one formats.
    expect(formatLocalDate('2026-02-03', 'en-US')).not.toBe('2026-02-03');
    expect(formatLocalDate('2026-02-03', 'en-US')).not.toContain('Invalid Date');
    // A day that is not on the calendar is not a date. new Date('2026-02-31')
    // would overflow it to 3 March; the date-only branch must not.
    expect(formatLocalDate('2026-02-31', 'en-US')).toBe('2026-02-31');
    expect(formatLocalDate('2026-13-01', 'en-US')).toBe('2026-13-01');
    expect(formatLocalDate('2026-00-00', 'en-US')).toBe('2026-00-00');
    expect(formatLocalDate('9999-99-99', 'en-US')).toBe('9999-99-99');
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
describe('formatLocalDate / formatLocalDateTime', () => {
  it('formats parseable ISO timestamps', () => {
    const iso = '2026-07-11T12:00:00.000Z';
    expect(formatLocalDate(iso, 'en-US')).toMatch(/2026/);
    expect(formatLocalDateTime(iso, 'en-US')).toMatch(/2026/);
  });
  it('accepts underscore locale tags (en_US) as BCP 47', () => {
    const iso = '2026-07-11T12:00:00.000Z';
    // Underscore form must not throw or yield Invalid Date; match hyphen form year.
    expect(formatLocalDate(iso, 'en_US')).toMatch(/2026/);
    expect(formatLocalDateTime(iso, 'de_DE')).toMatch(/2026/);
  });
  it('treats YYYY-MM-DD as a local calendar day (not UTC midnight)', () => {
    // `new Date('2026-07-12')` is UTC midnight; in US zones toLocaleDateString
    // would show July 11. Local-calendar parse must keep the selected day.
    const out = formatLocalDate('2026-07-12', 'en-US');
    expect(out).toMatch(/12/);
    expect(out).toMatch(/2026/);
    // Invalid calendar dates fall through to the raw string (not Invalid Date).
    expect(formatLocalDate('2026-02-31', 'en-US')).toBe('2026-02-31');
  });
  it('returns the raw string for unparseable values (never "Invalid Date")', () => {
    expect(formatLocalDate('not-a-date')).toBe('not-a-date');
    expect(formatLocalDateTime('not-a-date')).toBe('not-a-date');
    expect(formatLocalDate('not-a-date')).not.toBe('Invalid Date');
  });
  it('does not throw on structurally invalid locales (Intl throws RangeError)', () => {
    // htmlLang/i18n.language flow in unvalidated; a bad tag must not crash render.
    const iso = '2026-07-11T12:00:00.000Z';
    for (const bad of ['en-', '123', '*', 'e', '!!', 'a-b-c-d']) {
      expect(() => formatLocalDate(iso, bad)).not.toThrow();
      expect(() => formatLocalDateTime(iso, bad)).not.toThrow();
    }
  });
  // ISO comes from CR/user text and locale from the document/i18n; neither is
  // validated upstream, so arbitrary pairs must never throw.
  it('fuzz: never throws for arbitrary iso and locale inputs', () => {
    for (let i = 0; i < 2000; i++) {
      const iso = randomString(i % 40);
      const locale = i % 3 === 0 ? undefined : randomString(i % 8);
      expect(() => formatLocalDate(iso, locale)).not.toThrow();
      expect(() => formatLocalDateTime(iso, locale)).not.toThrow();
    }
  });
});

describe('textDirection', () => {
  it('returns rtl for Arabic, Hebrew, Persian and related tags', () => {
    for (const tag of ['ar', 'ar-SA', 'he', 'he-IL', 'fa', 'fa_IR', 'ur', 'ps']) {
      expect(textDirection(tag)).toBe('rtl');
    }
  });
  it('returns ltr for Latin/CJK tags and for invalid/missing tags', () => {
    for (const tag of ['en', 'en-US', 'de-DE', 'ja', 'zh-CN', undefined, '', '!!']) {
      expect(textDirection(tag)).toBe('ltr');
    }
  });
  it('never throws on hostile tags', () => {
    for (const loc of HOSTILE) {
      expect(() => textDirection(loc)).not.toThrow();
      expect(textDirection(loc) === 'ltr' || textDirection(loc) === 'rtl').toBeTruthy();
    }
  });
});

describe('formatCount', () => {
  it('formats with locale grouping', () => {
    expect(formatCount(1234, 'en-US')).toBe('1,234');
    expect(formatCount(1234, 'de-DE')).toBe('1.234');
  });
  it('uses native digits for ar-SA so a formatted 100 matches the score', () => {
    expect(formatCount(100, 'ar-SA')).toBe('١٠٠');
    expect(formatCount(88, 'ar-SA')).toBe('٨٨');
  });
  it('accepts underscore locale tags and invalid tags without throwing', () => {
    expect(formatCount(42, 'en_US')).toMatch(/42/);
    expect(() => formatCount(42, '!!')).not.toThrow();
  });
  it('returns empty for non-finite values (no English NaN/Infinity)', () => {
    expect(formatCount(NaN, 'en-US')).toBe('');
    expect(formatCount(Infinity, 'en-US')).toBe('');
    expect(formatCount(-Infinity, 'de-DE')).toBe('');
  });
});

describe('formatChartDate', () => {
  it('formats a valid Date with the given locale', () => {
    const d = new Date(2026, 6, 12, 15, 30, 0);
    expect(formatChartDate(d, 'en-US')).toMatch(/2026/);
    expect(formatChartDate(d.getTime(), 'en_US')).toMatch(/2026/);
  });
  it('returns empty for invalid instants (no English Invalid Date)', () => {
    expect(formatChartDate(new Date(NaN), 'en-US')).toBe('');
    expect(formatChartDate(Number.NaN, 'en-US')).toBe('');
  });
  it('does not throw on invalid locale tags', () => {
    expect(() => formatChartDate(new Date(0), '!!')).not.toThrow();
  });
});

describe('localDateInputValue', () => {
  it('formats the local calendar day as YYYY-MM-DD (not UTC)', () => {
    // Midday UTC so local-date vs UTC-date is stable in any common offset.
    const d = new Date(2026, 6, 12, 15, 30, 0);
    expect(localDateInputValue(d)).toBe('2026-07-12');
  });

  it('does not use UTC when local day differs from UTC day', () => {
    // 2026-07-12 01:00 local: toISOString may be the previous UTC day in
    // western zones, or still the 12th in eastern zones. Either way the local
    // calendar day must be 2026-07-12.
    const d = new Date(2026, 6, 12, 1, 0, 0);
    expect(localDateInputValue(d)).toBe('2026-07-12');
    // Contrasting wrong pattern: UTC slice can disagree with local day.
    const utcSlice = d.toISOString().slice(0, 10);
    // When the UTC day differs, the local-day value must not equal it; when
    // both are the 12th the equality above already pins the local behavior.
    expect(utcSlice === '2026-07-12' || localDateInputValue(d) !== utcSlice).toBeTruthy();
  });
});

describe('dateInputEndOfDayIso', () => {
  it('keeps a date-only deadline active through the selected local day', () => {
    const parsed = new Date(dateInputEndOfDayIso('2026-07-12') ?? 'invalid');
    expect(parsed.getFullYear()).toBe(2026);
    expect(parsed.getMonth()).toBe(6);
    expect(parsed.getDate()).toBe(12);
    expect(parsed.getHours()).toBe(23);
    expect(parsed.getMinutes()).toBe(59);
    expect(parsed.getSeconds()).toBe(59);
    expect(parsed.getMilliseconds()).toBe(999);
  });

  // 1ms before the next local midnight, so a DST fall-back that repeats 23:00
  // (America/Sao_Paulo 2019-02-16) uses the later 23:59, not setHours' first one.
  it.each(['2026-07-12', '2019-02-16', '2017-10-15', '2024-02-29', '2026-03-08', '2026-11-01'])(
    'end of %s is 1ms before the next local calendar day',
    (value) => {
      const ms = expiresAtMs(value);
      expect(Number.isNaN(ms)).toBeFalsy();
      const year = Number(value.slice(0, 4));
      const month = Number(value.slice(5, 7));
      const day = Number(value.slice(8, 10));
      const nextMidnight = new Date(0);
      nextMidnight.setFullYear(year, month - 1, day + 1);
      nextMidnight.setHours(0, 0, 0, 0);
      expect(ms).toBe(nextMidnight.getTime() - 1);
      const end = new Date(ms);
      expect(end.getFullYear()).toBe(year);
      expect(end.getMonth()).toBe(month - 1);
      expect(end.getDate()).toBe(day);
      const next = new Date(ms + 1);
      expect(
        next.getFullYear() !== year || next.getMonth() !== month - 1 || next.getDate() !== day,
      ).toBeTruthy();
    },
  );

  it.each(['', '2026-02-30', '2026-13-01', 'not-a-date'])('rejects invalid input %p', (value) => {
    expect(dateInputEndOfDayIso(value)).toBeUndefined();
  });

  // User-typed date input for waiver expires/review; never throws, and a
  // defined result must be a parseable ISO string for a real calendar day.
  it('fuzz: never throws; undefined or valid ISO end-of-day', () => {
    for (let i = 0; i < 2000; i++) {
      const value =
        i % 4 === 0
          ? randomString(i % 20)
          : i % 4 === 1
            ? `${2000 + (i % 50)}-${String((i % 14) + 1).padStart(2, '0')}-${String((i % 32) + 1).padStart(2, '0')}`
            : i % 4 === 2
              ? ''
              : `2026-07-${String((i % 28) + 1).padStart(2, '0')}`;
      const got = dateInputEndOfDayIso(value);
      if (got === undefined) continue;
      expect(isString(got)).toBeTruthy();
      const d = new Date(got);
      expect(Number.isNaN(d.getTime())).toBeFalsy();
      expect(d.getHours()).toBe(23);
      expect(d.getMinutes()).toBe(59);
      expect(d.getSeconds()).toBe(59);
      expect(d.getMilliseconds()).toBe(999);
    }
  });
});

describe('cached Intl formatters', () => {
  // The formatters are cached per locale tag so a render loop constructs one
  // Intl object instead of one per call. This pins the contract that makes that
  // safe: the output is byte-identical to the toLocale*String calls it replaced.
  it('formats exactly like the toLocale*String calls it replaced', () => {
    const iso = '2026-02-03T14:05:09Z';
    const d = new Date(iso);
    for (const locale of ['en-US', 'de-DE', 'ar-SA', 'ja_JP', 'not a tag', undefined]) {
      // safeLocale is the single validation gate; formatting goes through it,
      // so the expected output is the one that gate resolves to.
      const tag = safeLocale(locale);
      expect(formatCount(1234567, locale)).toBe((1234567).toLocaleString(tag));
      expect(formatLocalDate(iso, locale)).toBe(d.toLocaleDateString(tag));
      expect(formatChartDate(d, locale)).toBe(d.toLocaleDateString(tag));
    }
  });

  it('stays stable across repeated calls for the same locale', () => {
    // The cached formatter must not carry state between calls: a shared
    // Intl object that remembered the last value would drift on the 1000th.
    const first = formatCount(1234567, 'en-US');
    for (let i = 0; i < 1000; i++) {
      expect(formatCount(1234567, 'en-US')).toBe(first);
      expect(formatLocalDate('2026-02-03T14:05:09Z', 'en-US')).toBe(
        new Date('2026-02-03T14:05:09Z').toLocaleDateString('en-US'),
      );
    }
  });

  it('bounds the cache when the stream of distinct locale tags is not', () => {
    // The tag comes from the console session, but its space is not the console's
    // language list: every well-formed BCP 47 tag canonicalizes to a distinct
    // key, so a stream of them would otherwise grow the map for the life of the
    // tab. intlCached caps it, and output is unchanged either way.
    const cache = new Map<string, Intl.NumberFormat>();
    // The private-use subtag is the cheapest way to mint many distinct valid
    // tags: "x" accepts any alphanumeric subtag, so each index is a new key.
    const tags = Array.from({ length: 500 }, (_, i) => `en-US-x-${i.toString(16).padStart(8, '0')}`);
    for (const tag of tags) {
      expect(intlCached(cache, tag, () => new Intl.NumberFormat(tag)).format(1234)).toBe(
        new Intl.NumberFormat(tag).format(1234),
      );
    }
    expect(cache.size).toBeLessThanOrEqual(64);
    // A tag still in the working set is still reused, not rebuilt per call.
    const last = tags[tags.length - 1];
    expect(intlCached(cache, last, () => new Intl.NumberFormat(last))).toBe(cache.get(last));
  });
});

describe('parseInstant', () => {
  it('returns the absolute instant a cluster timestamp carries', () => {
    expect(parseInstant('2026-07-12T15:30:00Z')?.toISOString()).toBe('2026-07-12T15:30:00.000Z');
    expect(parseInstant('2026-07-12T15:30:00.123456Z')?.getTime()).toBe(
      Date.parse('2026-07-12T15:30:00.123456Z'),
    );
    // An offset is honored, not dropped: the instant is the same one.
    expect(parseInstant('2026-07-12T17:30:00+02:00')?.toISOString()).toBe(
      '2026-07-12T15:30:00.000Z',
    );
  });

  it('returns null rather than a substitute instant for unusable values', () => {
    // PatternFly's Timestamp substitutes its own new Date() for a missing or
    // unparseable date, so a caller that forwards a null would render "now" in
    // a scan row. A corrupt status value must resolve to null instead.
    expect(parseInstant('not-a-date')).toBeNull();
    expect(parseInstant('2026-02-31T00:00:00Z')).toBeNull();
    expect(parseInstant('')).toBeNull();
    expect(parseInstant(undefined)).toBeNull();
    expect(parseInstant(null)).toBeNull();
  });
});
