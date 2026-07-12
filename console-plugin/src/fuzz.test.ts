// Structural fuzz for the parsers that read ComplianceCheckResult CR fields.
// The k8s API is not runtime type-checked, so name/status/severity/description
// /annotations/labels are all untrusted: a tampered or partial CR must fall
// back, never throw and never corrupt a CSV export. The per-function unit tests
// pin known shapes; this feeds adversarial field values (wrong types, NULs,
// formula sigils, nested arrays, oversized/mixed-encoding strings) to pin the
// "must not throw" contract those helpers document.
//
// Scope note: the CR object itself is always an object (list elements from a
// k8s watch are never bare null); only its *fields* are fuzzed. Numeric status
// counts (ResultCounts) are operator-authored and out of scope here.
//
// Deterministic on purpose: a fixed-seed PRNG (no dependency, no wall clock) so
// a failure reproduces from the printed seed instead of flaking in CI.
import { effectiveStatus, inconsistentSources, resultFilterStatus } from './status';
import { checkSeverity } from './scoring';
import { checkBody, checkTitle, nodeScanPool, resultsCsv } from './results';
import {
  ComplianceCheckResult,
  nodePoolFromScanName,
  suiteProfileKey,
  suiteTailoredName,
} from './models';
import { isValidCron } from './cron';
import {
  dateInputEndOfDayIso,
  formatChartDate,
  formatCount,
  formatLocalDate,
  formatLocalDateTime,
  safeLocale,
} from './dates';

// mulberry32: 32-bit seeded PRNG, enough spread for structural fuzzing.
const rng = (seed: number) => (): number => {
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

// Nasty scalar leaves: types the CR schema forbids but the API can still deliver,
// plus strings that break CSV/annotation walking if a helper trusts them.
const LEAVES: unknown[] = [
  undefined,
  null,
  '',
  'PASS',
  'INCONSISTENT',
  'SKIP',
  'high',
  0,
  -1,
  NaN,
  Infinity,
  true,
  '=1+1',
  '@cmd',
  '+bad',
  '\tnode',
  'node:FAIL,other:PASS',
  'node: ',
  'a b',
  'x'.repeat(2000),
  'title\nrationale\nmore',
  'has\0nul',
  '＝＋−fullwidth',
  ',,,:::',
  '\r\n"quote",',
];

const leaf = (next: () => number): unknown => LEAVES[Math.floor(next() * LEAVES.length)];

const junk = (next: () => number, depth: number): unknown => {
  const r = next();
  if (depth > 3 || r < 0.7) return leaf(next);
  if (r < 0.85) return Array.from({ length: Math.floor(next() * 4) }, () => junk(next, depth + 1));
  const o: Record<string, unknown> = {};
  for (const k of ['name', 'annotations', 'labels', 'status', 'severity', 'description', 'x']) {
    if (next() < 0.5) o[k] = junk(next, depth + 1);
  }
  return o;
};

// A CR is always an object (real list elements never bare null); its fields are
// junk. metadata is usually present-but-malformed so the ?. guards get exercised.
const junkCR = (next: () => number): ComplianceCheckResult => {
  const o: Record<string, unknown> = {
    status: leaf(next),
    severity: leaf(next),
    description: leaf(next),
    metadata: next() < 0.8 ? junk(next, 2) : leaf(next),
  };
  return o as unknown as ComplianceCheckResult;
};

const ITER = 5000;

describe('CR field fuzz (untrusted CR input never throws)', () => {
  it('effectiveStatus / checkSeverity / checkTitle / checkBody return safe strings', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = rng(seed);
      const cr = junkCR(next);
      try {
        const st = effectiveStatus(cr as unknown as { status: string });
        expect(typeof st).toBe('string');
        expect(st.length).toBeGreaterThan(0);
        expect(typeof checkSeverity(cr)).toBe('string');
        expect(typeof checkTitle(cr)).toBe('string');
        expect(typeof checkBody(cr)).toBe('string');
      } catch (e) {
        throw new Error(`seed ${seed} threw: ${String(e)}`);
      }
    }
  });

  it('resultsCsv survives junk rows: BOM prefix, NUL stripped, always a string', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = rng(seed);
      const rows = Array.from({ length: Math.floor(next() * 5) }, () => junkCR(next));
      let out: string;
      try {
        out = resultsCsv(rows);
      } catch (e) {
        throw new Error(`seed ${seed} threw: ${String(e)}`);
      }
      expect(typeof out).toBe('string');
      // UTF-8 BOM so spreadsheets detect encoding.
      expect(out.startsWith('﻿')).toBe(true);
      // csvCell strips NUL (can truncate cells in some tools); a tampered CR
      // field carrying \0 must not survive into the export.
      expect(out.includes('\0')).toBe(false);
    }
  });

  // Filter / drill-down helpers also read untrusted annotations and labels.
  it('resultFilterStatus / inconsistentSources / suite parsers never throw', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = rng(seed);
      const cr = junkCR(next);
      // Mixed waiver shapes: array, Set, or absent (filter path branches).
      const waivers =
        next() < 0.3
          ? undefined
          : next() < 0.6
            ? [{ name: String(leaf(next) ?? ''), expiresAt: String(leaf(next) ?? '') }]
            : new Set([String(leaf(next) ?? ''), 'chk']);
      try {
        const st = resultFilterStatus(
          cr as unknown as {
            status: string;
            metadata?: { name?: string; annotations?: Record<string, string> };
          },
          waivers as never,
        );
        expect(typeof st).toBe('string');
        expect(st.length).toBeGreaterThan(0);
        const { sources, mostCommon } = inconsistentSources(cr);
        expect(Array.isArray(sources)).toBe(true);
        expect(mostCommon === null || typeof mostCommon === 'string').toBe(true);
        const pool = nodeScanPool(cr);
        expect(pool === null || typeof pool === 'string').toBe(true);
        const labels = cr.metadata?.labels as Record<string, string> | undefined;
        expect(() => suiteProfileKey(labels)).not.toThrow();
        expect(() => suiteTailoredName(labels)).not.toThrow();
        const scanLeaf = leaf(next);
        const scan = typeof scanLeaf === 'string' ? scanLeaf : String(scanLeaf ?? '');
        const fromScan = nodePoolFromScanName(scan);
        expect(fromScan === null || typeof fromScan === 'string').toBe(true);
      } catch (e) {
        throw new Error(`seed ${seed} threw: ${String(e)}`);
      }
    }
  });
});

