// Cron expression validation for ClusterBaseline.spec.schedule (5-field form).
import { codePointLength } from './text';

const cronMonths = {
  jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6,
  jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12,
} as const;
const cronDays = {
  sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6,
} as const;

// The operator parses a cron step with robfig's strconv.Atoi (int64). An
// all-digits step that overflows int64 makes it reject the whole schedule
// (InvalidSchedule Degraded), so reject the identical overflow here: otherwise
// the UI reports "Schedule updated" for a string the operator then refuses,
// leaving the CR Degraded until an admin hand-edits it.
const stepFitsInt64 = (digits: string): boolean => {
  const s = digits.replace(/^0+/, '');
  return s.length < 19 || (s.length === 19 && s <= '9223372036854775807');
};

const cronNumber = <T extends Record<string, number>>(
  value: string,
  names?: T,
): number | null => {
  // hasOwnProperty, not a bare index: names is an object literal, so a schedule
  // field reading "constructor" or "toString" would otherwise pick up the
  // inherited member and return a function where the signature promises a
  // number. The comparison below would then be false for every range, so the
  // field is rejected for the wrong reason and the number contract is a lie.
  // Called off Object.prototype, not via the instance, so a name key that
  // shadows hasOwnProperty on the object cannot disarm the check.
  const key = value.toLowerCase();
  if (names && Object.prototype.hasOwnProperty.call(names, key)) {
    return names[key];
  }
  if (!/^\d+$/.test(value)) return null;
  return Number(value);
};

const validCronField = <T extends Record<string, number>>(
  field: string,
  min: number,
  max: number,
  names?: T,
): boolean =>
  field.split(',').every((expression) => {
    if (!expression) return false;
    const stepParts = expression.split('/');
    if (stepParts.length > 2) return false;
    if (
      stepParts.length === 2 &&
      (!/^\d+$/.test(stepParts[1]) ||
        Number(stepParts[1]) <= 0 ||
        !stepFitsInt64(stepParts[1]))
    ) {
      return false;
    }
    const rangeParts = stepParts[0].split('-');
    if (rangeParts.length > 2) return false;
    if (rangeParts[0] === '*' || rangeParts[0] === '?') {
      return rangeParts.length === 1;
    }
    const start = cronNumber(rangeParts[0], names);
    const end = rangeParts.length === 2 ? cronNumber(rangeParts[1], names) : start;
    return start != null && end != null && start >= min && end <= max && start <= end;
  });

// Field separators, exactly the set the operator's strings.Fields splits on
// (Go unicode.IsSpace). JS \s and unicode.IsSpace disagree on two characters,
// and both directions break the console/operator lockstep this validator exists
// to keep:
//
//   U+FEFF  JS-only. "0\ufeff3 * * *" is five fields here and four fields to
//           the operator, so the console patches it and reports the schedule
//           saved, then the CR goes Degraded with InvalidSchedule on the next
//           reconcile. A zero-width no-break space rides in on text pasted from
//           a web page or a word processor.
//   U+0085  operator-only (NEL, C1). Rejecting it here is the safe direction:
//           the console refuses a schedule the operator would have run.
//
// Spelling the set out, rather than \s, is the point; a future \s that grows a
// character would reopen the gap silently. Keep in lockstep with
// normalizeAndParseSchedule's strings.Fields in schedule.go.
//
// Go unicode.IsSpace: the ASCII whitespace controls, space, NEL, NBSP, and the
// Unicode_Space_Separator plus line/paragraph separator blocks. Written as
// escapes so the set stays readable and diffable as plain ASCII.
const CRON_FIELD_SEPARATORS =
  '\t\n\u000b\f\r\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000';
// Global: without it only the leading alternative matches, so a value padded on
// both sides keeps its trailing separators.
const cronTrimRe = new RegExp(`^[${CRON_FIELD_SEPARATORS}]+|[${CRON_FIELD_SEPARATORS}]+$`, 'gu');
const cronSplitRe = new RegExp(`[${CRON_FIELD_SEPARATORS}]+`, 'u');

// Trim the outer separators with the same set the fields are split on, so a
// value the operator would keep as part of a field cannot lose that character
// on the way onto the CR.
export const trimCron = (s: string): string => s.replace(cronTrimRe, '');

// Match the operator's five-field robfig cron parser, including named months /
// weekdays and '?', while rejecting descriptors and out-of-range values before
// the UI patches the CR. Also enforce the CRD MaxLength=128 so a long-but-parseable
// string is not accepted client-side only to fail apiserver admission. Counted in
// code points, the unit the apiserver applies to MaxLength; a schedule the field
// grammar accepts is ASCII anyway, so the two counts agree on every input that
// gets past the field checks below.
export const isValidCron = (s: string): boolean => {
  const trimmed = trimCron(s);
  if (!trimmed || codePointLength(trimmed) > 128) {
    return false;
  }
  const fields = trimmed.split(cronSplitRe);
  return (
    fields.length === 5 &&
    validCronField(fields[0], 0, 59) &&
    validCronField(fields[1], 0, 23) &&
    validCronField(fields[2], 1, 31) &&
    validCronField(fields[3], 1, 12, cronMonths) &&
    validCronField(fields[4], 0, 6, cronDays)
  );
};
