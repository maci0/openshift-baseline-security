// Domain modules split out of this bag for navigability. Re-export so existing
// `from './utils'` / `from '../utils'` import paths stay stable.
export {
  resourceVersionTest,
  tailoredProfileBindingPatch,
  schedulePatch,
  batchApplyPatch,
  remediationApplyPatch,
  addWaiverPatch,
  removeWaiverPatch,
  rescanPatch,
} from './patches';
export { isValidCron } from './cron';
export { waiverExpired, findWaiver, isWaived, expiringWaivers } from './waivers';
export {
  clusterScore,
  aggregateCounts,
  scoreColor,
  severityWeight,
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
  ChangedCheck,
  changedChecks,
  nodeScanPool,
} from './results';
export { checkResultHref, machineConfigPoolHref, resultsHref } from './links';
export { isNodeRemediation, remediationObjectText } from './remediation';
export { ReportTranslate, buildReportHtml } from './report';

// Normalize k8s watch / fetch errors (string | Error | { message }) for Alerts.
export const errorMessage = (err: unknown): string | null => {
  if (err == null || err === '') {
    return null;
  }
  if (typeof err === 'string') {
    return err;
  }
  if (err instanceof Error) {
    return err.message || err.name;
  }
  // A message-bearing object, a null-prototype object, a throwing toString, or a
  // throwing `message` getter must all be tolerated: an error normalizer must
  // never throw. Guard the whole property access + String() fallback.
  try {
    if (typeof err === 'object' && 'message' in err) {
      const m = (err as { message: unknown }).message;
      if (typeof m === 'string' && m) {
        return m;
      }
    }
    return String(err);
  } catch {
    return 'Unknown error';
  }
};

// True for an apiserver "already exists" (409) rejection, so a create can be
// retried idempotently after a later step failed.
export const isAlreadyExists = (e: unknown): boolean => {
  const o = e as { code?: number; reason?: string; message?: string } | null;
  return (
    o?.code === 409 ||
    o?.reason === 'AlreadyExists' ||
    (typeof o?.message === 'string' && /already exists/i.test(o.message))
  );
};

// New profile list after toggling one key. An empty result is valid: clearing
// every profile disables scanning (the operator prunes the bindings).
export const toggledProfiles = (current: string[], key: string, checked: boolean): string[] =>
  checked ? [...new Set([...current, key])] : current.filter((p) => p !== key);

// Build a TailoredProfile CR body from an editor: a base profile to extend and
// optional rule names to enable/disable. Empty rule lists are omitted.
export const tailoredProfileManifest = (
  name: string,
  extendsProfile: string,
  disableRules: string[],
  enableRules: string[] = [],
): Record<string, unknown> => {
  const spec: Record<string, unknown> = {
    title: name,
    extends: extendsProfile,
  };
  const rule = (n: string) => ({ name: n, rationale: 'set via console' });
  if (enableRules.length) spec.enableRules = enableRules.map(rule);
  if (disableRules.length) spec.disableRules = disableRules.map(rule);
  return {
    apiVersion: 'compliance.openshift.io/v1alpha1',
    kind: 'TailoredProfile',
    metadata: { name, namespace: 'openshift-compliance' },
    spec,
  };
};

// A date-only deadline remains active through the selected local calendar day.
// Parsing YYYY-MM-DD directly as a Date means UTC midnight, which can expire it
// before that day starts locally and display as the previous day in some zones.
export const dateInputEndOfDayIso = (value: string): string | undefined => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return undefined;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const deadline = new Date(0);
  deadline.setFullYear(year, month - 1, day);
  deadline.setHours(23, 59, 59, 999);
  if (
    deadline.getFullYear() !== year ||
    deadline.getMonth() !== month - 1 ||
    deadline.getDate() !== day
  ) {
    return undefined;
  }
  return deadline.toISOString();
};
