import { isValidCron } from './cron';
import { isValidK8sName } from './names';
import { batchApplyPatch, batchApplyRequested, remediationApplyPatch, rescanPatch, resourceVersionTest, schedulePatch, tailoredProfileBindingPatch } from './patches';
import { fuzzRand, randomString } from './testing/fuzz';

describe('remediationApplyPatch', () => {
  it('adds the leaf when spec.remediation exists so absent defaulted fields are tolerated', () => {
    expect(remediationApplyPatch(true, true)).toEqual([
      { op: 'add', path: '/spec/remediation/apply', value: 'Automatic' },
    ]);
    expect(remediationApplyPatch(true, false)).toEqual([
      { op: 'add', path: '/spec/remediation/apply', value: 'Manual' },
    ]);
  });
  it('adds the parent object when spec.remediation is absent', () => {
    expect(remediationApplyPatch(false, true)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { apply: 'Automatic' } },
    ]);
    expect(remediationApplyPatch(false, false)).toEqual([
      { op: 'add', path: '/spec/remediation', value: { apply: 'Manual' } },
    ]);
  });
  it('fuzz: always a single op carrying a valid enum value', () => {
    for (const has of [true, false]) {
      for (const automatic of [true, false]) {
        const patch = remediationApplyPatch(has, automatic);
        expect(patch).toHaveLength(1);
        const v = patch[0].value;
        const apply = typeof v === 'string' ? v : (v as { apply: string }).apply;
        expect(['Automatic', 'Manual']).toContain(apply);
      }
    }
  });
});

describe('rescanPatch', () => {
  it('adds nested annotation when annotations map exists', () => {
    expect(rescanPatch(true, 't1')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations/compliance.openshift.io~1rescan',
        value: 't1',
      },
    ]);
  });
  it('adds the annotations object when missing', () => {
    expect(rescanPatch(false, 't2')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations',
        value: { 'compliance.openshift.io/rescan': 't2' },
      },
    ]);
  });
  it('guards whole-map creation against concurrent annotation writes', () => {
    expect(rescanPatch(false, 't3', '42')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '42' },
      {
        op: 'add',
        path: '/metadata/annotations',
        value: { 'compliance.openshift.io/rescan': 't3' },
      },
    ]);
  });
  // Nested annotation add cannot clobber siblings, so resourceVersion is unused
  // when the annotations map already exists.
  it('does not resourceVersion-guard a nested annotation add', () => {
    expect(rescanPatch(true, 't4', '99')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations/compliance.openshift.io~1rescan',
        value: 't4',
      },
    ]);
  });
  it('is a no-op for empty or whitespace-only tokens', () => {
    expect(rescanPatch(true, '')).toEqual([]);
    expect(rescanPatch(false, '   ')).toEqual([]);
    expect(rescanPatch(true, '\t', '99')).toEqual([]);
  });
  it('trims the rescan token before writing', () => {
    expect(rescanPatch(true, '  t5  ')).toEqual([
      {
        op: 'add',
        path: '/metadata/annotations/compliance.openshift.io~1rescan',
        value: 't5',
      },
    ]);
  });
  it('fuzz: last op carries the token; RV guard only for whole-map create', () => {
    for (let i = 0; i < 100; i++) {
      const token = String(i);
      const hasAnnotations = i % 2 === 0;
      const rv = i % 3 === 0 ? String(i) : undefined;
      const p = rescanPatch(hasAnnotations, token, rv);
      const expectGuard = !hasAnnotations && rv != null;
      expect(p).toHaveLength(expectGuard ? 2 : 1);
      if (expectGuard) {
        expect(p[0]).toEqual({ op: 'test', path: '/metadata/resourceVersion', value: rv });
      }
      const last = p[p.length - 1];
      if (hasAnnotations) {
        expect(last.value).toBe(token);
      } else {
        expect(
          (last.value as { 'compliance.openshift.io/rescan': string })[
            'compliance.openshift.io/rescan'
          ],
        ).toBe(token);
      }
    }
  });
});

