// Printable HTML compliance report builder (score, profiles, fails, waivers).
// Presentation-only: consumes domain helpers from models/results/scoring/status.
import { now as instant } from './clock';
import {
  checkProfileLabel,
  ClusterBaseline,
  ComplianceCheckResult,
  isOwnedByBaseline,
  profileTitle,
} from './models';
import { checkTitle, severityDisplayTitle } from './results';
import { aggregateCounts, checkSeverity, normalizeScore, scoreStatus } from './scoring';
import { effectiveStatus } from './status';
import {
  formatCount,
  formatLocalDate,
  formatLocalDateTime,
  safeLocale,
  textDirection,
} from './dates';
import { isFiniteNumber, stripFormatChars } from './parse';
import { waiverExpired } from './waivers';

// HTML-escape untrusted text (waiver reasons, rule titles) for the report.
const htmlEscapes = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
} satisfies Record<string, string>;
// Module-level regexes: multi-thousand FAIL-row exports must not recompile
// patterns on every esc() / default translate call.
const htmlEscapeRe = /[&<>"']/g;
const reportInterpRe = /\{\{(\w+)\}\}/g;
// Coerce first: CR fields typed as string are not runtime type-checked, so a
// tampered numeric/object/null value must not throw and abort report export.
const esc = (s: string): string =>
  // SAFETY: htmlEscapeRe matches only the five characters keyed in htmlEscapes,
  // so the lookup below cannot miss. Format characters (BIDI, zero-width) are
  // stripped first so untrusted CR text cannot reverse or hide neighboring cells.
  stripFormatChars(String(s ?? '')).replace(htmlEscapeRe, (c) => htmlEscapes[c as keyof typeof htmlEscapes]);

// Isolate untrusted CR text (check titles, waiver reasons, names) so a
// bidirectional override cannot reverse surrounding punctuation or column
// labels. dir=auto also puts ellipsis on the correct side for RTL titles.
const autoDir = (s: string): string => `<span dir="auto">${esc(s)}</span>`;

// One ResultCounts field rendered as a report cell. The CR status is not
// runtime type-checked: a non-numeric or non-finite value folds to 0 (so it can
// neither inject markup nor print an empty cell for NaN / Infinity), and a
// negative folds to 0 exactly as the operator's clampResultCounts does on write,
// so the printed table never disagrees with the status the operator published.
// All eight categories, same order as the on-screen per-profile card rows.
const countCell = (n: number | undefined): number => {
  if (!isFiniteNumber(n) || n < 0) {
    return 0;
  }
  return n;
};

// Interpolation variables for report chrome. Keys are open-ended ({{count}},
// {{formattedCount}}, {{name}}, ...) but values must render as text, so the
// contract is string-or-number; anything else is a caller bug.
export interface ReportVars {
  [key: string]: string | number | undefined;
}

// Optional translator for report chrome. When omitted, English source keys are
// used with simple {{var}} interpolation so unit tests need no i18n harness.
export type ReportTranslate = (key: string, options?: ReportVars) => string;

// Printable report palette and type scale, in one place. The report is a
// standalone document (CSP style-src 'unsafe-inline' only, no external CSS), so
// PatternFly 6 light-theme values are inlined rather than inherited. Naming
// them here is what keeps the report editable as a set: a literal pasted into
// the CSS below is a value that can drift into a second role with nothing to
// notice the split.
const REPORT_TOKENS = {
  // Neutrals, PatternFly 6 light theme. textSubtle and every text status value
  // sit at or above the 4.5:1 text ratio the console's own status tokens are
  // calibrated for (see scoreColor in src/scoring.ts).
  text: '#151515',
  textSubtle: '#4d4d4d',
  border: '#c7c7c7',
  surface: '#fff',
  surfaceSubtle: '#f2f2f2',
  // Text bands for a 0-100 score, same 60/90 thresholds as the console.
  statusDanger: '#b1380b',
  statusWarning: '#795600',
  statusSuccess: '#1e4f18',
  // The accent rule is decorative: it carries no text and nothing depends on
  // it, so it takes the PatternFly icon/status tokens rather than the 4.5:1
  // text ones. Same three hexes the Grafana dashboard thresholds already use
  // (operator/internal/controller/assets/compliance-dashboard.json), so the
  // report and the dashboards a cluster admin reads read as one product. The
  // rule follows the score instead of being a fixed brand color: a red frame on
  // a passing report contradicts the number printed under it, and the report is
  // read by people deciding whether a cluster passed.
  accentDanger: '#c9190b',
  accentWarning: '#f0ab00',
  accentSuccess: '#3e8635',
  // A report with no computable score is unscored, not failing. Its frame is
  // the neutral, the same value that renders "—" in the score line.
  accentNone: '#8a8d90',
  // Type scale, in rem off the 16px root. Each level is a named step, not a
  // default: the score sits a clear step above the page title because it is
  // the one number the report exists to communicate, and section titles get a
  // rule above them so the three tables read as three groups.
  textSm: '0.8125rem',
  textBase: '0.875rem',
  heading: '1.5rem',
  subheading: '1.125rem',
  score: '2.25rem',
  // Measure. The waiver table has six columns of free text; full-bleed on a
  // wide monitor stretches a reason across a thousand pixels. 72rem keeps the
  // longest table at a readable column width and centers the rest.
  measure: '72rem',
} as const;

// system-ui first so CJK/Arabic/Cyrillic glyphs resolve to platform fonts.
const REPORT_CSS =
  ':root{color-scheme:light}' +
  // The band class on <body> sets the custom property the body rule below
  // draws with. Every band is named explicitly, so a missing case is a
  // colorless rule rather than a silent wrong color.
  '.accent-danger{--report-accent:' +
  REPORT_TOKENS.accentDanger +
  '}' +
  '.accent-warning{--report-accent:' +
  REPORT_TOKENS.accentWarning +
  '}' +
  '.accent-success{--report-accent:' +
  REPORT_TOKENS.accentSuccess +
  '}' +
  '.accent-none{--report-accent:' +
  REPORT_TOKENS.accentNone +
  '}' +
  'body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans","Helvetica Neue",Arial,sans-serif;font-size:' +
  REPORT_TOKENS.textBase +
  ';line-height:1.5;max-width:' +
  REPORT_TOKENS.measure +
  ';margin:2rem auto;padding:0 1.5rem;color:' +
  REPORT_TOKENS.text +
  ';background:' +
  REPORT_TOKENS.surface +
  ';border-block-start:4px solid var(--report-accent);padding-block-start:1.5rem}' +
  'h1{font-size:' +
  REPORT_TOKENS.heading +
  ';font-weight:700;line-height:1.3;margin:0}' +
  'h2{font-size:' +
  REPORT_TOKENS.subheading +
  ';font-weight:700;line-height:1.3;border-block-start:1px solid ' +
  REPORT_TOKENS.border +
  ';padding-block-start:1rem;margin:2rem 0 0.5rem}' +
  '.muted{color:' +
  REPORT_TOKENS.textSubtle +
  ';font-size:' +
  REPORT_TOKENS.textSm +
  ';margin:0.25rem 0 0}' +
  '.score{font-size:' +
  REPORT_TOKENS.score +
  ';font-weight:700;line-height:1.2;font-variant-numeric:tabular-nums;margin:0.75rem 0 0}' +
  '.score-danger{color:' +
  REPORT_TOKENS.statusDanger +
  '}' +
  '.score-warning{color:' +
  REPORT_TOKENS.statusWarning +
  '}' +
  '.score-success{color:' +
  REPORT_TOKENS.statusSuccess +
  '}' +
  '.score-none{color:' +
  REPORT_TOKENS.textSubtle +
  ';font-size:' +
  REPORT_TOKENS.subheading +
  '}' +
  'table{border-collapse:collapse;margin:0.5rem 0 0;width:100%}' +
  'th,td{border-block-end:1px solid ' +
  REPORT_TOKENS.border +
  ';padding:0.5rem 0.75rem;text-align:start;overflow-wrap:anywhere;unicode-bidi:isolate}' +
  'th{background:' +
  REPORT_TOKENS.surfaceSubtle +
  ';font-weight:700}' +
  '.sev-high{color:' +
  REPORT_TOKENS.statusDanger +
  ';font-weight:700}' +
  '.sev-medium{color:' +
  REPORT_TOKENS.statusWarning +
  '}' +
  '@media print{body{margin:1cm auto;padding:0;border-block-start-width:2px}}';

// Known-only class: never interpolate untrusted CCR severity into markup.
const severityClass = (severity: string): string =>
  severity === 'high' ? ' class="sev-high"' : severity === 'medium' ? ' class="sev-medium"' : '';

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
  now: Date = instant(),
  translate: ReportTranslate = defaultReportTranslate,
  localeTag?: string,
): string => {
  const t = translate;
  // Inherit console locale/dir so the report matches the operator's language and
  // RTL layout when opened from a translated console session. Dates and counts
  // use the same BCP 47 tag so formatting is not tied to the browser OS alone.
  // Prefer the caller's i18n language (console session) over document.lang,
  // which can be missing or a different granularity (en vs en-US).
  // globalThis (not a typeof probe) so SSR-free console rendering still works
  // when the plugin bundle is evaluated outside a document (early boot).
  const docEl = globalThis.document?.documentElement;
  // safeLocale normalizes underscore form and rejects invalid tags (toLocale*
  // throws RangeError). Fall back to "en" for the html lang attribute only;
  // formatting still uses undefined (runtime default) when the tag is bad.
  const locale = safeLocale(localeTag || docEl?.lang || 'en');
  const htmlLang = locale ?? 'en';
  // Explicit document.dir wins (the console already mirrored the page); otherwise
  // derive RTL from the locale so an Arabic session still gets a RTL report when
  // document.dir is unset (tests, some embeddings).
  const htmlDir =
    docEl?.dir === 'rtl' ? 'rtl' : docEl?.dir === 'ltr' ? 'ltr' : textDirection(locale);
  // Same non-finite guard and locale validation as Overview counts.
  const fmt = (n: number): string => formatCount(n, locale);
  const maxText = fmt(100);
  const st = baseline.status ?? {};
  // Aggregate all eight status categories across built-in + tailored profiles,
  // the same set the on-screen composition donut and per-profile cards show.
  const totals = aggregateCounts(...(st.profiles ?? []), ...(st.tailoredProfiles ?? []));
  const totalChecks =
    totals.pass +
    totals.fail +
    totals.manual +
    totals.info +
    totals.error +
    totals.inconsistent +
    totals.waived +
    totals.notApplicable;
  // Match the donut: with zero evaluated checks a non-null status.score is stale
  // (0/0) and the UI shows "—", so the report must not print a number over it.
  // normalizeScore folds a non-numeric / non-finite / out-of-range status.score
  // (untrusted CR field) to no score, so a tampered value cannot print an empty
  // red "Score:" line where the console shows "Not scanned".
  const numericScore = normalizeScore(st.score);
  const score = totalChecks > 0 && numericScore !== null
    ? t('{{score}} / {{max}}', {
        score: fmt(numericScore),
        max: 100,
        formattedMax: maxText,
      })
    : t('Not scanned');
  // Same 60/90 bands as Overview. Unscored reports stay muted, not danger.
  // scoreStatus returns a closed union, so interpolating it yields a known
  // class name, never a value read off a cluster object.
  const band = totalChecks > 0 && numericScore !== null ? scoreStatus(numericScore) : null;
  const scoreClass = band ? `score score-${band}` : 'score score-none';
  const frameClass = band ? `accent-${band}` : 'accent-none';
  const profileRows = [
    ...(st.profiles ?? []).map((p) => ({ name: t(profileTitle(p.key ?? '')), c: p })),
    ...(st.tailoredProfiles ?? []).map((p) => ({
      name: t('{{name}} (tailored)', { name: p.name }),
      c: p,
    })),
  ]
    .map(
      ({ name, c }) =>
        `<tr><td>${autoDir(name)}</td><td>${fmt(countCell(c.pass))}</td><td>${fmt(countCell(c.fail))}</td>` +
        `<td>${fmt(countCell(c.manual))}</td><td>${fmt(countCell(c.info))}</td>` +
        `<td>${fmt(countCell(c.inconsistent))}</td><td>${fmt(countCell(c.error))}</td>` +
        `<td>${fmt(countCell(c.waived))}</td><td>${fmt(countCell(c.notApplicable))}</td></tr>`,
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
    const severity = checkSeverity(r);
    failingParts.push(
      `<tr><td>${autoDir(name)}</td><td>${autoDir(checkTitle(r))}</td>` +
        // checkProfileLabel returns i18n source titles for built-ins; t() leaves
        // tailored names and the empty em dash unchanged when no key exists.
        `<td>${autoDir(t(checkProfileLabel(labels)))}</td>` +
        `<td${severityClass(severity)}>${esc(severityDisplayTitle(severity, t))}</td></tr>`,
    );
  }
  const failingRows = failingParts.join('');
  const waiverRows = activeWaivers
    .map(
      (w) =>
        `<tr><td>${autoDir(w.name)}</td><td>${autoDir(w.reason ?? '')}</td>` +
        `<td>${autoDir(w.requestedBy ?? '')}</td><td>${autoDir(w.approvedBy ?? '')}</td>` +
        `<td>${w.expiresAt ? esc(formatLocalDate(w.expiresAt, locale)) : ''}</td>` +
        `<td>${w.reviewBy ? esc(formatLocalDate(w.reviewBy, locale)) : ''}</td></tr>`,
    )
    .join('');
  const emptyProfiles = `<tr><td colspan="9" class="muted">${esc(t('No profiles'))}</td></tr>`;
  const emptyFailing = `<tr><td colspan="4" class="muted">${esc(t('None'))}</td></tr>`;
  const emptyWaivers = `<tr><td colspan="6" class="muted">${esc(t('None'))}</td></tr>`;
  const whenText = now.toLocaleString(locale);
  const lastScanText = st.lastScanTime
    ? formatLocalDateTime(st.lastScanTime, locale)
    : t('n/a');
  const waiverCount = activeWaivers.length;
  // CSP: no scripts (report is static HTML). style-src unsafe-inline covers the
  // embedded chrome CSS only; all untrusted text is HTML-escaped above.
  return `<!doctype html><html lang="${esc(htmlLang)}" dir="${htmlDir}"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"><meta name="referrer" content="no-referrer"><title>${esc(t('Compliance report'))}</title>
<style>${REPORT_CSS}</style></head><body class="${frameClass}">
<h1>${esc(t('Compliance report'))}</h1>
<p class="muted">${esc(t('Generated {{when}} • last scan {{lastScan}}', {
    when: whenText,
    lastScan: lastScanText,
  }))}</p>
<p class="${scoreClass}">${esc(t('Score: {{score}}', { score }))}</p>
<h2>${esc(t('Profiles'))}</h2>
<table><thead><tr><th>${esc(t('Profile'))}</th><th>${esc(t('Pass'))}</th><th>${esc(t('Fail'))}</th><th>${esc(t('Manual'))}</th><th>${esc(t('Info'))}</th><th>${esc(t('Inconsistent'))}</th><th>${esc(t('Error'))}</th><th>${esc(t('Waived'))}</th><th>${esc(t('Not applicable'))}</th></tr></thead>
<tbody>${profileRows || emptyProfiles}</tbody></table>
<h2>${esc(t('Failing checks'))}</h2>
<table><thead><tr><th>${esc(t('Check'))}</th><th>${esc(t('Title'))}</th><th>${esc(t('Profile'))}</th><th>${esc(t('Severity'))}</th></tr></thead>
<tbody>${failingRows || emptyFailing}</tbody></table>
<h2>${esc(
    t('Active waivers ({{count}})', {
      count: waiverCount,
      formattedCount: fmt(waiverCount),
    }),
  )}</h2>
<table><thead><tr><th>${esc(t('Check'))}</th><th>${esc(t('Reason'))}</th><th>${esc(t('Requested by'))}</th><th>${esc(t('Approved by'))}</th><th>${esc(t('Expires'))}</th><th>${esc(t('Review by'))}</th></tr></thead>
<tbody>${waiverRows || emptyWaivers}</tbody></table>
</body></html>`;
};
