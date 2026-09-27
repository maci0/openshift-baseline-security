// Local-calendar date helpers for form inputs and display. Shared by waivers,
// report, and Results UI so timezone edge cases live in one place.
import { now } from './clock';

// YYYY-MM-DD for an <input type="date"> min/max/value in the user's local
// calendar. Avoid toISOString().slice(0, 10): that is UTC and shifts the day
// near midnight for non-UTC zones (and always for UTC+ users in the evening).
export const localDateInputValue = (d: Date = now()): string => {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
};

// Module-level: form/display paths call date parse/format repeatedly; avoid
// re-binding pattern objects on every keystroke or waiver-expiry tick.
const localDateOnlyRe = /^(\d{4})-(\d{2})-(\d{2})$/;

// Parse YYYY-MM-DD as a local calendar day. Rejects invalid calendar dates
// (e.g. 2026-02-31). Shared by end-of-day deadlines and display formatting so
// timezone edge cases live in one place. `new Date('YYYY-MM-DD')` is UTC midnight
// and must not be used for calendar dates. Noon (not midnight) so a valid day
// whose local 00:00 is skipped by a spring-forward (America/Sao_Paulo 2017-10-15,
// America/Santiago 2022-09-11) is not rejected. setFullYear, not `new Date(y,m,d)`,
// so years 0000-0099 stay those years rather than 1900-1999.
const parseLocalDateOnly = (value: string): Date | null => {
  const match = localDateOnlyRe.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const d = new Date(0);
  d.setFullYear(year, month - 1, day);
  d.setHours(12, 0, 0, 0);
  if (d.getFullYear() !== year || d.getMonth() !== month - 1 || d.getDate() !== day) {
    return null;
  }
  return d;
};

// End of the local calendar day for a date-only value (the last instant that
// day is still active), or null when unparseable/invalid. Shared by the ISO
// writer (dateInputEndOfDayIso) and the ms reader (expiresAtMs) so a stored
// expiry and its comparison can never disagree about the day boundary.
// 1ms before the next local midnight, not setHours(23,59,59,999): on a fall-back
// that repeats 23:00 (America/Sao_Paulo 2019-02-16) setHours lands on the first
// 23:59 and would expire the waiver an hour before the calendar day ends.
const endOfLocalDay = (value: string): Date | null => {
  const d = parseLocalDateOnly(value);
  if (!d) return null;
  d.setDate(d.getDate() + 1);
  d.setHours(0, 0, 0, 0);
  return new Date(d.getTime() - 1);
};

// A date-only deadline remains active through the selected local calendar day.
// Parsing YYYY-MM-DD directly as a Date means UTC midnight, which can expire it
// before that day starts locally and display as the previous day in some zones.
export const dateInputEndOfDayIso = (value: string): string | undefined =>
  endOfLocalDay(value)?.toISOString();

// Epoch ms for a waiver expiresAt string. A date-only YYYY-MM-DD is treated as
// end of that local calendar day (same instant dateInputEndOfDayIso writes), so
// a hand-edited date-only expiry agrees with how it displays via formatLocalDate
// instead of expiring at UTC midnight (up to ~24h early for UTC+ users).
// Returns NaN when unparseable, so callers' Number.isNaN guards hold.
export const expiresAtMs = (iso: string): number => {
  const d = endOfLocalDay(iso);
  if (d) return d.getTime();
  // Date-only shape but not a real calendar day (e.g. 2026-02-31): fail closed.
  if (localDateOnlyRe.test(iso)) return NaN;
  return new Date(iso).getTime();
};

// Intl APIs expect BCP 47 tags (en-US). Some stacks pass underscore form (en_US);
// normalize that, and validate: toLocale*String throws RangeError on a
// structurally invalid tag, so a malformed document/i18n locale would otherwise
// crash formatting. Fall back to the runtime default (undefined) when invalid.
//
// Canonicalization is a pure function of the input, and every formatter below
// (count, date, collator, list) calls this on each use: once per card, per
// waiver row, per chart tick, and once per comparison of a sorted rule catalog.
// Intl.getCanonicalLocales allocates and normalizes, so the uncached form spent
// that on strings a console session offers a handful of. Keep one entry per
// distinct tag, the way the formatter maps below keep one per locale. undefined
// (no locale) is returned without touching the map.
const localeTags = new Map<string, string | undefined>();

// Above this many distinct tags the input is not a console locale list (a
// caller feeding unbounded text), so stop growing: drop everything and let the
// few live locales repopulate. Keeps the cache bounded without a TTL on a map
// whose entries never go stale.
const localeTagCacheMax = 64;

export const safeLocale = (locale?: string): string | undefined => {
  if (!locale) return undefined;
  if (localeTags.has(locale)) return localeTags.get(locale);
  const tag = locale.replace(/_/g, '-');
  let canonical: string | undefined;
  try {
    canonical = Intl.getCanonicalLocales(tag)[0];
  } catch {
    canonical = undefined;
  }
  if (localeTags.size >= localeTagCacheMax) {
    localeTags.clear();
  }
  localeTags.set(locale, canonical);
  return canonical;
};

// Upper bound on entries per Intl cache below. The caches exist to reuse
// formatters across renders, and the key is a canonical BCP 47 tag, whose space
// is not the console's language list: i18next reads the tag from the URL and
// from navigator.languages, and any well-formed tag canonicalizes to a distinct
// key, so "en-US-u-ca-buddhist", "en-US-u-nu-arab" and friends each add one
// entry. Unbounded, that grows for the life of the console tab. A cap keeps the
// reuse for a session that toggles between real languages and costs only a
// construction per call once a hostile tag stream is past it.
const INTL_CACHE_MAX = 64;

