import { resultsCsv, severityDisplayTitle, checkTitle, checkBody, changedChecksMany, nodeScanPool } from './results';
import { machineConfigPoolHref } from './links';
import { ComplianceCheckResult } from './models';

const result = (name: string, description?: string): ComplianceCheckResult =>
  ({ metadata: { name, namespace: 'ns' }, description }) as ComplianceCheckResult;

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

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
describe('checkTitle', () => {
  it('uses the first line of the description', () => {
    expect(checkTitle(result('x', 'Title line\nRationale text'))).toBe('Title line');
  });
  it('trims whitespace', () => {
    expect(checkTitle(result('x', '  Title  \nrest'))).toBe('Title');
  });
  it('falls back to the name when description is missing or blank', () => {
    expect(checkTitle(result('fallback'))).toBe('fallback');
    expect(checkTitle(result('fallback', ''))).toBe('fallback');
    expect(checkTitle(result('fallback', '\n\n'))).toBe('fallback');
  });
  // Missing/non-string metadata.name must still yield a non-empty title so
  // Results rows and CSV cells never render undefined.
  it('falls back to unknown when name is missing or non-string', () => {
    expect(
      checkTitle({ metadata: { name: '', namespace: 'ns' } } as ComplianceCheckResult),
    ).toBe('unknown');
    expect(
      checkTitle({
        metadata: { name: 42 as unknown as string, namespace: 'ns' },
      } as ComplianceCheckResult),
    ).toBe('unknown');
    expect(
      checkTitle({
        metadata: {} as ComplianceCheckResult['metadata'],
        description: 'Title only',
      } as ComplianceCheckResult),
    ).toBe('Title only');
  });
  it('fuzz: never throws and never returns empty', () => {
    for (let i = 0; i < 2000; i++) {
      const title = checkTitle(result('name', randomString(i % 64)));
      expect(typeof title).toBe('string');
      expect(title.length).toBeGreaterThan(0);
    }
  });
  // CRs are not runtime type-checked: a tampered non-string description must
  // fall back to the name, not throw on .indexOf/.trim.
  it('fuzz: tolerates non-string (tampered CR) descriptions', () => {
    for (const bad of [0, 42, true, {}, [], null, NaN] as unknown[]) {
      const r = { metadata: { name: 'nm' }, description: bad } as unknown as ComplianceCheckResult;
      expect(checkTitle(r)).toBe('nm');
      expect(checkBody(r)).toBe('');
    }
  });
});

describe('checkBody', () => {
  it('returns everything after the first line, trimmed', () => {
    expect(checkBody(result('x', 'Title\nBody line 1\nBody line 2'))).toBe(
      'Body line 1\nBody line 2',
    );
  });
  it('returns empty for single-line or missing descriptions', () => {
    expect(checkBody(result('x', 'Title only'))).toBe('');
    expect(checkBody(result('x'))).toBe('');
  });
  it('fuzz: never throws', () => {
    for (let i = 0; i < 2000; i++) {
      expect(typeof checkBody(result('n', randomString(i % 64)))).toBe('string');
    }
  });
});

