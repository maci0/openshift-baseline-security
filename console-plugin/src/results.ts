// Result display helpers, CSV export, and scan-diff row builders.
import { ComplianceCheckResult, nodePoolFromScanName, SCAN_NAME_LABEL, Waiver } from './models';
import { checkSeverity } from './scoring';
import { resultFilterStatus } from './status';
import { activeWaivedNames } from './waivers';
import { checkResultHref } from './links';

// Localized severity label for Results UI and the printable report. Keep a single
// switch so chip titles and report cells cannot drift. Unknown / empty use the
// same "Unknown" source string; other values pass through for forward-compat.
export const severityDisplayTitle = (
  severity: string | undefined,
  t: (key: string) => string,
): string => {
  switch (severity) {
    case 'high':
      return t('High');
    case 'medium':
      return t('Medium');
    case 'low':
      return t('Low');
    case 'info':
      return t('Info');
    case 'unknown':
    case undefined:
    case '':
      return t('Unknown');
    default:
      return severity;
  }
};

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
// Use indexOf/slice instead of split so long descriptions are not fully tokenized
// on every Results row and CSV export (thousands of checks per multi-profile scan).
export const checkTitle = (r: ComplianceCheckResult): string => {
  // name is typed string but CRs are not runtime type-checked; always return a
  // non-empty string so Results rows / CSV cells never get undefined.
  const name =
    typeof r.metadata?.name === 'string' && r.metadata.name ? r.metadata.name : 'unknown';
  const d = r.description;
  // description is typed string but CRs are not runtime type-checked; a tampered
  // non-string value must fall back, not throw on .indexOf.
  if (typeof d !== 'string' || !d) {
    return name;
  }
  const i = d.indexOf('\n');
  const first = (i < 0 ? d : d.slice(0, i)).trim();
  return first || name;
};

export const checkBody = (r: ComplianceCheckResult): string => {
  const d = r.description;
  if (typeof d !== 'string' || !d) {
    return '';
  }
  const i = d.indexOf('\n');
  return i < 0 ? '' : d.slice(i + 1).trim();
};