// Fetch from (or insert into) one of the module-level Intl caches. Past the cap
// the oldest key is dropped, so the map is bounded by INTL_CACHE_MAX entries
// rather than by how many distinct locale tags the tab has seen. Eviction is
// insertion order, not least-recently-used: a hit does not reorder the map, which
// keeps the hot path to one get and no reordering bookkeeping.
export const intlCached = <T>(cache: Map<string, T>, key: string, make: () => T): T => {
  const hit = cache.get(key);
  if (hit !== undefined) {
    return hit;
  }
  const built = make();
  if (cache.size >= INTL_CACHE_MAX) {
    // Map iterates in insertion order, so the first key is the oldest.
    for (const oldest of cache.keys()) {
      cache.delete(oldest);
      break;
    }
  }
  cache.set(key, built);
  return built;
};

// CSS/HTML dir from a BCP 47 tag. Used when document.dir is unset (report
// export, tests) so Arabic/Hebrew/Persian sessions still get RTL chrome.
// Prefers Intl.Locale#getTextInfo (Node 22 / Chromium); falls back to the
// primary language subtag when the Locale Info API is missing.
export const textDirection = (locale?: string): 'ltr' | 'rtl' => {
  const tag = safeLocale(locale);
  if (!tag) return 'ltr';
  try {
    // SAFETY: TypeScript's es2021 Intl.Locale typings omit getTextInfo
    // (Locale Info API). Node 22 and Chromium implement it; the catch covers
    // engines that throw or lack the method.
    const dir = (
      new Intl.Locale(tag) as Intl.Locale & {
        getTextInfo: () => { direction: string };
      }
    ).getTextInfo().direction;
    if (dir === 'rtl') return 'rtl';
  } catch {
    switch (tag.split('-')[0].toLowerCase()) {
      case 'ar':
      case 'fa':
      case 'he':
      case 'ur':
      case 'ps':
      case 'sd':
      case 'yi':
      case 'ckb':
      case 'dv':
        return 'rtl';
      default:
        break;
    }
  }
  return 'ltr';
};

// Display helpers for ISO timestamps from CR/user text. Unparseable values
// return the raw string instead of "Invalid Date" so hand-edits stay debuggable.
const parsedLocalDate = (iso: string): Date | null => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
};

// The instant an absolute cluster timestamp carries, or null when the value is
// absent or not an instant. Callers that hand a timestamp to a component must
// resolve it here first: a component that falls back to the browser's own
// `new Date()` (PatternFly's Timestamp does) renders "now" in place of the
// value, so a corrupt status timestamp would read as a scan that just finished
// and the row would move again on every mount.
export const parseInstant = (iso: string | undefined | null): Date | null =>
  iso ? parsedLocalDate(iso) : null;

// Formatting is the hot path on every card, waiver row, remediation row, and
// chart tick, and each toLocale*String call builds a fresh Intl formatter. The
// engine caches only the runtime default locale, so an explicit console locale
// pays a construction (and the option resolution behind it) per call. Keep one
// formatter per locale tag instead, the way text.ts keeps one Collator.
//
// undefined (the empty key) means the runtime default and is cache-only;
// constructing with a validated canonical tag is what safeLocale returned, so
// these maps add no new way to throw. intlCached caps each map, so the key
// space is bounded even when the locale tag is not one a console session offers.
const numberFormatters = new Map<string, Intl.NumberFormat>();
const dateFormatters = new Map<string, Intl.DateTimeFormat>();

const numberFormatter = (locale?: string): Intl.NumberFormat => {
  const tag = safeLocale(locale);
  return intlCached(numberFormatters, tag ?? '', () => new Intl.NumberFormat(tag));
};

// No component options: that is what Date#toLocaleDateString passes, so the
// output is identical to the call it replaces. It is not Date#toLocaleString
// (date and time), which stays on the built-in because it is only reached by
// the report export, once per row of a downloaded file.
const dateFormatter = (locale?: string): Intl.DateTimeFormat => {
  const tag = safeLocale(locale);
  return intlCached(dateFormatters, tag ?? '', () => new Intl.DateTimeFormat(tag));
};

export const formatLocalDate = (iso: string, locale?: string): string => {
  // Date-only: local calendar day only (never fall through to Date('YYYY-MM-DD'),
  // which is UTC midnight and overflows invalid days like 2026-02-31).
  if (localDateOnlyRe.test(iso)) {
    const local = parseLocalDateOnly(iso);
    return local ? dateFormatter(locale).format(local) : iso;
  }
  const d = parsedLocalDate(iso);
  return d ? dateFormatter(locale).format(d) : iso;
};

export const formatLocalDateTime = (iso: string, locale?: string): string => {
  const d = parsedLocalDate(iso);
  return d ? d.toLocaleString(safeLocale(locale)) : iso;
};

// Locale-aware integer/count display (grouping separators, native digits).
// safeLocale already rejects invalid tags so the formatter cannot throw.
// Non-finite values (NaN / ±Infinity from corrupt CR scores) return empty so
// the UI never paints the English literals "NaN" or "Infinity".
export const formatCount = (n: number, locale?: string): string =>
  Number.isFinite(n) ? numberFormatter(locale).format(n) : '';

// Chart / axis date label from a Date or epoch ms. Invalid instants return
// empty (Victory would otherwise paint the English "Invalid Date" string).
// Locale is validated the same way as formatLocalDate.
export const formatChartDate = (value: Date | number, locale?: string): string => {
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? '' : dateFormatter(locale).format(d);
};
