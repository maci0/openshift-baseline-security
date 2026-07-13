import { resultsCsv, severityDisplayTitle } from './results';
import { ComplianceCheckResult } from './models';

// severityDisplayTitle is shared by Results chips and the printable report so
// filter labels cannot drift from export cells. Map known severities through t();
// unknown/empty become "Unknown"; forward-compat values pass through.
describe('severityDisplayTitle', () => {
  const t = (key: string) => `i18n:${key}`;

  it.each([
    ['high', 'i18n:High'],
    ['medium', 'i18n:Medium'],
    ['low', 'i18n:Low'],
    ['info', 'i18n:Info'],
    ['unknown', 'i18n:Unknown'],
    ['', 'i18n:Unknown'],
    [undefined, 'i18n:Unknown'],
  ] as const)('severity %p -> %s', (sev, want) => {
    expect(severityDisplayTitle(sev, t)).toBe(want);
  });

  it('passes through forward-compat severities without i18n wrapping', () => {
    expect(severityDisplayTitle('critical', t)).toBe('critical');
    expect(severityDisplayTitle('HIGH', t)).toBe('HIGH'); // case-sensitive product contract
  });
});

// resultsCsv serializes ComplianceCheckResult CRs (untrusted: names, titles, and
// severity come straight from in-cluster objects) into a CSV a user downloads
// and opens in a spreadsheet. Two failure modes matter: CSV *injection* (a cell
// starting with =/+/-/@ evaluated as a formula, CWE-1236) and *structure breaks*
// (an embedded comma/quote/newline splitting one logical row into several, or
// desyncing columns). csvCell hardens both; nothing pins it. This is a fuzz
// sweep: inject formula sigils, delimiters, quotes, newlines, NULs, and Unicode
// look-alikes, then re-parse the output with a real RFC 4180 reader and assert
// the row/column shape survived and no cell stayed formula-dangerous.

const HOSTILE = [
  '=cmd|/c calc',
  '+1+1',
  '-2+3',
  '@SUM(A1)',
  '|dde',
  '\t=danger',
  '  =leadingspace', // Excel trims then evaluates
  '＝fullwidth',
  '＋fullwidthplus',
  '−unicodeminus',
  'a,b,c', // delimiter
  'line1\r\nline2', // CRLF row break
  'line1\nline2', // LF row break
  'has"quote',
  '"balanced"',
  'trailing"',
  '\0nul\0byte',
  'normal title',
  '',
  ' ',
  '值', // non-ASCII (BOM/UTF-8 path)
  'x'.repeat(2000),
];

// Deterministic PRNG (mulberry32) so failures reproduce; no Math.random in CI.
const rng = (seed: number) => () => {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (rand: () => number): string => HOSTILE[Math.floor(rand() * HOSTILE.length)];

const hostileResults = (rand: () => number): ComplianceCheckResult[] =>
  Array.from({ length: Math.floor(rand() * 8) }, () => ({
    metadata: {
      name: pick(rand),
      namespace: 'openshift-compliance',
      annotations: { 'baselinesecurity.openshift.io/waived': pick(rand) },
    },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    status: pick(rand) as any,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    severity: pick(rand) as any,
    description: pick(rand),
  }));

// Minimal RFC 4180 reader: honours quoted fields, escaped ("") quotes, and
// newlines embedded inside quotes so a legitimately-quoted cell is not miscounted
// as a new row. Enough to validate structure, not a general CSV library.
const parseCsv = (text: string): string[][] => {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let inQuotes = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inQuotes) {
      if (c === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') {
        inQuotes = false;
      } else {
        cell += c;
      }
    } else if (c === '"') {
      inQuotes = true;
    } else if (c === ',') {
      row.push(cell);
      cell = '';
    } else if (c === '\r' || c === '\n') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      row.push(cell);
      cell = '';
      rows.push(row);
      row = [];
    } else {
      cell += c;
    }
  }
  row.push(cell);
  rows.push(row);
  return rows;
};

// The exact hardening predicate from results.ts: a neutralized cell must NOT
// still start (after optional whitespace) with a formula/DDE sigil.
const formulaRe = /^\s*[=+\-@|\t\r\n＝＋－＠−]/;

describe('resultsCsv fuzz sweep', () => {
  it('keeps CSV structure and neutralizes formulas under hostile input', () => {
    for (let seed = 0; seed < 400; seed++) {
      const rand = rng(seed);
      const results = hostileResults(rand);

      let csv = '';
      expect(() => {
        csv = resultsCsv(results);
      }).not.toThrow();

      // UTF-8 BOM so spreadsheets detect encoding.
      expect(csv[0]).toBe('﻿');
      const rows = parseCsv(csv.slice(1));

      // Exactly one header + one row per result: no embedded newline split a row.
      expect(rows.length).toBe(results.length + 1);
      expect(rows[0]).toEqual(['name', 'title', 'status', 'severity', 'waived']);

      for (let r = 1; r < rows.length; r++) {
        expect(rows[r].length).toBe(5); // no stray comma desynced columns
        for (const cell of rows[r]) {
          // No cell may still look like a formula: hardening prepends "'".
          expect(formulaRe.test(cell)).toBe(false);
          expect(cell).not.toContain('\0'); // NULs stripped
        }
      }
    }
  });

  it('handles the empty result set', () => {
    const csv = resultsCsv([]);
    expect(csv).toBe('﻿name,title,status,severity,waived');
  });
});
