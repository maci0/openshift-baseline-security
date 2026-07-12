import {
  checkProfileLabel,
  ClusterBaseline,
  ComplianceCheckResult,
  isOwnedByBaseline,
} from './models';
import { checkTitle } from './results';
import { effectiveStatus } from './status';
import { isWaived, waiverExpired } from './waivers';

// HTML-escape untrusted text (waiver reasons, rule titles) for the report.
const htmlEscapes: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};
const esc = (s: string): string => s.replace(/[&<>"']/g, (c) => htmlEscapes[c]);

// Optional translator for report chrome. When omitted, English source keys are
// used with simple {{var}} interpolation so unit tests need no i18n harness.
export type ReportTranslate = (key: string, options?: Record<string, unknown>) => string;

const defaultReportTranslate: ReportTranslate = (key, options) => {
  if (!options) {
    return key;
  }
  return key.replace(/\{\{(\w+)\}\}/g, (_, name: string) =>
    options[name] !== undefined ? String(options[name]) : `{{${name}}}`,
  );
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
  const st = baseline.status ?? {};
  const score =
    st.score != null ? t('{{score}} / 100', { score: st.score }) : t('Not scanned');
  const profileRows = [
    ...(st.profiles ?? []).map((p) => ({ name: (p.key ?? '').toUpperCase(), c: p })),
    ...(st.tailoredProfiles ?? []).map((p) => ({
      name: t('{{name}} (tailored)', { name: p.name }),
      c: p,
    })),
  ]
    .map(
      ({ name, c }) =>
        `<tr><td>${esc(name)}</td><td>${c.pass ?? 0}</td><td>${c.fail ?? 0}</td>` +
        `<td>${c.manual ?? 0}</td><td>${c.inconsistent ?? 0}</td><td>${c.waived ?? 0}</td></tr>`,
    )
    .join('');
  const activeWaivers = (baseline.spec.waivers ?? []).filter((w) => !waiverExpired(w, now));
  const failingRows = results
    .filter(
      (r) =>
        isOwnedByBaseline(
          r.metadata.labels,
          baseline.spec.profiles,
          baseline.spec.tailoredProfiles,
        ) &&
        effectiveStatus(r) === 'FAIL' &&
        !isWaived(r.metadata.name, baseline.spec.waivers, now),
    )
    .map(
      (r) =>
        `<tr><td>${esc(r.metadata.name)}</td><td>${esc(checkTitle(r))}</td>` +
        `<td>${esc(checkProfileLabel(r.metadata.labels))}</td><td>${esc(r.severity ?? 'unknown')}</td></tr>`,
    )
    .join('');
  const waiverRows = activeWaivers
    .map(
      (w) =>
        `<tr><td>${esc(w.name)}</td><td>${esc(w.reason ?? '')}</td>` +
        `<td>${esc(w.requestedBy ?? '')}</td><td>${esc(w.approvedBy ?? '')}</td>` +
        `<td>${w.expiresAt ? esc(new Date(w.expiresAt).toLocaleDateString()) : ''}</td>` +
        `<td>${w.reviewBy ? esc(new Date(w.reviewBy).toLocaleDateString()) : ''}</td></tr>`,
    )
    .join('');
  const emptyProfiles = `<tr><td colspan="6" class="muted">${esc(t('No profiles'))}</td></tr>`;
  const emptyFailing = `<tr><td colspan="4" class="muted">${esc(t('None'))}</td></tr>`;
  const emptyWaivers = `<tr><td colspan="6" class="muted">${esc(t('None'))}</td></tr>`;
  // CSP: no scripts (report is static HTML). style-src unsafe-inline covers the
  // embedded chrome CSS only; all untrusted text is HTML-escaped above.
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"><meta name="referrer" content="no-referrer"><title>${esc(t('Compliance report'))}</title>
<style>body{font-family:sans-serif;margin:2rem;color:#151515}h1{margin-bottom:0}
table{border-collapse:collapse;margin:1rem 0;width:100%}th,td{border:1px solid #ccc;padding:4px 8px;text-align:left}
.muted{color:#666}</style></head><body>
<h1>${esc(t('Compliance report'))}</h1>
<p class="muted">${esc(t('Generated {{when}} • last scan {{lastScan}}', {
    when: now.toISOString(),
    lastScan: st.lastScanTime ?? t('n/a'),
  }))}</p>
<h2>${esc(t('Score: {{score}}', { score }))}</h2>
<h3>${esc(t('Profiles'))}</h3>
<table><thead><tr><th>${esc(t('Profile'))}</th><th>${esc(t('Pass'))}</th><th>${esc(t('Fail'))}</th><th>${esc(t('Manual'))}</th><th>${esc(t('Inconsistent'))}</th><th>${esc(t('Waived'))}</th></tr></thead>
<tbody>${profileRows || emptyProfiles}</tbody></table>
<h3>${esc(t('Failing checks'))}</h3>
<table><thead><tr><th>${esc(t('Check'))}</th><th>${esc(t('Title'))}</th><th>${esc(t('Profile'))}</th><th>${esc(t('Severity'))}</th></tr></thead>
<tbody>${failingRows || emptyFailing}</tbody></table>
<h3>${esc(t('Active waivers ({{count}})', { count: activeWaivers.length }))}</h3>
<table><thead><tr><th>${esc(t('Check'))}</th><th>${esc(t('Reason'))}</th><th>${esc(t('Requested by'))}</th><th>${esc(t('Approved by'))}</th><th>${esc(t('Expires'))}</th><th>${esc(t('Review by'))}</th></tr></thead>
<tbody>${waiverRows || emptyWaivers}</tbody></table>
</body></html>`;
};
