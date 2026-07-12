const cronMonths: Record<string, number> = {
  jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6,
  jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12,
};
const cronDays: Record<string, number> = {
  sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6,
};

const cronNumber = (value: string, names?: Record<string, number>): number | null => {
  const named = names?.[value.toLowerCase()];
  if (named != null) return named;
  if (!/^\d+$/.test(value)) return null;
  return Number(value);
};

const validCronField = (
  field: string,
  min: number,
  max: number,
  names?: Record<string, number>,
): boolean =>
  field.split(',').every((expression) => {
    if (!expression) return false;
    const stepParts = expression.split('/');
    if (stepParts.length > 2) return false;
    if (stepParts.length === 2 && (!/^\d+$/.test(stepParts[1]) || Number(stepParts[1]) <= 0)) {
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

// Match the operator's five-field robfig cron parser, including named months /
// weekdays and '?', while rejecting descriptors and out-of-range values before
// the UI patches the CR. Also enforce the CRD MaxLength=128 so a long-but-parseable
// string is not accepted client-side only to fail apiserver admission.
export const isValidCron = (s: string): boolean => {
  const trimmed = s.trim();
  if (!trimmed || trimmed.length > 128) {
    return false;
  }
  const fields = trimmed.split(/\s+/);
  return (
    fields.length === 5 &&
    validCronField(fields[0], 0, 59) &&
    validCronField(fields[1], 0, 23) &&
    validCronField(fields[2], 1, 31) &&
    validCronField(fields[3], 1, 12, cronMonths) &&
    validCronField(fields[4], 0, 6, cronDays)
  );
};
