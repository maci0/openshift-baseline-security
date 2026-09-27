// Boundary guards for values that k8s payloads and browser APIs deliver
// without runtime validation. Every representation check lives here so call
// sites branch on domain values instead of raw typeof expressions.

export const isString = (v: unknown): v is string => typeof v === 'string';

// Finite only: CR numerics feed Math.min/max/format pipelines where NaN or
// Infinity must fall back, not propagate.
export const isFiniteNumber = (v: unknown): v is number =>
  typeof v === 'number' && Number.isFinite(v);

// Unicode format characters (BIDI overrides, zero-width, BOM, word joiner).
// Untrusted CR text uses these to hide a CSV formula sigil, spoof an audit
// name, or reverse a filename extension. Module-level: CSV/report export
// walks thousands of cells.
const formatCharRe = /\p{Cf}/gu;
// C0/C1 controls plus format. Identity fields must not keep either; CSV
// cells keep tab/CR/LF (quoted) so this is not used on export rows.
const controlAndFormatRe = /[\p{Cc}\p{Cf}]/gu;
// Controls on an export row, minus the three RFC 4180 delimiters. A
// spreadsheet trims leading control characters off a cell before deciding
// whether it is a formula, so "\u0001=cmd" is evaluated even though the sigil
// is not the first character. Tab/CR/LF must survive for quoting, so they are
// spelled out rather than handled by \s.
const exportControlRe = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/gu;

// Drop BIDI / zero-width / BOM so a later formula or HTML check sees the
// real first character. Leaves tab/CR/LF in place for RFC 4180 quoting.
export const stripFormatChars = (s: string): string => s.replace(formatCharRe, '');

// Drop controls and format characters from identity-bearing strings
// (waiver requestedBy/approvedBy/reason). Trim first at the call site so a
// tab-only value stays empty rather than becoming a leftover control.
export const stripControlAndFormat = (s: string): string =>
  s.replace(controlAndFormatRe, '');

// Drop the controls a spreadsheet would trim off a cell before evaluating it,
// keeping the RFC 4180 delimiters. Run on export rows only.
export const stripExportControls = (s: string): string => s.replace(exportControlRe, '');
