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

// A date-only deadline remains active through the selected local calendar day.
// Parsing YYYY-MM-DD directly as a Date means UTC midnight, which can expire it
// before that day starts locally and display as the previous day in some zones.
export const dateInputEndOfDayIso = (value: string): string | undefined => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return undefined;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const deadline = new Date(0);
  deadline.setFullYear(year, month - 1, day);
  deadline.setHours(23, 59, 59, 999);
  if (
    deadline.getFullYear() !== year ||
    deadline.getMonth() !== month - 1 ||
    deadline.getDate() !== day
  ) {
    return undefined;
  }
  return deadline.toISOString();
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

// Date-only YYYY-MM-DD as a local calendar day. `new Date('YYYY-MM-DD')` is UTC
// midnight, which formats as the previous day in western zones (and is wrong for
// a calendar date that was never a UTC instant). Invalid calendar days (e.g.
// 2026-02-31) return null so callers keep the raw string.
const parsedDateOnlyLocal = (iso: string): Date | null => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const d = new Date(0);
  d.setFullYear(year, month - 1, day);
  d.setHours(0, 0, 0, 0);
  if (
    d.getFullYear() !== year ||
    d.getMonth() !== month - 1 ||
    d.getDate() !== day
  ) {
    return null;
  }
  return d;
};

export const formatLocalDate = (iso: string, locale?: string): string => {
  // Date-only: local calendar day only (never fall through to Date('YYYY-MM-DD'),
  // which is UTC midnight and overflows invalid days like 2026-02-31).
  if (/^\d{4}-\d{2}-\d{2}$/.test(iso)) {
    const local = parsedDateOnlyLocal(iso);
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
export const formatCount = (n: number, locale?: string): string =>
  n.toLocaleString(safeLocale(locale));
