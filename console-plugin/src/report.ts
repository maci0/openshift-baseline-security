// Printable HTML compliance report builder (score, profiles, fails, waivers).
// Presentation-only: consumes domain helpers from models/results/scoring/status.
import {
  checkProfileLabel,
  ClusterBaseline,
  ComplianceCheckResult,
  isOwnedByBaseline,
  profileTitle,
} from './models';
import { checkTitle, severityDisplayTitle } from './results';
import { checkSeverity } from './scoring';
import { effectiveStatus } from './status';
import { formatCount, formatLocalDate, formatLocalDateTime, safeLocale } from './dates';
import { waiverExpired } from './waivers';

// HTML-escape untrusted text (waiver reasons, rule titles) for the report.
const htmlEscapes: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};
// Module-level regexes: multi-thousand FAIL-row exports must not recompile
// patterns on every esc() / default translate call.
const htmlEscapeRe = /[&<>"']/g;
const reportInterpRe = /\{\{(\w+)\}\}/g;
// Coerce first: CR fields typed as string are not runtime type-checked, so a
// tampered numeric/object/null value must not throw and abort report export.
const esc = (s: string): string =>
  String(s ?? '').replace(htmlEscapeRe, (c) => htmlEscapes[c]);

// Optional translator for report chrome. When omitted, English source keys are
// used with simple {{var}} interpolation so unit tests need no i18n harness.
export type ReportTranslate = (key: string, options?: Record<string, unknown>) => string;

const defaultReportTranslate: ReportTranslate = (key, options) => {
  if (!options) {
    return key;
  }
  // Prefer formatted* when present (locale-aware grouping), else the raw name.
  // English JSON values remap {{count}}→{{formattedCount}}; the no-i18n path
  // still interpolates the source key and must show the same digits.
  return key.replace(reportInterpRe, (_, name: string) => {
    const alias = `formatted${name.charAt(0).toUpperCase()}${name.slice(1)}`;
    if (options[alias] !== undefined) {
      return String(options[alias]);
    }
    if (options[name] !== undefined) {
      return String(options[name]);
    }
    return `{{${name}}}`;
  });
};

// Build a printable, self-contained HTML compliance report from already-watched
// data: overall score, per-profile breakdown, current failing checks, and active
// waivers with attribution. All untrusted text is HTML-escaped.
// Pass `translate` (e.g. i18next t) so report chrome follows the console locale.
export const buildReportHtml = (
  baseline: ClusterBaseline,
  results: ComplianceCheckResult[] = [],
  now: Date = new Date(),
  translate: ReportTranslate = defaultReportTranslate,
): string => {
  const t = translate;
  // Inherit console locale/dir so the report matches the operator's language and
  // RTL layout when opened from a translated console session. Dates and counts
  // use the same BCP 47 tag so formatting is not tied to the browser OS alone.
  const docEl =
    typeof document !== 'undefined' ? document.documentElement : undefined;
  // safeLocale normalizes underscore form and rejects invalid tags (toLocale*
  // throws RangeError). Fall back to "en" for the html lang attribute only;
  // formatting still uses undefined (runtime default) when the tag is bad.
  const locale = safeLocale(docEl?.lang || 'en');
  const htmlLang = locale ?? 'en';
  const htmlDir = docEl?.dir === 'rtl' ? 'rtl' : 'ltr';
  // Same non-finite guard and locale validation as Overview counts.
  const fmt = (n: number): string => formatCount(n, locale);
  const st = baseline.status ?? {};
  const score =
    st.score != null
      ? t('{{score}} / 100', { score: fmt(Number(st.score)) })
      : t('Not scanned');
  const profileRows = [
    ...(st.profiles ?? []).map((p) => ({ name: t(profileTitle(p.key ?? '')), c: p })),
    ...(st.tailoredProfiles ?? []).map((p) => ({
      name: t('{{name}} (tailored)', { name: p.name }),
      c: p,
    })),
  ]
    .map(
      ({ name, c }) =>
        // Coerce counts to numbers: the CR status is not runtime type-checked,
        // so a tampered non-numeric value cannot inject markup here.
        `<tr><td>${esc(name)}</td><td>${fmt(Number(c.pass) || 0)}</td><td>${fmt(Number(c.fail) || 0)}</td>` +
        `<td>${fmt(Number(c.manual) || 0)}</td><td>${fmt(Number(c.inconsistent) || 0)}</td><td>${fmt(Number(c.waived) || 0)}</td></tr>`,
    )
    .join('');
  const activeWaivers = (baseline.spec.waivers ?? []).filter(
    (w) => w.name && !waiverExpired(w, now),
  );
  // Same active set as score/CSV (activeWaivedNames); rebuild from the filtered
  // list so empty names cannot suppress a FAIL row via a corrupt waiver entry.
  const activeWaived = new Set(activeWaivers.map((w) => w.name));
  const profileSet = new Set(baseline.spec.profiles ?? []);
  const tailoredSet = new Set(baseline.spec.tailoredProfiles ?? []);
  // Single pass over results: no intermediate filtered array (export can hold
  // multi-thousand CCRs; only FAIL rows become HTML).
  const failingParts: string[] = [];
  for (const r of results) {
    // Optional-chain: partial/tampered CCR list items must not abort the report.
    const labels = r.metadata?.labels;
    const name = r.metadata?.name ?? '';
    if (
      !isOwnedByBaseline(labels, profileSet, tailoredSet) ||
      effectiveStatus(r) !== 'FAIL' ||
      activeWaived.has(name)
    ) {
      continue;
    }
    failingParts.push(
      `<tr><td>${esc(name)}</td><td>${esc(checkTitle(r))}</td>` +
        // checkProfileLabel returns i18n source titles for built-ins; t() leaves
        // tailored names and the empty em dash unchanged when no key exists.
        `<td>${esc(t(checkProfileLabel(labels)))}</td>` +
        `<td>${esc(severityDisplayTitle(checkSeverity(r), t))}</td></tr>`,
    );
  }
  const failingRows = failingParts.join('');
  const waiverRows = activeWaivers
    .map(
      (w) =>
        `<tr><td>${esc(w.name)}</td><td>${esc(w.reason ?? '')}</td>` +
        `<td>${esc(w.requestedBy ?? '')}</td><td>${esc(w.approvedBy ?? '')}</td>` +
        `<td>${w.expiresAt ? esc(formatLocalDate(w.expiresAt, locale)) : ''}</td>` +
        `<td>${w.reviewBy ? esc(formatLocalDate(w.reviewBy, locale)) : ''}</td></tr>`,
    )
    .join('');
  const emptyProfiles = `<tr><td colspan="6" class="muted">${esc(t('No profiles'))}</td></tr>`;
  const emptyFailing = `<tr><td colspan="4" class="muted">${esc(t('None'))}</td></tr>`;
  const emptyWaivers = `<tr><td colspan="6" class="muted">${esc(t('None'))}</td></tr>`;
  const whenText = now.toLocaleString(locale);
  const lastScanText = st.lastScanTime
    ? formatLocalDateTime(st.lastScanTime, locale)
    : t('n/a');
  const waiverCount = activeWaivers.length;
  // CSP: no scripts (report is static HTML). style-src unsafe-inline covers the
  // embedded chrome CSS only; all untrusted text is HTML-escaped above.
  // system-ui first so CJK/Arabic/Cyrillic glyphs resolve to platform fonts.
  return `<!doctype html><html lang="${esc(htmlLang)}" dir="${htmlDir}"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"><meta name="referrer" content="no-referrer"><title>${esc(t('Compliance report'))}</title>
<style>:root{color-scheme:light}body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans","Helvetica Neue",Arial,sans-serif;margin:2rem;color:#151515;background:#fff}h1{margin-bottom:0}
table{border-collapse:collapse;margin:1rem 0;width:100%}th,td{border:1px solid #ccc;padding:4px 8px;text-align:start}
.muted{color:#666}</style></head><body>
<h1>${esc(t('Compliance report'))}</h1>
<p class="muted">${esc(t('Generated {{when}} • last scan {{lastScan}}', {
    when: whenText,
    lastScan: lastScanText,
  }))}</p>
<h2>${esc(t('Score: {{score}}', { score }))}</h2>
<h3>${esc(t('Profiles'))}</h3>
<table><thead><tr><th>${esc(t('Profile'))}</th><th>${esc(t('Pass'))}</th><th>${esc(t('Fail'))}</th><th>${esc(t('Manual'))}</th><th>${esc(t('Inconsistent'))}</th><th>${esc(t('Waived'))}</th></tr></thead>
<tbody>${profileRows || emptyProfiles}</tbody></table>
<h3>${esc(t('Failing checks'))}</h3>
<table><thead><tr><th>${esc(t('Check'))}</th><th>${esc(t('Title'))}</th><th>${esc(t('Profile'))}</th><th>${esc(t('Severity'))}</th></tr></thead>
<tbody>${failingRows || emptyFailing}</tbody></table>
<h3>${esc(
    t('Active waivers ({{count}})', {
      count: waiverCount,
      formattedCount: fmt(waiverCount),
    }),
  )}</h3>
<table><thead><tr><th>${esc(t('Check'))}</th><th>${esc(t('Reason'))}</th><th>${esc(t('Requested by'))}</th><th>${esc(t('Approved by'))}</th><th>${esc(t('Expires'))}</th><th>${esc(t('Review by'))}</th></tr></thead>
<tbody>${waiverRows || emptyWaivers}</tbody></table>
</body></html>`;
};
