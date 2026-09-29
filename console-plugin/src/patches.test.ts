import { isValidCron } from './cron';
import { isValidK8sName } from './names';
import { isString } from './parse';
import { addWaiverPatch, batchApplyPatch, batchApplyRequested, remediationApplyPatch, removeWaiverPatch, rescanPatch, rescanToken, resourceVersionTest, schedulePatch, tailoredProfileBindingPatch, type PatchOp } from './patches';
import { randomString } from './testing/fuzz';
import { trimGoSpace } from './text';

// Minimal RFC 6902 applier covering the op set the builders in patches.ts emit
// (add / replace / remove / test, object members, whole arrays, and the "-"
// array append). It exists so a test can assert what a SECOND execution does
// to a document instead of asserting only the op list: the whole-point failure
// mode is a patch that looks right and silently replaces a list.
type Doc = { [key: string]: unknown };

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

const readPointer = (doc: Doc, path: string): unknown => {
  let cur: unknown = doc;
  for (const raw of path.split('/').slice(1)) {
    const key = raw.replace(/~1/g, '/').replace(/~0/g, '~');
    if (cur == null || typeof cur !== 'object') {
      throw new Error(`path ${path} does not resolve`);
    }
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
};

const writePointer = (doc: Doc, path: string, value: unknown): void => {
  const keys = path.split('/').slice(1).map((k) => k.replace(/~1/g, '/').replace(/~0/g, '~'));
  const last = keys.pop();
  if (last == null) throw new Error('empty pointer');
  let cur: unknown = doc;
  for (const key of keys) {
    if (cur == null || typeof cur !== 'object') {
      throw new Error(`path ${path} does not resolve`);
    }
    cur = (cur as Record<string, unknown>)[key];
  }
  if (cur == null || typeof cur !== 'object') {
    throw new Error(`path ${path} does not resolve`);
  }
  (cur as Record<string, unknown>)[last] = value;
};

const removePointer = (doc: Doc, path: string): void => {
  const keys = path.split('/').slice(1).map((k) => k.replace(/~1/g, '/').replace(/~0/g, '~'));
  const last = keys.pop();
  if (last == null) throw new Error('empty pointer');
  let cur: unknown = doc;
  for (const key of keys) {
    if (cur == null || typeof cur !== 'object') {
      throw new Error(`path ${path} does not resolve`);
    }
    cur = (cur as Record<string, unknown>)[key];
  }
  if (Array.isArray(cur) && last !== '-') {
    cur.splice(Number(last), 1);
  } else if (cur != null && typeof cur === 'object') {
    delete (cur as Record<string, unknown>)[last];
  }
};

// applyOps returns the patched document, or throws the way the apiserver rejects
// a failed test op. Applied to a fresh clone, so the caller keeps the original
// and can replay the same ops against it.
const applyOps = (doc: Doc, ops: PatchOp[]): Doc => {
  const next = clone(doc);
  for (const op of ops) {
    switch (op.op) {
      case 'test':
        if (JSON.stringify(readPointer(next, op.path)) !== JSON.stringify(op.value)) {
          throw new Error(`test failed at ${op.path}`);
        }
        break;
      case 'add':
        if (op.path.endsWith('/-')) {
          const arr = readPointer(next, op.path.slice(0, -2));
          if (!Array.isArray(arr)) throw new Error(`${op.path} parent is not an array`);
          arr.push(op.value);
        } else {
          writePointer(next, op.path, op.value);
        }
        break;
      case 'replace':
        writePointer(next, op.path, op.value);
        break;
      case 'remove':
        removePointer(next, op.path);
        break;
    }
  }
  return next;
};

const newDoc = (resourceVersion: string): Doc => ({
  metadata: { resourceVersion, annotations: undefined },
  spec: {},
});

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
        const v: unknown = patch[0].value;
        // SAFETY: remediationApplyPatch emits either the bare enum string (leaf
        // add at /spec/remediation/apply) or an object holding it under `apply`.
        const wrapped = v as { apply?: unknown };
        const apply = isString(v) ? v : wrapped.apply;
        expect(['Automatic', 'Manual']).toContain(apply);
      }
    }
  });
});

