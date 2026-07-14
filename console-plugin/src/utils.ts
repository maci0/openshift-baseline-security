// Barrel re-exports for domain modules under src/. Prefer importing from the
// owning module in new code; this file keeps `from './utils'` stable for tests
// (components import domain modules directly).
//
// Module map (where new logic should go):
//   models.ts       - K8s types, GVKs, CRD-aligned constants, profile display
//   patches.ts      - ClusterBaseline / TailoredProfile JSON patches
//   scoring.ts      - pass/fail and severity-weighted score math
//   status.ts       - INCONSISTENT collapse, SKIP to N/A, waived-FAIL filter status
//   results.ts      - result display helpers, CSV, scan-diff rows
//   waivers.ts      - waiver lookup, expiry, active-set helpers
//   dates.ts        - local-calendar date input and display helpers
//   profiles.ts     - profile toggle and TailoredProfile manifests
//   remediation.ts  - remediation kind / object rendering
//   report.ts       - printable HTML compliance report
//   cron.ts         - cron expression validation
//   names.ts        - K8s / tailored name validation
//   links.ts        - console deep-link hrefs
//   errors.ts       - watch/fetch error normalization
//   download.ts     - browser blob download
//   components/     - React pages, tabs, page context, UI-only helpers
//                     (import domain modules directly; do not use this barrel)
//   components/feedback.ts - shared success-banner dismiss timing
//
// Tests: put new unit/fuzz coverage next to the domain module as
// `<module>.test.ts` (import from that module, not this barrel). utils.test.ts
// is the legacy cross-module suite kept for existing coverage stability.
export {
  resourceVersionTest,
  tailoredProfileBindingPatch,
  schedulePatch,
  batchApplyPatch,
  batchApplyRequested,
  remediationApplyPatch,
  addWaiverPatch,
  removeWaiverPatch,
  rescanPatch,
} from './patches';
export { isValidCron } from './cron';
export {
  waiverExpired,
  findWaiver,
  isWaived,
  activeWaivedNames,
  expiringWaivers,
  soonestDeadlineDelayMs,
  futureWaiverDeadlineMs,
} from './waivers';
export {
  dateInputEndOfDayIso,
  localDateInputValue,
  formatLocalDate,
  formatLocalDateTime,
  formatCount,
  formatChartDate,
  safeLocale,
} from './dates';
export {
  HISTORY_SCORING_MODE_ANN,
  ScoringMode,
  SCORE_DANGER_BELOW,
  SCORE_SUCCESS_AT,
  effectiveScoringMode,
  historyScoringModeMismatch,
  clusterScore,
  aggregateCounts,
  scoreColor,
  scoreLabelColor,
  severityWeight,
  checkSeverity,
  flatProfileScore,
  profileScore,
} from './scoring';
export {
  NodeStatus,
  inconsistentSources,
  effectiveStatus,
  resultFilterStatus,
} from './status';
export { isValidK8sName, isValidTailoredProfileName } from './names';
export {
  checkTitle,
  checkBody,
  resultsCsv,
  changedChecksMany,
  ChangedCheck,
  changedChecks,
  nodeScanPool,
  severityDisplayTitle,
} from './results';
export { checkResultHref, machineConfigPoolHref, resultsHref } from './links';
export {
  isNodeRemediation,
  remediationObjectText,
  missingDependencySummary,
  compareRemediationsForApplyOrder,
} from './remediation';
export { downloadBlob } from './download';
export { ReportTranslate, buildReportHtml } from './report';
export { errorMessage, isAlreadyExists } from './errors';
export { toggledProfiles, tailoredProfileManifest, tailoredProfileSpecMatches } from './profiles';
export { PROFILE_INFO, profileTitle } from './models';