// RFC 4180 CSV cell with spreadsheet-formula hardening. Values come from CR
// data, i.e. untrusted input. Drop NULs (can truncate cells in some tools).
// Prefix formula-looking cells with an apostrophe before quoting so spreadsheet
// apps import them as literal text. Also catch leading whitespace before a
// formula sigil (Excel often trims then evaluates).
// Fullwidth / Unicode sigils (＝＋－＠, U+2212 minus) and leading '|' (legacy
// Excel DDE) are treated the same as ASCII formula starters (CWE-1236).
// Module-level regexes so multi-thousand-row exports do not recompile patterns.
const csvNulRe = /\0/g;
const csvFormulaRe = /^\s*[=+\-@|\t\r\n\uFF1D\uFF0B\uFF0D\uFF20\u2212]/;
const csvQuoteRe = /[",\t\r\n]/;
const csvDoubleQuoteRe = /"/g;
const csvCell = (v: string): string => {
  // Coerce first: untrusted CR fields and resultFilterStatus may yield
  // non-string values (missing status, non-string name). Export must never throw.
  const cleaned = String(v ?? '').replace(csvNulRe, '');
  const safe = csvFormulaRe.test(cleaned) ? `'${cleaned}` : cleaned;
  return csvQuoteRe.test(safe) ? `"${safe.replace(csvDoubleQuoteRe, '""')}"` : safe;
};

// resultsCsv serializes check results to a CSV report. Deterministic column
// order; one header row. Status uses the same key as Results filters
// (FAIL+active-waiver => WAIVED; benign INCONSISTENT collapses). The waived
// column marks score exclusions so exports match Overview score math.
// Column names and status/severity enum values stay English so scripts keep a
// stable schema. UTF-8 BOM is prefixed so spreadsheets detect encoding.
// Pass a prebuilt active-waiver Set (from activeWaivedNames) to skip rebuilding
// it on multi-thousand-row exports when the Results table already has one.
export const resultsCsv = (
  results: ComplianceCheckResult[],
  waivers?: Waiver[] | ReadonlySet<string>,
): string => {
  // name carries the benchmark prefix (ocp4-cis-, ocp4-pci-dss-), so the profile
  // is already distinguishable without a separate column.
  // Pre-sized lines array: avoid map+join intermediates and push growth for
  // multi-thousand-row exports.
  const lines: string[] = new Array(results.length + 1);
  lines[0] = 'name,title,status,severity,waived';
  // Active waivers once: O(1) per row (multi-thousand CCRs; MaxItems=256 waivers).
  // Prebuilt Set (Results table) skips activeWaivedNames; Waiver[] builds once.
  const activeWaived =
    waivers == null || Array.isArray(waivers)
      ? activeWaivedNames(waivers)
      : waivers;
  for (let i = 0; i < results.length; i++) {
    const r = results[i];
    // Same key as Results filters/table (resultFilterStatus): FAIL+waiver => WAIVED.
    // Guard metadata: partial list items must not throw mid-export.
    const status = resultFilterStatus(
      {
        status: r.status,
        metadata: { name: r.metadata?.name ?? '', annotations: r.metadata?.annotations },
      },
      activeWaived,
    );
    lines[i + 1] =
      csvCell(String(r.metadata?.name ?? '')) +
      ',' +
      csvCell(checkTitle(r)) +
      ',' +
      csvCell(status) +
      ',' +
      csvCell(checkSeverity(r)) +
      ',' +
      csvCell(status === 'WAIVED' ? 'true' : 'false');
  }
  // UTF-8 BOM: Excel and similar tools need it to detect UTF-8 and avoid
  // mojibake for non-ASCII check titles. Column/enum tokens stay English for a
  // stable machine schema.
  return `\uFEFF${lines.join('\r\n')}`;
};

export interface ChangedCheck {
  name: string;
  title: string;
  href: string;
}

// Resolve status.newlyFailed / status.fixed check names into display items for
// the Overview "Recent changes" card: a human title (from the watched results,
// falling back to the raw name) and a deep-link to the ComplianceCheckResult.
//
// Index only the requested names (often a handful of regressions) instead of
// mapping every CCR. Early-exit when names is empty so Overview can call this
// for both newlyFailed and fixed without a full-list scan when either is empty.
export const changedChecks = (
  names: readonly string[] | undefined,
  results: ComplianceCheckResult[] | undefined,
): ChangedCheck[] => changedChecksMany([names], results)[0];

// Resolve several name lists with one CCR index pass (Overview newlyFailed +
// fixed). Empty lists short-circuit; when every list is empty no results scan.
export const changedChecksMany = (
  nameLists: readonly (readonly string[] | undefined)[],
  results: ComplianceCheckResult[] | undefined,
): ChangedCheck[][] => {
  const orderedLists = nameLists.map((names) => (names ?? []).filter(Boolean));
  const want = new Set<string>();
  for (const list of orderedLists) {
    for (const name of list) {
      want.add(name);
    }
  }
  if (want.size === 0) {
    return orderedLists.map(() => []);
  }
  const byName = new Map<string, ComplianceCheckResult>();
  for (const r of results ?? []) {
    const n = r.metadata?.name;
    if (n && want.has(n) && !byName.has(n)) {
      byName.set(n, r);
      if (byName.size === want.size) {
        break;
      }
    }
  }
  return orderedLists.map((list) =>
    list.map((name) => {
      const r = byName.get(name);
      return { name, title: r ? checkTitle(r) : name, href: checkResultHref(name) };
    }),
  );
};

// The MachineConfigPool a node scan targeted, parsed from the scan-name label
// ("<profile>-node-<pool>"), or null for a platform (non-node) check. Node scans
// run per-MCP, so this is the pool the per-node results below belong to.
export const nodeScanPool = (result: ComplianceCheckResult): string | null =>
  nodePoolFromScanName(result.metadata?.labels?.[SCAN_NAME_LABEL] ?? '');