// String-parser fuzz: isValidCron (spec.schedule from the schedule form) and the
// date/locale helpers all take untrusted strings and document a "must not throw"
// contract (isValidCron returns a boolean; the formatters fall back to raw/empty
// on unparseable input). Feed adversarial strings to pin that contract. Locale
// tags matter: Intl.* throws RangeError on a structurally invalid tag, so a bad
// document/i18n locale must be swallowed by safeLocale, not crash formatting.
const cronToken = (next: () => number): string => {
  const parts = ['*', '?', '/', '-', ',', '0', '59', '99', '7', 'jan', 'mon',
    String(Math.floor(next() * 1e9)), 'x'.repeat(Math.floor(next() * 40))];
  return parts[Math.floor(next() * parts.length)];
};
const junkString = (next: () => number): string => {
  const l = leaf(next);
  const base = typeof l === 'string' ? l : String(l ?? '');
  const n = Math.floor(next() * 6);
  let s = base;
  for (let i = 0; i < n; i++) s += cronToken(next) + (next() < 0.5 ? ' ' : '');
  return s;
};

describe('string-parser fuzz (untrusted schedule / date / locale never throws)', () => {
  it('isValidCron returns a boolean for any input', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = rng(seed);
      const s = junkString(next);
      try {
        expect(typeof isValidCron(s)).toBe('boolean');
      } catch (e) {
        throw new Error(`seed ${seed} (${JSON.stringify(s)}) threw: ${String(e)}`);
      }
    }
  });

  it('date/locale formatters never throw and keep their return contract', () => {
    for (let seed = 1; seed <= ITER; seed++) {
      const next = rng(seed);
      const iso = junkString(next);
      const locale = next() < 0.5 ? undefined : junkString(next);
      try {
        expect(typeof safeLocale(locale)).not.toBe('object'); // string | undefined
        expect(typeof formatLocalDate(iso, locale)).toBe('string');
        expect(typeof formatLocalDateTime(iso, locale)).toBe('string');
        expect(typeof formatChartDate(iso as unknown as number, locale)).toBe('string');
        expect(typeof formatCount(next() * 1e12 - 5e11, locale)).toBe('string');
        const eod = dateInputEndOfDayIso(iso);
        expect(eod === undefined || typeof eod === 'string').toBe(true);
      } catch (e) {
        throw new Error(
          `seed ${seed} (iso=${JSON.stringify(iso)}, locale=${JSON.stringify(locale)}) threw: ${String(e)}`,
        );
      }
    }
  });
});
