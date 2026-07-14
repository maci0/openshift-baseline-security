import { isValidCron } from './cron';

// cron.ts validates ClusterBaseline.spec.schedule, a free-text field a user
// types (or a hand-edited CR carries) before the console patches it onto the CR.
// isValidCron is the client-side gate against a string that would fail apiserver
// admission or, worse, parse into an unintended schedule. Its comments promise a
// pure boolean verdict with a MaxLength=128 cap; nothing pins that under hostile
// input. This is a fuzz sweep: hammer the validator with malformed, oversized,
// and adversarial field values and assert the throw-safety and cap invariants.

// Structural corpus: empty/whitespace, wrong field counts, step/range abuse,
// out-of-range numbers, named months/weekdays (valid and bogus), control/unicode
// bytes, and ReDoS-shaped repetition. Reused verbatim as every field.
const HOSTILE = [
  '',
  ' ',
  '\0',
  '*',
  '* * * * *',
  '*/5 * * * *',
  '0 0 1 1 0',
  '0 0 1 JAN SUN',
  '0 0 1 jan sun',
  '0 0 1 foo bar',
  '60 * * * *', // minute out of range
  '* 24 * * *', // hour out of range
  '* * 0 * *', // day-of-month below min
  '* * * 13 *', // month out of range
  '* * * * 7', // weekday out of range
  '5-1 * * * *', // inverted range
  '*/0 * * * *', // zero step
  '*/-1 * * * *',
  '*/99999999999999999999 * * * *', // step overflows int64: operator rejects
  '0 0 1 1 1/99999999999999999999', // overflow step in the weekday field
  '1/2/3 * * * *', // too many step parts
  '1-2-3 * * * *', // too many range parts
  '? ? ? ? ?',
  '1,2,3 * * * *',
  '1,,2 * * * *', // empty list element
  '* * * *', // four fields
  '* * * * * *', // six fields
  '   * * * * *   ', // surrounds trimmed
  '*\t*\n* * *', // mixed whitespace separators
  '@daily', // descriptor, rejected
  'x'.repeat(200), // over the 128 cap
  ('1,'.repeat(500) + '1') + ' * * * *', // long but structured: cap must still reject
  '日 本 語 * *',
  '🙂 * * * *',
  'NaN * * * *',
  'Infinity * * * *',
  '0x1 * * * *',
  '01 * * * *', // leading zero, still numeric
];

describe('isValidCron accept/reject behavior', () => {
  // Without these the throw-safety sweep passes even if isValidCron always
  // returned false: pin that real schedules are accepted and bad ones rejected.
  it('accepts well-formed five-field schedules', () => {
    for (const s of [
      '* * * * *',
      '*/5 * * * *',
      '0 0 1 1 0',
      '0 0 1 JAN SUN',
      '0 0 1 jan sun',
      '1,2,3 * * * *',
      '5-10 * * * *',
      '0 0 * * MON-FRI',
      '   * * * * *   ',
      // Lockstep with operator TestNormalizedScheduleTable: '?' in any field,
      // named/numeric ranges with a step, comma lists, and a parseable
      // never-fires date (Feb 31) must all be accepted on both sides.
      '? ? ? ? ?',
      '0 2 * * ?',
      '0 0 1 JAN-JUN/2 *',
      '0 0 1 jan-jun/2 *',
      '0 0 1 1-12/3 *',
      '0 0 * * mon-fri/2',
      '0,15,30 * * * *',
      '0 0 31 2 *',
      // A large but int64-parseable step must stay accepted on both sides: the
      // overflow guard must not over-reject what the operator's robfig accepts.
      '*/1000000 * * * *',
    ]) {
      expect(isValidCron(s)).toBe(true);
    }
  });
  it('rejects out-of-range, malformed, and oversized schedules', () => {
    for (const s of [
      '60 * * * *',
      '* 24 * * *',
      '* * 0 * *',
      '* * * 13 *',
      '* * * * 7',
      '5-1 * * * *',
      '*/0 * * * *',
      '@daily',
      '* * * *',
      '* * * * * *',
      'x'.repeat(200),
      // Lockstep with operator TestNormalizedScheduleTable: reversed named
      // range, and Quartz/Jenkins-only tokens the robfig standard parser (and
      // thus the operator) rejects, must be rejected client-side too.
      '0 0 1 DEC-JAN *',
      '0 0 L * *',
      '0 0 * * 1#2',
      'H H * * *',
    ]) {
      expect(isValidCron(s)).toBe(false);
    }
  });
});

describe('isValidCron throw-safety (fuzz sweep)', () => {
  for (const s of HOSTILE) {
    const label = JSON.stringify(s).slice(0, 40);
    it(`returns a strict boolean and never throws for ${label}`, () => {
      let out: unknown;
      expect(() => {
        out = isValidCron(s);
      }).not.toThrow();
      expect(typeof out).toBe('boolean');
    });
  }

  it('never accepts a string longer than the CRD MaxLength=128', () => {
    for (const s of HOSTILE) {
      if (s.trim().length > 128) {
        expect(isValidCron(s)).toBe(false);
      }
    }
  });

  it('never accepts anything other than exactly five whitespace fields', () => {
    for (const s of HOSTILE) {
      if (!isValidCron(s)) continue;
      const fields = s.trim().split(/\s+/);
      expect(fields).toHaveLength(5);
    }
  });
});