describe('schedule editor helpers', () => {
  it('isValidCron accepts 5 fields, rejects otherwise', () => {
    expect(isValidCron('0 1 * * *')).toBeTruthy();
    expect(isValidCron('*/5 0-6 1,15 * 1-5')).toBeTruthy();
    expect(isValidCron('0 1 * JAN MON-FRI')).toBeTruthy();
    expect(isValidCron('0 1 ? * MON')).toBeTruthy();
    expect(isValidCron('0 1 * *')).toBeFalsy(); // 4 fields
    expect(isValidCron('0 1 * * * *')).toBeFalsy(); // 6 fields
    expect(isValidCron('not a cron')).toBeFalsy();
    expect(isValidCron('@every 1s')).toBeFalsy();
    expect(isValidCron('60 1 * * *')).toBeFalsy();
    expect(isValidCron('0 24 * * *')).toBeFalsy();
    expect(isValidCron('0 1 * * 7')).toBeFalsy();
    expect(isValidCron('*/0 1 * * *')).toBeFalsy();
    expect(isValidCron('')).toBeFalsy();
    // CRD MaxLength=128: a long-but-five-field string must not pass client-side.
    expect(isValidCron(`0 1 * * ${'1'.repeat(200)}`)).toBeFalsy();
  });

  // Schedule editor feeds free-form text into isValidCron before patching the
  // CR; arbitrary input must never throw and must only accept 5-field forms.
  it('fuzz: isValidCron never throws; true implies five fields', () => {
    const seeds = [
      '0 1 * * *',
      '*/5 0-6 1,15 * 1-5',
      '@daily',
      '',
      '0 1 * * * *',
      '0 1 * JAN MON',
      '60 1 * * *',
    ];
    for (let i = 0; i < 2000; i++) {
      const s = i < seeds.length ? seeds[i] : randomString(i % 64);
      let ok: boolean;
      expect(() => {
        ok = isValidCron(s);
      }).not.toThrow();
      expect(typeof ok!).toBe('boolean');
      if (ok!) {
        expect(s.trim().split(/\s+/)).toHaveLength(5);
      }
    }
  });

  it('schedulePatch always adds a valid cron (creates or replaces the leaf)', () => {
    expect(schedulePatch('0 2 * * *')).toEqual([
      { op: 'add', path: '/spec/schedule', value: '0 2 * * *' },
    ]);
    // Trims before validate/write so surrounding whitespace does not fail admission.
    expect(schedulePatch('  0 3 * * *  ')).toEqual([
      { op: 'add', path: '/spec/schedule', value: '0 3 * * *' },
    ]);
  });

  it('schedulePatch is a no-op for invalid cron (fail closed before admission)', () => {
    expect(schedulePatch('')).toEqual([]);
    expect(schedulePatch('@every 1s')).toEqual([]);
    expect(schedulePatch('0 1 * *')).toEqual([]);
    expect(schedulePatch('60 1 * * *')).toEqual([]);
  });
});

describe('resourceVersionTest', () => {
  // Optimistic concurrency op prepended to every ClusterBaseline mutation path.
  it('emits a resourceVersion test op when RV is known', () => {
    expect(resourceVersionTest('42')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '42' },
    ]);
  });
  it('emits nothing when RV is missing (no false conflict)', () => {
    expect(resourceVersionTest(undefined)).toEqual([]);
    expect(resourceVersionTest('')).toEqual([]);
  });
});

describe('tailoredProfileBindingPatch', () => {
  it('is idempotent when the profile is already bound', () => {
    expect(tailoredProfileBindingPatch(['custom'], 'custom', '12')).toEqual([]);
  });
  it('guards and appends to an existing list', () => {
    expect(tailoredProfileBindingPatch(['old'], 'custom', '12')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '12' },
      { op: 'test', path: '/spec/tailoredProfiles', value: ['old'] },
      { op: 'add', path: '/spec/tailoredProfiles/-', value: 'custom' },
    ]);
  });
  it('guards creation of an absent list against concurrent replacement', () => {
    expect(tailoredProfileBindingPatch(undefined, 'custom', '12')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '12' },
      { op: 'add', path: '/spec/tailoredProfiles', value: ['custom'] },
    ]);
  });
  // Without resourceVersion the list-test/add still applies, but no RV guard.
  it('appends without a resourceVersion guard when RV is absent', () => {
    expect(tailoredProfileBindingPatch(['old'], 'custom')).toEqual([
      { op: 'test', path: '/spec/tailoredProfiles', value: ['old'] },
      { op: 'add', path: '/spec/tailoredProfiles/-', value: 'custom' },
    ]);
    expect(tailoredProfileBindingPatch(undefined, 'custom')).toEqual([
      { op: 'add', path: '/spec/tailoredProfiles', value: ['custom'] },
    ]);
  });
  it('is a no-op for invalid tailored names (CRD MaxLength 51 / DNS-1123)', () => {
    expect(tailoredProfileBindingPatch(undefined, '')).toEqual([]);
    expect(tailoredProfileBindingPatch(undefined, 'Bad_Name')).toEqual([]);
    expect(tailoredProfileBindingPatch(undefined, 'a'.repeat(52))).toEqual([]);
  });
  it('refuses a new bind past CRD MaxItems=32 (already-bound stays no-op)', () => {
    const full = Array.from({ length: 32 }, (_, i) => `tp-${i}`);
    expect(tailoredProfileBindingPatch(full, 'tp-new', '9')).toEqual([]);
    // Already bound: still a no-op even at the limit (not an over-limit reject path).
    expect(tailoredProfileBindingPatch(full, 'tp-0', '9')).toEqual([]);
  });
});

