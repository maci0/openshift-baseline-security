// Cron expression validation for ClusterBaseline.spec.schedule (5-field form).
import { codePointLength, GO_SPACE, trimGoSpace } from './text';

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

// Fields split on GO_SPACE, the operator's strings.Fields set; that module
// documents why the two JS and Go sets disagree and must not be \s. Global:
// without it only the leading alternative of a padded value would match.
const cronSplitRe = new RegExp(`[${GO_SPACE}]+`, 'u');

// Match the operator's five-field robfig cron parser, including named months /
// weekdays and '?', while rejecting descriptors and out-of-range values before
// the UI patches the CR. Also enforce the CRD MaxLength=128 so a long-but-parseable
// string is not accepted client-side only to fail apiserver admission. Counted in
// code points, the unit the apiserver applies to MaxLength; a schedule the field
// grammar accepts is ASCII anyway, so the two counts agree on every input that
// gets past the field checks below.
export const isValidCron = (s: string): boolean => {
  const trimmed = trimGoSpace(s);
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
