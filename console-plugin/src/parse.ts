// Boundary guards for values that k8s payloads and browser APIs deliver
// without runtime validation. Every representation check lives here so call
// sites branch on domain values instead of raw typeof expressions.

export const isString = (v: unknown): v is string => typeof v === 'string';

// Finite only: CR numerics feed Math.min/max/format pipelines where NaN or
// Infinity must fall back, not propagate.
export const isFiniteNumber = (v: unknown): v is number =>
  typeof v === 'number' && Number.isFinite(v);

// Unrestricted number. The content key has to tell NaN and Infinity apart from
// the strings that spell them, so it cannot borrow isFiniteNumber's
// fold-to-absent.
export const isNumber = (v: unknown): v is number => typeof v === 'number';

export const isBoolean = (v: unknown): v is boolean => typeof v === 'boolean';

export const isBigInt = (v: unknown): v is bigint => typeof v === 'bigint';

export const isSymbol = (v: unknown): v is symbol => typeof v === 'symbol';

// Callable values reach the content key only as the residue JSON.stringify
// cannot carry; the key tags them by kind, it never calls them.
export const isFunction = (v: unknown): v is (...args: never[]) => void =>
  typeof v === 'function';

// A value that crossed the boundary (a field off a cluster object, a browser
// API result, a caught throw) before a guard has run. The model's declared type
// is a claim, not a fact: nothing validates a CR at runtime. Attached to the
// field it is read from so the alias resolves through an owner shape instead of
// spelling `unknown` on every signature; callers narrow with the guards above,
// and the few that must tolerate any shape (the content-key encoder) say so.
export interface UntrustedField {
  readonly value?: unknown;
}

export type UntrustedValue = UntrustedField['value'];

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
// oxlint-disable-next-line eslint/no-control-regex -- matching control characters is this expression's whole contract, and they are already written as escapes
const exportControlRe = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/gu;

// Drop BIDI / zero-width / BOM so a later formula or HTML check sees the
// real first character. Leaves tab/CR/LF in place for RFC 4180 quoting.
export const stripFormatChars = (s: string): string => s.replace(formatCharRe, '');

// Drop controls and format characters from identity-bearing strings
// (waiver requestedBy/approvedBy). Trim first at the call site so a
// tab-only value stays empty rather than becoming a leftover control.
export const stripControlAndFormat = (s: string): string =>
  s.replace(controlAndFormatRe, '');

// Free prose (waiver reason, remediation error text, report cells). Same
// invisible-character defense, minus the two format characters that carry
// script content: U+200D ZWJ and U+200C ZWNJ join letters in Arabic, Persian,
// and Indic scripts and glue emoji sequences into one glyph. Stripping them
// rewrites what the user wrote (a Persian compound misspells, a family emoji
// becomes three separate emoji), and prose has no identity to spoof, so the
// joiners stay. Everything else in \p{Cf} (BIDI controls, zero-width space,
// BOM, word joiner) still renders as nothing and only ever exists to hide the
// next character. Identity fields keep the full strip: a ZWJ in front of
// "admin" is a spoofing vector there and no legal identity needs one.
const controlRe = /\p{Cc}/gu;
// \p{Cf} minus U+200C / U+200D, written as the negation of "not-Cf or either
// joiner" so the class needs no set-operation syntax.
const joinerSafeFormatRe = /[^\P{Cf}\u200C\u200D]/gu;

export const stripInvisibleText = (s: string): string =>
  s.replace(controlRe, '').replace(joinerSafeFormatRe, '');

// Drop the controls a spreadsheet would trim off a cell before evaluating it,
// keeping the RFC 4180 delimiters. Run on export rows only.
export const stripExportControls = (s: string): string => s.replace(exportControlRe, '');