describe('resultsCsv', () => {
  const r = (name: string, status: string, severity: string, description?: string) =>
    ({ metadata: { name, namespace: 'ns' }, status, severity, description }) as ComplianceCheckResult;
  // Strip the UTF-8 BOM so line assertions stay readable.
  const csvLines = (csv: string): string[] => csv.replace(/^\uFEFF/, '').split('\r\n');

  it('emits a header and one row per result', () => {
    const csv = resultsCsv([r('a', 'PASS', 'low', 'Title A'), r('b', 'FAIL', 'high', 'Title B')]);
    expect(csv.startsWith('\uFEFF')).toBe(true);
    const lines = csvLines(csv);
    expect(lines[0]).toBe('name,title,status,severity,waived');
    expect(lines[1]).toBe('a,Title A,PASS,low,false');
    expect(lines[2]).toBe('b,Title B,FAIL,high,false');
  });
  it('marks waived checks so export matches score exclusions', () => {
    // Status is WAIVED (same as Results filter/table), waived column true.
    const csv = resultsCsv([r('b', 'FAIL', 'high', 'Fail B')], [{ name: 'b', reason: 'risk' }]);
    expect(csvLines(csv)[1]).toBe('b,Fail B,WAIVED,high,true');
  });
  it('does not mark a waived PASS as score-excluded (self-healing)', () => {
    // Operator only excludes FAIL+waiver; a waived check that now PASSes
    // still counts toward the score, so the CSV must not claim waived=true.
    const csv = resultsCsv([r('b', 'PASS', 'high', 'Pass B')], [{ name: 'b', reason: 'stale' }]);
    expect(csvLines(csv)[1]).toBe('b,Pass B,PASS,high,false');
  });
  // Expired waivers re-enter the score denominator; CSV waived=false must match.
  // Far-past expiresAt so wall-clock CI drift cannot flip the column.
  it('does not mark expired waivers as score-excluded', () => {
    const csv = resultsCsv(
      [r('b', 'FAIL', 'high', 'Fail B')],
      [{ name: 'b', reason: 'risk', expiresAt: '2000-01-01T00:00:00Z' }],
    );
    expect(csvLines(csv)[1]).toBe('b,Fail B,FAIL,high,false');
  });
  it('quotes and escapes cells containing comma, quote, or newline', () => {
    const csv = resultsCsv([r('x,y', 'FAIL', 'high', 'He said "hi"\nline2')]);
    const row = csvLines(csv)[1];
    expect(row).toBe('"x,y","He said ""hi""",FAIL,high,false');
  });
  it('neutralizes spreadsheet formula-looking cells from untrusted CR data', () => {
    const csv = resultsCsv([
      r('=cmd', '-1', '@import', '+SUM(1,1)'),
      r('\tTabbed', '\nNewline', 'low'),
      r('\rCarriage', 'PASS', 'low'),
      r(' =cmd', 'PASS', 'low'), // leading space then formula
      r('a\0b', 'PASS', 'low'), // NUL stripped (can truncate cells)
      r('\uFF1Dcmd', 'PASS', 'low'), // fullwidth equals
      r('|DDE', 'PASS', 'low'), // legacy Excel DDE
    ]);
    const lines = csvLines(csv);
    // status "-1" is an unknown CO status: effectiveStatus folds it to ERROR (the
    // operator tally's default bucket), so the status cell is a fixed enum, not a
    // passed-through formula. name/title/severity still exercise formula-escaping.
    expect(lines[1]).toBe(`'=cmd,"'+SUM(1,1)",ERROR,'@import,false`);
    // status "\nNewline" is unknown too -> ERROR; the escaped name/title cols still
    // exercise tab/newline quoting.
    expect(csv).toContain(`"'\tTabbed","'\tTabbed",ERROR,low,false`);
    expect(csv).toContain(`"'\rCarriage","'\rCarriage",PASS,low,false`);
    expect(csv).toContain(`' =cmd`);
    expect(csv).toContain('ab,ab,PASS,low,false');
    expect(csv).toContain(`'\uFF1Dcmd`);
    expect(csv).toContain(`'|DDE`);
    expect(csv).not.toContain('\0');
  });
  it('handles empty input (header only)', () => {
    expect(resultsCsv([])).toBe('\uFEFFname,title,status,severity,waived');
  });
  // Export must match the Results table: benign INCONSISTENT collapses so CSV
  // status is not a raw "INCONSISTENT" that fails filters and score math.
  it('collapses benign INCONSISTENT via resultFilterStatus', () => {
    const inconsistent = {
      metadata: {
        name: 'inc',
        namespace: 'ns',
        annotations: {
          'compliance.openshift.io/inconsistent-source': 'node0:PASS',
          'compliance.openshift.io/most-common-status': 'NOT-APPLICABLE',
        },
      },
      status: 'INCONSISTENT',
      severity: 'medium',
      description: 'Benign split',
    } as ComplianceCheckResult;
    const csv = resultsCsv([inconsistent]);
    expect(csvLines(csv)[1]).toBe('inc,Benign split,PASS,medium,false');
  });
  // Operator folds SKIP into notApplicable; CSV must match Overview N/A export.
  it('folds SKIP into NOT-APPLICABLE via resultFilterStatus', () => {
    const csv = resultsCsv([r('s1', 'SKIP', 'low', 'Skipped rule')]);
    expect(csvLines(csv)[1]).toBe('s1,Skipped rule,NOT-APPLICABLE,low,false');
  });
  // Missing/non-string status must not throw (csvCell used to call .replace on
  // undefined) and must export ERROR so the row matches operator ResultCounts.
  it('tolerates missing status and exports ERROR (operator tally parity)', () => {
    const missing = {
      metadata: { name: 'orphan', namespace: 'ns' },
      severity: 'low',
      description: 'No status field',
    } as unknown as ComplianceCheckResult;
    expect(() => resultsCsv([missing])).not.toThrow();
    expect(csvLines(resultsCsv([missing]))[1]).toBe(
      'orphan,No status field,ERROR,low,false',
    );
  });
  // Partial list items (no metadata) must not abort CSV export.
  it('tolerates missing metadata on a result row', () => {
    const bare = { status: 'PASS', severity: 'low' } as unknown as ComplianceCheckResult;
    expect(() => resultsCsv([bare])).not.toThrow();
    expect(csvLines(resultsCsv([bare]))[1]).toBe(',unknown,PASS,low,false');
  });
  it('fuzz: valid CSV (quotes balanced) for arbitrary CR text', () => {
    const rand = () =>
      Array.from({ length: Math.floor(fuzzRand() * 40) }, () =>
        String.fromCharCode(Math.floor(fuzzRand() * 128)),
      ).join('');
    // Formula-looking prefixes (CWE-1236) that must be neutralized with a leading '.
    const formulaSeeds = ['=cmd', '+SUM(1)', '-1', '@import', '|DDE', '\tTab', '\rCR', ' =eq', '\uFF1Dfull'];
    const formulaRe = /^\s*[=+\-@|\t\r\n\uFF1D\uFF0B\uFF0D\uFF20\u2212]/;
    for (let i = 0; i < 2000; i++) {
      const name = i < formulaSeeds.length ? formulaSeeds[i] : rand();
      const title = i % 3 === 0 ? formulaSeeds[i % formulaSeeds.length] : rand();
      const csv = resultsCsv([r(name, 'FAIL', 'high', title)]);
      expect(typeof csv).toBe('string');
      expect(csv.startsWith('\uFEFF')).toBe(true);
      // Total double-quotes are even (all escapes balanced).
      expect((csv.match(/"/g) ?? []).length % 2).toBe(0);
      // Five columns: name,title,status,severity,waived (header + one data row).
      // Quoted cells may legitimately contain escaped CRLF (RFC 4180), so rows
      // split on CRLF outside quotes, not on a raw split('\r\n').
      const rows: string[] = [''];
      let inQ = false;
      const body = csv.replace(/^\uFEFF/, '');
      for (let j = 0; j < body.length; j++) {
        const ch = body[j];
        if (ch === '"') {
          inQ = !inQ;
          rows[rows.length - 1] += ch;
        } else if (!inQ && ch === '\r' && body[j + 1] === '\n') {
          rows.push('');
          j++;
        } else {
          rows[rows.length - 1] += ch;
        }
      }
      expect(rows).toHaveLength(2);
      // RFC 4180: split on commas outside quotes.
      const cols: string[] = [];
      let cell = '';
      inQ = false;
      for (let j = 0; j < rows[1].length; j++) {
        const ch = rows[1][j];
        if (ch === '"') {
          inQ = !inQ;
          cell += ch;
        } else if (ch === ',' && !inQ) {
          cols.push(cell);
          cell = '';
        } else {
          cell += ch;
        }
      }
      cols.push(cell);
      expect(cols).toHaveLength(5);
      // No NULs survive export (can truncate cells in spreadsheet tools).
      expect(csv).not.toContain('\0');
      // Unquote RFC 4180 cells (outer quotes + doubled inner quotes).
      const unquote = (c: string): string => {
        if (c.length >= 2 && c.startsWith('"') && c.endsWith('"')) {
          return c.slice(1, -1).replace(/""/g, '"');
        }
        return c;
      };
      // Formula-looking name / rendered title must be apostrophe-prefixed in the row.
      const renderedTitle = checkTitle(r(name, 'FAIL', 'high', title));
      const nameClean = String(name ?? '').replace(/\0/g, '');
      const titleClean = String(renderedTitle ?? '').replace(/\0/g, '');
      if (formulaRe.test(nameClean) && nameClean.length > 0) {
        expect(unquote(cols[0]).startsWith("'")).toBe(true);
      }
      if (formulaRe.test(titleClean) && titleClean.length > 0) {
        expect(unquote(cols[1]).startsWith("'")).toBe(true);
      }
    }
  });
});

describe('nodeScanPool', () => {
  const withScan = (scan?: string): ComplianceCheckResult =>
    ({
      metadata: { name: 'r', namespace: 'ns', labels: scan ? { 'compliance.openshift.io/scan-name': scan } : {} },
      status: 'INCONSISTENT',
    }) as ComplianceCheckResult;

  it('extracts the MachineConfigPool from a node scan name', () => {
    expect(nodeScanPool(withScan('ocp4-cis-node-worker'))).toBe('worker');
    expect(nodeScanPool(withScan('ocp4-cis-node-master'))).toBe('master');
    expect(nodeScanPool(withScan('ocp4-pci-dss-node-infra'))).toBe('infra');
    expect(nodeScanPool(withScan('custom-node-profile-node-worker'))).toBe('worker');
  });
  it('returns null for a platform (non-node) scan or missing label', () => {
    expect(nodeScanPool(withScan('ocp4-cis'))).toBeNull();
    expect(nodeScanPool(withScan())).toBeNull();
    expect(nodeScanPool(withScan('ocp4-cis-node-'))).toBeNull();
  });
  it('fuzz: never throws for arbitrary scan names', () => {
    for (let i = 0; i < 500; i++) {
      const out = nodeScanPool(withScan(randomString(i % 30)));
      expect(out === null || typeof out === 'string').toBe(true);
    }
  });
  it('machineConfigPoolHref builds an encoded MCP console path', () => {
    expect(machineConfigPoolHref('worker')).toBe(
      '/k8s/cluster/machineconfiguration.openshift.io~v1~MachineConfigPool/worker',
    );
    expect(machineConfigPoolHref('a b')).toContain(encodeURIComponent('a b'));
  });
});

describe('changedChecksMany', () => {
  const res = (name: string, description?: string) =>
    ({ metadata: { name, namespace: 'openshift-compliance' }, description }) as ComplianceCheckResult;

  it('resolves names to title + deep-link, name as title fallback', () => {
    const results = [res('ocp4-cis-a', 'Audit profile set\nrationale')];
    const [items] = changedChecksMany([['ocp4-cis-a', 'ocp4-cis-missing']], results);
    expect(items).toHaveLength(2);
    expect(items[0]).toEqual({
      name: 'ocp4-cis-a',
      title: 'Audit profile set',
      href: expect.stringContaining('ocp4-cis-a'),
    });
    // Unknown name falls back to the raw name as its title.
    expect(items[1].title).toBe('ocp4-cis-missing');
  });
  it('filters empty names and tolerates undefined inputs', () => {
    expect(changedChecksMany([undefined], undefined)).toEqual([[]]);
    const [items] = changedChecksMany([['', 'x']], []);
    expect(items).toHaveLength(1);
  });
  it('drops non-string elements from untrusted status lists without throwing', () => {
    // status.newlyFailed/fixed are not runtime type-checked; a corrupt element
    // (number/object/bool/array) must be dropped, not reach checkResultHref and
    // crash the Overview "Recent changes" render.
    const hostile = [42, {}, true, ['x'], null, undefined, 'real'] as unknown as string[];
    expect(() => changedChecksMany([hostile], [])).not.toThrow();
    const [items] = changedChecksMany([hostile], []);
    expect(items).toHaveLength(1);
    expect(items[0].name).toBe('real');
    expect(() => changedChecksMany([hostile, hostile], [])).not.toThrow();
  });
  // Regression / newlyFailed names and CCR descriptions are untrusted cluster text.
  it('fuzz: never throws; drops empty names; every item has name/title/href', () => {
    for (let i = 0; i < 500; i++) {
      const names = Array.from({ length: i % 8 }, (_, j) =>
        j % 3 === 0 ? '' : randomString(j % 24),
      );
      const results = names
        .filter(Boolean)
        .slice(0, 3)
        .map((name) => res(name, randomString(i % 40)));
      const [items] = changedChecksMany([names], results);
      expect(items.length).toBe(names.filter(Boolean).length);
      for (const x of items) {
        expect(x.name.length).toBeGreaterThan(0);
        expect(typeof x.title).toBe('string');
        expect(x.href).toContain(
          '/k8s/ns/openshift-compliance/compliance.openshift.io~v1alpha1~ComplianceCheckResult/',
        );
      }
    }
  });
  it('changedChecksMany indexes multiple lists in one pass', () => {
    const results = [
      res('a', 'Title A'),
      res('b', 'Title B'),
      res('c', 'Title C'),
    ];
    const [nf, fx] = changedChecksMany(
      [
        ['a', 'missing'],
        ['c', ''],
      ],
      results,
    );
    expect(nf.map((x) => x.title)).toEqual(['Title A', 'missing']);
    expect(fx.map((x) => x.title)).toEqual(['Title C']);
    expect(changedChecksMany([undefined, []], results)).toEqual([[], []]);
  });
});
