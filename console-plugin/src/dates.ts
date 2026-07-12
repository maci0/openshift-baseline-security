// Local-calendar date helpers for form inputs and display. Shared by waivers,
// report, and Results UI so timezone edge cases live in one place.

// YYYY-MM-DD for an <input type="date"> min/max/value in the user's local
// calendar. Avoid toISOString().slice(0, 10): that is UTC and shifts the day
// near midnight for non-UTC zones (and always for UTC+ users in the evening).
export const localDateInputValue = (d: Date = new Date()): string => {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
};

// Module-level: form/display paths call date parse/format repeatedly; avoid
// re-binding pattern objects on every keystroke or waiver-expiry tick.
const localDateOnlyRe = /^(\d{4})-(\d{2})-(\d{2})$/;
const localDateOnlyTestRe = /^\d{4}-\d{2}-\d{2}$/;

// Parse YYYY-MM-DD as a local calendar day. Rejects invalid calendar dates
// (e.g. 2026-02-31). Shared by end-of-day deadlines and display formatting so
// timezone edge cases live in one place. `new Date('YYYY-MM-DD')` is UTC midnight
// and must not be used for calendar dates.
const parseLocalDateOnly = (value: string): Date | null => {
  const match = localDateOnlyRe.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const d = new Date(0);
  d.setFullYear(year, month - 1, day);
  d.setHours(0, 0, 0, 0);
  if (d.getFullYear() !== year || d.getMonth() !== month - 1 || d.getDate() !== day) {
    return null;
  }
  return d;
};

// A date-only deadline remains active through the selected local calendar day.
// Parsing YYYY-MM-DD directly as a Date means UTC midnight, which can expire it
// before that day starts locally and display as the previous day in some zones.
export const dateInputEndOfDayIso = (value: string): string | undefined => {
  const d = parseLocalDateOnly(value);
  if (!d) return undefined;
  d.setHours(23, 59, 59, 999);
  return d.toISOString();
};

// Epoch ms for a waiver expiresAt string. A date-only YYYY-MM-DD is treated as
// end of that local calendar day (matching dateInputEndOfDayIso, the value the
// picker writes), so a hand-edited date-only expiry agrees with how it displays
// via formatLocalDate instead of expiring at UTC midnight (up to ~24h early for
// UTC+ users). Returns NaN when unparseable, so callers' Number.isNaN guards hold.
export const expiresAtMs = (iso: string): number => {
  if (localDateOnlyTestRe.test(iso)) {
    const d = parseLocalDateOnly(iso);
    if (!d) return NaN;
    d.setHours(23, 59, 59, 999);
    return d.getTime();
  }
  return new Date(iso).getTime();
};

// Intl APIs expect BCP 47 tags (en-US). Some stacks pass underscore form (en_US);
// normalize that, and validate: toLocale*String throws RangeError on a
// structurally invalid tag, so a malformed document/i18n locale would otherwise
// crash formatting. Fall back to the runtime default (undefined) when invalid.
export const safeLocale = (locale?: string): string | undefined => {
  if (!locale) return undefined;
  const tag = locale.replace(/_/g, '-');
  try {
    return Intl.getCanonicalLocales(tag)[0];
  } catch {
    return undefined;
  }
};

// Display helpers for ISO timestamps from CR/user text. Unparseable values
// return the raw string instead of "Invalid Date" so hand-edits stay debuggable.
const parsedLocalDate = (iso: string): Date | null => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
};

export const formatLocalDate = (iso: string, locale?: string): string => {
  // Date-only: local calendar day only (never fall through to Date('YYYY-MM-DD'),
  // which is UTC midnight and overflows invalid days like 2026-02-31).
  if (localDateOnlyTestRe.test(iso)) {
    const local = parseLocalDateOnly(iso);
    return local ? local.toLocaleDateString(safeLocale(locale)) : iso;
  }
  const d = parsedLocalDate(iso);
  return d ? d.toLocaleDateString(safeLocale(locale)) : iso;
};

export const formatLocalDateTime = (iso: string, locale?: string): string => {
  const d = parsedLocalDate(iso);
  return d ? d.toLocaleString(safeLocale(locale)) : iso;
};

// Locale-aware integer/count display (grouping separators, native digits).
// safeLocale already rejects invalid tags so toLocaleString does not throw.
// Non-finite values (NaN / ±Infinity from corrupt CR scores) return empty so
// the UI never paints the English literals "NaN" or "Infinity".
export const formatCount = (n: number, locale?: string): string =>
  Number.isFinite(n) ? n.toLocaleString(safeLocale(locale)) : '';

// Chart / axis date label from a Date or epoch ms. Invalid instants return
// empty (Victory would otherwise paint the English "Invalid Date" string).
// Locale is validated the same way as formatLocalDate.
export const formatChartDate = (value: Date | number, locale?: string): string => {
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(safeLocale(locale));
};