describe('rescanToken', () => {
  it('differs between two clicks in the same millisecond', () => {
    expect(rescanToken(1000, 1)).not.toBe(rescanToken(1000, 2));
  });

  it('differs across a page reload, where the sequence restarts at 1', () => {
    // The regression: a session counter alone restarted at 1 after a reload, so
    // the first rescan of the new session rewrote the value the apiserver
    // already held. CO watches for a change, so no scan started and the UI
    // still reported success.
    const previousSession = rescanToken(1_000_000, 1);
    const afterReload = rescanToken(2_000_000, 1);
    expect(afterReload).not.toBe(previousSession);
  });

  it('produces a non-empty token rescanPatch accepts', () => {
    expect(rescanPatch(true, rescanToken(1_000_000, 1))).toHaveLength(1);
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
      const guard = expectGuard
        ? [{ op: 'test', path: '/metadata/resourceVersion', value: rv }]
        : [];
      const add = hasAnnotations
        ? [
            {
              op: 'add',
              path: '/metadata/annotations/compliance.openshift.io~1rescan',
              value: token,
            },
          ]
        : [
            {
              op: 'add',
              path: '/metadata/annotations',
              value: { 'compliance.openshift.io/rescan': token },
            },
          ];
      expect(p).toEqual([...guard, ...add]);
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

  // Restated here, not imported from cron.ts, so the field count is checked against
  // the operator's rule rather than against the validator under test.
  // oxlint-disable-next-line eslint/no-control-regex -- the operator's separator set is defined by the control characters in it (tab, LF, VT, FF, CR), spelled as escapes
  const operatorSpaceRe = /[\t\n\u000b\f\r\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/;

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
      let ok: boolean | undefined;
      expect(() => {
        ok = isValidCron(s);
      }).not.toThrow();
      expect(ok).toBeDefined();
      // Split on the operator's set (Go unicode.IsSpace, as used by strings.Fields
      // in schedule.go), not \s: a value that is five fields to the operator is
      // what must round-trip, and \s admits U+FEFF, which the operator does not
      // split on.
      let bad: string | undefined;
      const count = trimGoSpace(s).split(operatorSpaceRe).length;
      if (ok && count !== 5) {
        bad = `isValidCron accepted ${JSON.stringify(s)} (${count} fields)`;
      }
      expect(bad).toBeUndefined();
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
  it('resourceVersion-guards both shapes, so a replay cannot re-run the batch', () => {
    const guard = { op: 'test', path: '/metadata/resourceVersion', value: '7' };
    expect(batchApplyPatch(true, ['a'], '7')[0]).toEqual(guard);
    expect(batchApplyPatch(false, ['a'], '7')[0]).toEqual(guard);
    // No known resourceVersion (no guard rather than a false conflict).
    expect(batchApplyPatch(true, ['a'])).toHaveLength(1);
    expect(batchApplyPatch(false, ['a'])).toHaveLength(1);
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
    // SAFETY: with accepted names batchApplyPatch emits exactly one op whose
    // value is the joined comma-separated annotation string.
    const value = patch[0].value as string;
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
      const v: unknown = patch[0].value;
      // SAFETY: the single emitted op carries either the joined name string
      // (nested add) or the whole annotations map holding that string.
      const map = v as { 'baselinesecurity.openshift.io/batch-apply': string };
      const value = isString(v) ? v : map['baselinesecurity.openshift.io/batch-apply'];
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
    const isAnnotationMap = (v: unknown): v is Record<string, string> =>
      v != null && typeof v === 'object';
    for (let i = 0; i < 1000; i++) {
      const raw: Record<string, string> | null | undefined =
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
      let got: boolean | undefined;
      expect(() => {
        got = batchApplyRequested(raw);
      }).not.toThrow();
      expect(got).toBeDefined();
      let bad: string | undefined;
      if (!isAnnotationMap(raw)) {
        if (got) {
          bad = `batchApplyRequested truthy for ${JSON.stringify(raw)}`;
        }
      } else {
        const val = raw[key];
        const hasToken = isString(val) && val.split(',').some((p) => p.trim());
        if (got !== hasToken) {
          bad = `batchApplyRequested=${got}, want ${hasToken} for ${JSON.stringify(raw)}`;
        }
      }
      expect(bad).toBeUndefined();
    }
  });
});

// What a second execution does to the object, not just to the op list. Every
// builder here emits a resourceVersion test, so a replay against a document
// that has already moved on is rejected instead of overwriting the first
// write's result.
describe('repeated execution', () => {
  const RV = '100';

  it('a replayed waiver create is rejected and does not drop the first waiver', () => {
    // First submit reads an absent list, so it creates it. A second submit
    // built from the same (now stale) snapshot reaches the same branch.
    const first = newDoc(RV);
    const afterFirst = applyOps(first, addWaiverPatch(undefined, { name: 'chk-a' }, RV));
    expect((afterFirst.spec as { waivers: { name: string }[] }).waivers).toEqual([
      { name: 'chk-a' },
    ]);
    // The apiserver bumped resourceVersion on the first write.
    const afterWrite: Doc = { ...afterFirst, metadata: { resourceVersion: '101' } };
    const replay = addWaiverPatch(undefined, { name: 'chk-b' }, RV);
    expect(() => applyOps(afterWrite, replay)).toThrow('test failed');
    // The list the first submit created is intact: a plain `add` on /spec/waivers
    // would have replaced it with the single-entry replay.
    expect((afterWrite.spec as { waivers: { name: string }[] }).waivers).toEqual([
      { name: 'chk-a' },
    ]);
  });

  it('a replayed waiver append is rejected, and a re-read re-runs as an update', () => {
    const base: Doc = {
      metadata: { resourceVersion: RV },
      spec: { waivers: [{ name: 'chk-a' }] },
    };
    const append = addWaiverPatch([{ name: 'chk-a' }], { name: 'chk-b' }, RV);
    const once = applyOps(base, append);
    expect((once.spec as { waivers: { name: string }[] }).waivers).toEqual([
      { name: 'chk-a' },
      { name: 'chk-b' },
    ]);
    // Replaying the same patch: the list test no longer matches, so no second row.
    expect(() => applyOps(once, append)).toThrow('test failed');
    expect((once.spec as { waivers: { name: string }[] }).waivers).toHaveLength(2);
    // Re-running with a fresh read is an update of the same entry, not an append.
    const reread = addWaiverPatch(
      (once.spec as { waivers: { name: string }[] }).waivers,
      { name: 'chk-a', reason: 'risk' },
      '102',
    );
    const twice = applyOps(
      { ...once, metadata: { resourceVersion: '102' } },
      reread,
    );
    expect((twice.spec as { waivers: { name: string }[] }).waivers).toEqual([
      { name: 'chk-a', reason: 'risk' },
      { name: 'chk-b' },
    ]);
  });

  it('a replayed waiver remove is rejected rather than removing a shifted index', () => {
    const base: Doc = {
      metadata: { resourceVersion: RV },
      spec: { waivers: [{ name: 'chk-a' }, { name: 'chk-b' }] },
    };
    const remove = removeWaiverPatch(0, 'chk-a', RV);
    const once = applyOps(base, remove);
    expect((once.spec as { waivers: { name: string }[] }).waivers).toEqual([
      { name: 'chk-b' },
    ]);
    // Index 0 now holds chk-b, so even the name test refuses a stale replay.
    expect(() => applyOps(once, remove)).toThrow('test failed');
    expect((once.spec as { waivers: { name: string }[] }).waivers).toEqual([
      { name: 'chk-b' },
    ]);
  });

  it('a replayed batch-apply request is rejected on both annotation shapes', () => {
    for (const hasAnnotations of [true, false]) {
      const base: Doc = hasAnnotations
        ? {
            metadata: {
              resourceVersion: RV,
              annotations: { 'kubectl.kubernetes.io/last-applied-configuration': '{}' },
            },
            spec: {},
          }
        : newDoc(RV);
      const request = batchApplyPatch(hasAnnotations, ['rem-a'], RV);
      const once = applyOps(base, request);
      const anns = (once.metadata as { annotations: Record<string, string> }).annotations;
      expect(anns['baselinesecurity.openshift.io/batch-apply']).toBe('rem-a');
      // The annotation is a one-shot the operator consumes and clears: a replay
      // landing after it ran would pause the pools and re-apply the batch.
      const moved: Doc = { ...once, metadata: { ...once.metadata, resourceVersion: '101' } };
      expect(() => applyOps(moved, request)).toThrow('test failed');
    }
  });

  it('a replayed batch-apply does not drop a sibling annotation written since', () => {
    const base: Doc = {
      metadata: { resourceVersion: '101', annotations: { other: 'keep' } },
      spec: {},
    };
    // The stale view had no annotations map at all, so this build takes the
    // whole-map branch, which replaces the map the sibling now lives in.
    const request = batchApplyPatch(false, ['rem-a'], RV);
    const anns = (base.metadata as { annotations: Record<string, string> }).annotations;
    expect(() => applyOps(base, request)).toThrow('test failed');
    expect(anns['other']).toBe('keep');
  });
});
