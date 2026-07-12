import { ComplianceCheckResult, Waiver } from './models';
import { effectiveStatus } from './status';
import { isWaived } from './waivers';
import { checkResultHref } from './links';

// The description's first line is the rule title; the rest is the rationale.
// description comes from ComplianceCheckResult CRs, i.e. untrusted input.
// Use indexOf/slice instead of split so long descriptions are not fully tokenized
// on every Results row and CSV export (thousands of checks per multi-profile scan).
export const checkTitle = (r: ComplianceCheckResult): string => {
  const d = r.description;
  if (!d) {
    return r.metadata.name;
  }
  const i = d.indexOf('\n');
  const first = (i < 0 ? d : d.slice(0, i)).trim();
  return first || r.metadata.name;
};

export const checkBody = (r: ComplianceCheckResult): string => {
  const d = r.description;
  if (!d) {
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
const csvCell = (v: string): string => {
  const cleaned = v.replace(/\0/g, '');
  const safe = /^\s*[=+\-@\t\r\n]/.test(cleaned) ? `'${cleaned}` : cleaned;
  return /[",\t\r\n]/.test(safe) ? `"${safe.replace(/"/g, '""')}"` : safe;
};

// resultsCsv serializes check results to a CSV report. Deterministic column
// order; one header row. When waivers are provided, a waived column marks
// checks excluded from the score (FAIL + waiver only; a waived PASS still
// counts toward the score) so exports match Overview score math.
export const resultsCsv = (
  results: ComplianceCheckResult[],
  waivers?: Waiver[],
): string => {
  // name carries the benchmark prefix (ocp4-cis-, ocp4-pci-dss-), so the profile
  // is already distinguishable without a separate column.
  const header = ['name', 'title', 'status', 'severity', 'waived'];
  const rows = results.map((r) => {
    // Effective status once per row: export matches the table (benign
    // INCONSISTENT collapses) and waived is derived from the same value.
    const status = effectiveStatus(r);
    return [
      r.metadata.name,
      checkTitle(r),
      status,
      r.severity,
      status === 'FAIL' && isWaived(r.metadata.name, waivers) ? 'true' : 'false',
    ]
      .map((c) => csvCell(String(c ?? '')))
      .join(',');
  });
  return [header.join(','), ...rows].join('\r\n');
};

export interface ChangedCheck {
  name: string;
  title: string;
  href: string;
}

// Resolve status.newlyFailed / status.fixed check names into display items for
// the Overview "Recent changes" card: a human title (from the watched results,
// falling back to the raw name) and a deep-link to the ComplianceCheckResult.
export const changedChecks = (
  names: string[] | undefined,
  results: ComplianceCheckResult[] | undefined,
): ChangedCheck[] => {
  const byName = new Map((results ?? []).map((r) => [r.metadata.name, r]));
  return (names ?? [])
    .filter(Boolean)
    .map((name) => {
      const r = byName.get(name);
      return { name, title: r ? checkTitle(r) : name, href: checkResultHref(name) };
    });
};

// The MachineConfigPool a node scan targeted, parsed from the scan-name label
// ("<profile>-node-<pool>"), or null for a platform (non-node) check. Node scans
// run per-MCP, so this is the pool the per-node results below belong to.
export const nodeScanPool = (result: ComplianceCheckResult): string | null => {
  const scan = result.metadata?.labels?.['compliance.openshift.io/scan-name'] ?? '';
  const i = scan.lastIndexOf('-node-');
  return i < 0 ? null : scan.slice(i + '-node-'.length) || null;
};