describe('batchApplyPatch', () => {
  it('adds the annotation, creating the map when absent', () => {
    expect(batchApplyPatch(true, ['a', 'b'])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.openshift.io~1batch-apply', value: 'a,b' },
    ]);
    expect(batchApplyPatch(false, ['a'])).toEqual([
      { op: 'add', path: '/metadata/annotations', value: { 'baselinesecurity.openshift.io/batch-apply': 'a' } },
    ]);
  });
  it('is a no-op for empty, blank, or invalid names', () => {
    expect(batchApplyPatch(true, [])).toEqual([]);
    expect(batchApplyPatch(true, ['', '  ', ','])).toEqual([]);
    expect(batchApplyPatch(false, ['Bad_Name', 'UPPER'])).toEqual([]);
  });
  it('dedupes, trims, and caps at the operator batch limit', () => {
    expect(batchApplyPatch(true, [' a ', 'b', 'a', 'b '])).toEqual([
      { op: 'add', path: '/metadata/annotations/baselinesecurity.openshift.io~1batch-apply', value: 'a,b' },
    ]);
    const many = Array.from({ length: 300 }, (_, i) => `rem-${i}`);
    const patch = batchApplyPatch(true, many);
    expect(patch).toHaveLength(1);
    const value = (patch[0] as { value: string }).value;
    expect(value.split(',')).toHaveLength(256);
    expect(value.startsWith('rem-0,')).toBeTruthy();
    expect(value.endsWith(',rem-255')).toBeTruthy();
  });
  // Free-form remediation names from multi-select / deep-links before annotation write.
  it('fuzz: never throws; empty or single add; value names are DNS-1123 and <=256', () => {
    for (let i = 0; i < 500; i++) {
      const names = Array.from({ length: i % 20 }, (_, j) =>
        j % 4 === 0
          ? `rem-${j}`
          : j % 4 === 1
            ? randomString(j % 30)
            : j % 4 === 2
              ? `  rem-${j}  `
              : '',
      );
      const patch = batchApplyPatch(i % 2 === 0, names);
      expect(Array.isArray(patch)).toBeTruthy();
      expect(patch.length).toBeLessThanOrEqual(1);
      if (patch.length === 0) continue;
      const value =
        typeof patch[0].value === 'string'
          ? patch[0].value
          : (patch[0].value as { 'baselinesecurity.openshift.io/batch-apply': string })[
              'baselinesecurity.openshift.io/batch-apply'
            ];
      const parts = value.split(',');
      expect(parts.length).toBeLessThanOrEqual(256);
      expect(new Set(parts).size).toBe(parts.length);
      for (const p of parts) {
        expect(isValidK8sName(p)).toBeTruthy();
      }
    }
  });
});

describe('batchApplyRequested', () => {
  const key = 'baselinesecurity.openshift.io/batch-apply';
  it('is false when missing, empty, whitespace, or comma-only', () => {
    expect(batchApplyRequested(undefined)).toBeFalsy();
    expect(batchApplyRequested(null)).toBeFalsy();
    expect(batchApplyRequested({})).toBeFalsy();
    expect(batchApplyRequested({ [key]: '' })).toBeFalsy();
    expect(batchApplyRequested({ [key]: '   ' })).toBeFalsy();
    expect(batchApplyRequested({ [key]: ',, , ' })).toBeFalsy();
  });
  it('is true when any non-empty remediation name token is present', () => {
    expect(batchApplyRequested({ [key]: 'a' })).toBeTruthy();
    expect(batchApplyRequested({ [key]: ' a , b ' })).toBeTruthy();
    expect(batchApplyRequested({ [key]: ',,rem-1,' })).toBeTruthy();
  });
  // Annotation value is CR-editable; must never throw and true only when a
  // non-empty comma token exists after trim (operator batchRemediationNames).
  it('fuzz: never throws; true iff a non-empty token exists', () => {
    for (let i = 0; i < 1000; i++) {
      const raw =
        i % 6 === 0
          ? undefined
          : i % 6 === 1
            ? null
            : i % 6 === 2
              ? {}
              : i % 6 === 3
                ? { [key]: '' }
                : i % 6 === 4
                  ? { [key]: ',, ,\t,' }
                  : { [key]: randomString(i % 64) + (i % 3 === 0 ? ',rem' : '') };
      let got: boolean;
      expect(() => {
        got = batchApplyRequested(raw as never);
      }).not.toThrow();
      expect(typeof got!).toBe('boolean');
      if (raw == null || typeof raw !== 'object') {
        expect(got!).toBeFalsy();
        continue;
      }
      const val = (raw as Record<string, unknown>)[key];
      if (typeof val !== 'string') {
        expect(got!).toBeFalsy();
        continue;
      }
      const hasToken = val.split(',').some((p) => p.trim());
      expect(got!).toBe(hasToken);
    }
  });
});
