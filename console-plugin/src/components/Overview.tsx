import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sPatch,
  useAccessReview,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Label,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  EmptyState,
  EmptyStateBody,
  Flex,
  FlexItem,
  Gallery,
  HelperText,
  HelperTextItem,
  Icon,
  PageSection,
  Skeleton,
  Split,
  SplitItem,
  TextInput,
  Timestamp,
} from '@patternfly/react-core';
import {
  CheckCircleIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  InfoCircleIcon,
  MinusCircleIcon,
} from '@patternfly/react-icons';
import {
  ClusterBaseline,
  clusterBaselinePatchAccess,
  ClusterBaselineModel,
  ComplianceCheckResult,
  profileTitle,
  ResultCounts,
  scanningDisabled,
  ScoreSnapshot,
  suiteFilterKey,
} from '../models';
import { effectiveSchedule, isValidCron } from '../cron';
import {
  CHANGES_MAX_HEIGHT,
  DASHBOARD_CARD_MIN_WIDTH,
  DONUT_CARD_MIN_HEIGHT,
  DONUT_SKELETON_HEIGHT,
  SCHEDULE_FIELD_MIN_WIDTH,
  SCORE_CARD_MIN_WIDTH,
  SPARKLINE_HEIGHT,
  TREND_SKELETON_HEIGHT,
} from '../layout';
import { AccessGate, mayWrite } from '../permissions';
import { formatCount, parseInstant, safeLocale } from '../dates';
import { errorMessage } from '../errors';
import { resultsHref } from '../links';
import { historyContentKey, toTrendData } from '../overviewTrend';
import { encodeKeyList } from '../contentKey';
import { resourceVersionTest, schedulePatch } from '../patches';
import { ChangedCheck, changedChecksMany } from '../results';
import { formatList, listSeparators } from '../text';
import {
  aggregateCounts,
  effectiveScoringMode,
  historyScoringModeMismatch,
  latestSnapshotScore,
  normalizeScore,
  profileScore,
  scoreLabelColor,
} from '../scoring';
import {
  activeWaivedNames,
  expiringWaivers,
} from '../waivers';
import BaselineNotConfigured from './BaselineNotConfigured';
import { BaselineUnavailableSection } from './BaselineUnavailable';
import LoadingCards from './LoadingCards';
import ConsoleLink from './ConsoleLink';
import { regionFocusProps, withDisabledTip } from './DisabledTip';
import { ChunkError } from './ChunkError';
import { useChunk } from './chunkLoad';
import { useAutoDismiss } from './useAutoDismiss';
import { useWaiverExpiryClock } from './useWaiverExpiryClock';

// Start the charts chunk as soon as this module evaluates (default tab), so
// Victory download overlaps first paint of the score cards instead of waiting
// for the donut slot to render.
const loadOverviewCharts = () =>
  import(/* webpackChunkName: "overview-charts" */ './OverviewCharts');
// The catch is here only to keep a failed GET out of the unhandled-rejection
// path: webpack hands the charts gate the same cached (rejected) promise, and
// that gate is the one surface that reports the failure with Retry.
void loadOverviewCharts().catch(() => undefined);

// Stable empty list so optional status arrays do not allocate each render.
const EMPTY_NAMES: readonly string[] = [];
const EMPTY_RESULTS: ComplianceCheckResult[] = [];
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;
// The CRD caps status.newlyFailed / status.fixed at 4096 names each, and the
// first paint maps every one of them into the alert and the Recent-changes
// card. Thousands of links in one commit block the main thread long enough to
// hold up the rest of the page, so both surfaces render this many per group
// first and the card offers the rest on demand.
const CHANGES_RENDER_LIMIT = 25;
// Module constant, not an inline literal: useWaiverExpiryClock memoizes its
// content key on this array's identity, and a fresh literal every render would
// rebuild the key (and reschedule the expiry timer) on every keystroke.
const EXPIRING_SOON_OFFSET_MS: readonly number[] = [-2 * WEEK_MS];

// status.lastScanTime / status.nextScanTime are cluster-supplied RFC3339
// strings. PatternFly's Timestamp takes `date`, and substitutes the browser's
// own `new Date()` when the prop is absent or unparseable, so passing the
// timestamp under any other name (or passing a corrupt value straight
// through) painted the current wall clock in the scan rows: "Last scan" read
// as now on every mount and the Details card carried no scan time at all.
// Resolve the instant here and show the raw string when it is not one, which
// keeps a hand-edited status visible instead of inventing a fresh-looking one.
const ClusterTimestamp: React.FC<{ value: string; locale?: string }> = ({
  value,
  locale,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const instant = parseInstant(value);
  return instant ? (
    <Timestamp date={instant} locale={locale} />
  ) : (
    <span aria-label={t('Unknown')}>{value}</span>
  );
};

// Donut segment colors (module-level so CCR churn does not rebind CSS var
// strings). Named for the token each one reads, not for the hue it resolves to:
// the custom token lands on a teal and the info token on a purple, so a
// color-named constant here would read as the wrong status.
const DONUT_GREEN = 'var(--pf-t--global--icon--color--status--success--default)';
const DONUT_RED = 'var(--pf-t--global--icon--color--status--danger--default)';
const DONUT_ORANGE = 'var(--pf-t--global--icon--color--status--warning--default)';
const DONUT_CUSTOM = 'var(--pf-t--global--icon--color--status--custom--default)';
const DONUT_GREY = 'var(--pf-t--global--icon--color--disabled)';
const DONUT_INFO = 'var(--pf-t--global--icon--color--status--info--default)';
// Distinct hues so Error is not confused with Fail, nor Waived with N/A. The
// Observe dashboard stacks the same eight statuses, where two adjacent
// same-colored bands are just as unreadable, so it paints these last two from
// the same tokens
// (operator/internal/controller/assets/compliance-dashboard.json, pinned by
// TestDashboardUsesStatusPalette).
const DONUT_ORANGERED = 'var(--pf-t--global--color--nonstatus--orangered--default)';
const DONUT_TEAL = 'var(--pf-t--global--color--nonstatus--teal--default)';

// Inline editor for spec.schedule in the Details card, gated on patch permission.
const ScheduleEditor: React.FC<{ baseline: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  // effectiveSchedule, not String#trim: the value read back is the one the
  // operator's normalizeAndParseSchedule sees, so a schedule the operator calls
  // InvalidSchedule cannot render here as a healthy cron.
  const current = effectiveSchedule(baseline.spec.schedule);
  const [editing, setEditing] = React.useState(false);
  const [value, setValue] = React.useState(current);
  const [busy, setBusy] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render.
  const busyRef = React.useRef(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [saved, setSaved] = React.useState(false);
  const inputRef = React.useRef<HTMLInputElement>(null);
  const editButtonRef = React.useRef<HTMLButtonElement>(null);
  // Track edit sessions so Cancel/Save can restore focus to Edit (WCAG 2.4.3).
  const wasEditing = React.useRef(false);
  // Auto-clear "Schedule updated" so success feedback matches other tabs.
  useAutoDismiss(saved, false, () => setSaved(false));
  const [canEdit, canEditLoading] = useAccessReview(clusterBaselinePatchAccess);
  const editGate: AccessGate = { allowed: canEdit };
  const valid = isValidCron(value);

  // Move focus into the field when opening edit; return it to Edit when closing.
  React.useEffect(() => {
    if (editing) {
      inputRef.current?.focus();
      wasEditing.current = true;
    } else if (wasEditing.current) {
      editButtonRef.current?.focus();
      wasEditing.current = false;
    }
  }, [editing]);

  const cancelEdit = () => {
    if (busyRef.current) return;
    setValue(current);
    setErr(null);
    setEditing(false);
  };

  if (!editing) {
    return (
      <>
        <Split hasGutter>
          <SplitItem>
            <code>{current}</code>
          </SplitItem>
          <SplitItem>
            {withDisabledTip(
              canEditLoading
                ? t('Checking permissions…')
                : canEdit
                  ? undefined
                  : t('You do not have permission to edit the schedule.'),
              <Button
                ref={editButtonRef}
                variant="link"
                isInline
                isDisabled={!canEdit || canEditLoading}
                // Named action: bare "Edit" is ambiguous next to other page links.
                aria-label={t('Edit schedule')}
                onClick={() => {
                  setValue(current);
                  setErr(null);
                  setSaved(false);
                  setEditing(true);
                }}
              >
                {t('Edit')}
              </Button>,
            )}
          </SplitItem>
        </Split>
        {saved && (
          <HelperText role="status">
            <HelperTextItem variant="success">{t('Schedule updated.')}</HelperTextItem>
          </HelperText>
        )}
      </>
    );
  }
  const save = async () => {
    if (!valid || busyRef.current) return;
    // Same gate the Edit control carries, so an edit form opened while permitted
    // cannot spend the patch after the review flipped to denied.
    if (!mayWrite(editGate)) {
      setErr(t('You do not have permission to edit the baseline.'));
      return;
    }
    // Presence is != null (not !!): empty string is still a present field.
    // Empty schedule ops would leave only an RV test: a successful no-op that
    // looks like the schedule was updated when nothing changed. schedulePatch
    // trims with the operator's separator set, not String#trim, so the value it
    // stores is the one the operator will split into the same fields.
    const scheduleOps = schedulePatch(value);
    if (!scheduleOps.length) {
      setErr(t('Invalid schedule. Use a five-field cron expression.'));
      return;
    }
    busyRef.current = true;
    setBusy(true);
    setErr(null);
    try {
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        data: [
          ...resourceVersionTest(baseline.metadata.resourceVersion),
          ...scheduleOps,
        ],
      });
      setSaved(true);
      setEditing(false);
    } catch (e) {
      setErr(errorMessage(e) ?? t('Failed to update schedule.'));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };
  return (
    <>
      {/* Flex wrap: Split keeps Save/Cancel on one line and clips on narrow cards. */}
      <Flex
        gap={{ default: 'gapSm' }}
        alignItems={{ default: 'alignItemsCenter' }}
        flexWrap={{ default: 'wrap' }}
      >
        <FlexItem grow={{ default: 'grow' }} style={{ minWidth: SCHEDULE_FIELD_MIN_WIDTH }}>
          <TextInput
            ref={inputRef}
            id="schedule-cron"
            aria-label={t('Schedule')}
            aria-invalid={!valid}
            aria-describedby="schedule-cron-help"
            value={value}
            onChange={(_e, v) => {
              setValue(v);
              // Clear a previous save error once the user edits again.
              if (err) setErr(null);
            }}
            // Cron is not prose: suppress browser spellcheck and password managers.
            spellCheck={false}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                // Invalid Enter must not look broken: helper already shows the rule;
                // also surface the Alert path used by save failures.
                if (!valid) {
                  setErr(t('Enter a 5-field cron schedule.'));
                  return;
                }
                void save();
              } else if (e.key === 'Escape') {
                e.preventDefault();
                cancelEdit();
              }
            }}
            validated={valid ? 'default' : 'error'}
          />
        </FlexItem>
        <FlexItem>
          <Button variant="primary" isInline isDisabled={!valid || busy} isLoading={busy} onClick={() => void save()}>
            {t('Save schedule')}
          </Button>
        </FlexItem>
        <FlexItem>
          <Button variant="link" isInline isDisabled={busy} onClick={cancelEdit}>
            {t('Cancel')}
          </Button>
        </FlexItem>
      </Flex>
      <HelperText id="schedule-cron-help">
        <HelperTextItem variant={valid ? 'default' : 'error'}>
          {valid
            ? t('Five-field cron (minute hour day-of-month month day-of-week). Example: 0 1 * * *')
            : t('Enter a 5-field cron schedule.')}
        </HelperTextItem>
      </HelperText>
      {err && (
        <Alert
          variant="danger"
          isInline
          isLiveRegion
          title={<span dir="auto">{err}</span>}
          style={{ marginTop: 'var(--pf-t--global--spacer--xs)' }}
        />
      )}
    </>
  );
};

const CountRow: React.FC<{
  icon: React.ReactElement;
  status: React.ComponentProps<typeof Icon>['status'];
  label: string;
  count: number;
  href?: string;
}> = ({ icon, status, label, count, href }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  // formatCount: underscore BCP 47 + invalid tags (toLocaleString throws RangeError).
  const countText = formatCount(count, i18n.language);
  // Only linked when there is a target and a non-zero count (zero rows have
  // nothing to deep-link to). Whole row is the hit target when linked (not only
  // the number): larger click area, matches "Fail" navigating like the count.
  const linked = !!href && count > 0;
  const row = (
    <Flex gap={{ default: 'gapSm' }} alignItems={{ default: 'alignItemsCenter' }}>
      <FlexItem>
        <Icon status={status} size="sm">
          {icon}
        </Icon>
      </FlexItem>
      <FlexItem grow={{ default: 'grow' }}>{label}</FlexItem>
      <FlexItem>
        {linked ? (
          <span
            style={{
              color: 'var(--pf-t--global--text--color--link--default)',
              textDecoration: 'underline',
            }}
          >
            {countText}
          </span>
        ) : (
          countText
        )}
      </FlexItem>
    </Flex>
  );
  if (linked) {
    return (
      <ConsoleLink
        href={href}
        // Named for screen readers: "Fail: 5", not a bare number (WCAG 2.4.4).
        aria-label={t('{{label}}: {{value}}', { label, value: countText })}
        style={{ color: 'inherit', textDecoration: 'none', display: 'block' }}
      >
        {row}
      </ConsoleLink>
    );
  }
  return row;
};

// Static row metadata (icons once at module load). Labels are i18n source keys.
const PROFILE_COUNT_ROWS: readonly {
  k: keyof ResultCounts;
  f: string;
  labelKey: string;
  icon: React.ReactElement;
  status: React.ComponentProps<typeof Icon>['status'];
  always?: boolean;
}[] = [
  { k: 'pass', f: 'PASS', labelKey: 'Pass', icon: <CheckCircleIcon />, status: 'success', always: true },
  { k: 'fail', f: 'FAIL', labelKey: 'Fail', icon: <ExclamationCircleIcon />, status: 'danger', always: true },
  { k: 'manual', f: 'MANUAL', labelKey: 'Manual', icon: <ExclamationTriangleIcon />, status: 'warning' },
  { k: 'info', f: 'INFO', labelKey: 'Info', icon: <InfoCircleIcon />, status: 'info' },
  {
    k: 'inconsistent',
    f: 'INCONSISTENT',
    labelKey: 'Inconsistent',
    icon: <ExclamationTriangleIcon />,
    status: 'custom',
  },
  { k: 'error', f: 'ERROR', labelKey: 'Error', icon: <ExclamationCircleIcon />, status: 'danger' },
  { k: 'waived', f: 'WAIVED', labelKey: 'Waived', icon: <MinusCircleIcon />, status: undefined },
  {
    k: 'notApplicable',
    f: 'NOT-APPLICABLE',
    labelKey: 'Not applicable',
    icon: <MinusCircleIcon />,
    status: undefined,
  },
];

// Per-profile status rows for a score card. Pass/Fail always show; the rest
// (Manual, Info, Inconsistent, Error, Waived, N/A) show only when non-zero, so
// a card's rows match the statuses the composition donut aggregates.
// Memoized: counts identity is stable while CCR watch churn re-renders Overview.
const ProfileCounts = React.memo<{ counts: ResultCounts; filterKey: string }>(
  ({ counts, filterKey }) => {
    const { t } = useTranslation('plugin__baseline-security-console-plugin');
    return (
      <>
        {PROFILE_COUNT_ROWS.filter((r) => r.always || (counts[r.k] ?? 0) > 0).map((r) => (
          <CountRow
            key={r.k}
            icon={r.icon}
            status={r.status}
            label={t(r.labelKey)}
            count={counts[r.k] ?? 0}
            href={resultsHref(r.f, filterKey)}
          />
        ))}
      </>
    );
  },
);
ProfileCounts.displayName = 'ProfileCounts';

const Overview: React.FC<{
  baseline?: ClusterBaseline;
  loaded: boolean;
  // Set when the baseline watch failed. CompliancePage forces `loaded` true on
  // an error, so without it a failed watch falls into BaselineNotConfigured and
  // claims the CR does not exist.
  baselineError?: unknown;
  // Shared from CompliancePage (single watch); used for Recent changes titles
  // and SeverityWeighted per-profile scores.
  checkResults?: ComplianceCheckResult[];
  // Set when the check-results watch failed. The list is then empty for a
  // reason that has nothing to do with the checks, so "no longer in the
  // results" would be a false statement about why a name is unmatched.
  checkResultsError?: unknown;
}> = ({ baseline, loaded, baselineError, checkResults, checkResultsError }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  // One BCP 47 tag for all score/count formatting (same path as report / dates).
  const locale = safeLocale(i18n.language);
  const [chartAttempt, setChartAttempt] = React.useState(0);
  const charts = useChunk(loadOverviewCharts, chartAttempt);

  // SeverityWeighted per-profile scores need to scan check results. Group owned
  // results by filter key once here instead of letting each profile card's
  // profileScore re-scan every result (O(cards x results)); each card then only
  // weighs its own bucket. null in Flat mode, where scores use counts alone.
  // checkResults is already baseline-owned (CompliancePage suite selector);
  // only bucket by suiteFilterKey (no second ownership scan).
  const scoringMode = effectiveScoringMode(baseline);
  const weighted = scoringMode === 'SeverityWeighted';
  const historyModeMismatch = historyScoringModeMismatch(baseline);
  const resultsByKey = React.useMemo(() => {
    if (!weighted) {
      return null;
    }
    const m = new Map<string, ComplianceCheckResult[]>();
    for (const r of checkResults ?? []) {
      // Optional-chain: partial/tampered CCR list items must not crash Overview.
      const key = suiteFilterKey(r.metadata?.labels);
      if (key === undefined) {
        continue;
      }
      const arr = m.get(key);
      if (arr) {
        arr.push(r);
      } else {
        m.set(key, [r]);
      }
    }
    return m;
  }, [weighted, checkResults]);

  // Content keys: status-only CR updates reallocate waivers/profiles arrays with
  // the same membership; identity deps would re-weigh multi-thousand CCRs every
  // reconcile even when score inputs did not change.
  const waivers = baseline?.spec.waivers;
  // Active waivers are time-sensitive: membership alone is not enough. A waiver
  // can expire (or enter the expiring-soon window) with no CR edit. Without a
  // tick, SeverityWeighted profile badges and the expiring-soon alert stay
  // wrong until CCR identity or waiversKey change. ResultsTab clocks expiry
  // only; Overview also clocks window entry for the 2-week alert (-2w offset).
  const { key: waiversKey, tick: waiverClock } = useWaiverExpiryClock(
    waivers,
    EXPIRING_SOON_OFFSET_MS,
  );
  // Last history tip per bucket (empty-CCR fallback in profileScore only).
  const statusProfiles = baseline?.status?.profiles;
  const statusTailored = baseline?.status?.tailoredProfiles;
  const profileHistKey = React.useMemo(() => {
    let key = '';
    for (const p of statusProfiles ?? []) {
      key += encodeKeyList([p.key, latestSnapshotScore(p.history)]);
    }
    for (const tp of statusTailored ?? []) {
      key += encodeKeyList([`tp:${tp.name}`, latestSnapshotScore(tp.history)]);
    }
    return key;
  }, [statusProfiles, statusTailored]);
  // Per-bucket result counts. profileScore falls back to flat pass/fail counts
  // when the CCR bucket is empty and history is empty too, so weightedScores
  // below reads these and must recompute when they change.
  const countsKey = React.useMemo(() => {
    let key = '';
    for (const p of statusProfiles ?? []) {
      key += encodeKeyList([
        p.key, p.pass ?? 0, p.fail ?? 0, p.manual ?? 0, p.info ?? 0,
        p.error ?? 0, p.inconsistent ?? 0, p.waived ?? 0, p.notApplicable ?? 0,
      ]);
    }
    for (const tp of statusTailored ?? []) {
      key += encodeKeyList([
        `tp:${tp.name}`, tp.pass ?? 0, tp.fail ?? 0, tp.manual ?? 0, tp.info ?? 0,
        tp.error ?? 0, tp.inconsistent ?? 0, tp.waived ?? 0, tp.notApplicable ?? 0,
      ]);
    }
    return key;
  }, [statusProfiles, statusTailored]);

  // One waiver Set + one score pass for all cards (avoids N Set builds and
  // re-scoring every Overview re-render during CCR watch churn).
  const weightedScores = React.useMemo(() => {
    if (!resultsByKey) {
      return null;
    }
    const waived = activeWaivedNames(waivers);
    const scores = new Map<string, number | null>();
    for (const p of statusProfiles ?? []) {
      scores.set(
        p.key,
        profileScore(p, {
          mode: 'SeverityWeighted',
          filterKey: p.key,
          results: resultsByKey.get(p.key) ?? EMPTY_RESULTS,
          activeWaived: waived,
          history: p.history,
        }),
      );
    }
    for (const tp of statusTailored ?? []) {
      const key = `tp-${tp.name}`;
      scores.set(
        key,
        profileScore(tp, {
          mode: 'SeverityWeighted',
          filterKey: key,
          results: resultsByKey.get(key) ?? EMPTY_RESULTS,
          activeWaived: waived,
          history: tp.history,
        }),
      );
    }
    return scores;
    // profiles/waivers read when content keys or expiry clock change.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys + clock
  }, [resultsByKey, waiversKey, profileHistKey, countsKey, waiverClock]);

  // Hooks must run before early returns. Resolve titles from the watched CCR
  // list; one index pass for newlyFailed + fixed (no dual full-list scan).
  // Content keys: status updates reallocate these arrays with the same names.
  const newlyFailed = baseline?.status?.newlyFailed ?? EMPTY_NAMES;
  const fixed = baseline?.status?.fixed ?? EMPTY_NAMES;
  // The CRD caps these at 4096 names, so the key join is megabytes of string.
  // Memoize on array identity: status updates reallocate the array, so a new
  // reference still recomputes, while a re-render from any other state does not.
  const newlyFailedKey = React.useMemo(() => encodeKeyList(newlyFailed), [newlyFailed]);
  const fixedKey = React.useMemo(() => encodeKeyList(fixed), [fixed]);
  const recentChanges = React.useMemo(
    () => changedChecksMany([newlyFailed, fixed], checkResults),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys
    [newlyFailedKey, fixedKey, checkResults],
  );
  const newlyFailedItems = recentChanges[0];
  const fixedItems = recentChanges[1];
  // Only the rendered slice goes into the DOM; the counts in the alert title
  // and the group terms stay the full totals, so nothing is silently dropped
  // from the picture. The card toggles the rest in on demand.
  const [showAllChanges, setShowAllChanges] = React.useState(false);
  const visibleNewlyFailed = showAllChanges
    ? newlyFailedItems
    : newlyFailedItems.slice(0, CHANGES_RENDER_LIMIT);
  const visibleFixed = showAllChanges ? fixedItems : fixedItems.slice(0, CHANGES_RENDER_LIMIT);
  const changesTruncated = !showAllChanges && (visibleNewlyFailed.length < newlyFailedItems.length
    || visibleFixed.length < fixedItems.length);
  // List punctuation for the alert's inline link list of check links.
  const newlyFailedSeparators = React.useMemo(
    () => listSeparators(visibleNewlyFailed.length, locale),
    [visibleNewlyFailed.length, locale],
  );
  // Names in status.newlyFailed with no current check result: nothing to link,
  // so the alert reports them apart from the ones it can name.
  const unresolvedNewlyFailed = newlyFailed.length - newlyFailedItems.length;
  // An unreadable results list makes every name unmatched. Say so rather than
  // attributing it to a removed rule.
  const resultsUnreadable = !!checkResultsError;

  // Main score-trend chart: same CCR-churn stability as MiniTrend (Date objects
  // and Victory path data must not rebuild when history content is unchanged).
  const history = baseline?.status?.history;
  const overallHistKey = historyContentKey(history);
  const historyChartData = React.useMemo(
    () => toTrendData(history),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key
    [overallHistKey],
  );

  // Composition donut totals from status only (not CCR list). Memoize so CCR
  // watch churn does not re-aggregate when profile arrays only reallocate.
  // countsKey above: identity of status.profiles flaps every status update
  // even when rollup numbers are unchanged.
  const totals = React.useMemo(
    () => aggregateCounts(...(statusProfiles ?? []), ...(statusTailored ?? [])),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key
    [countsKey],
  );

  // Segment labels + Victory series: stable when totals/t unchanged so CCR
  // watch re-renders do not rebuild donut data/colorScale every tick.
  // filter keys match Results rowFilter-result-status / resultsHref (and
  // profile CountRow links) so the composition legend is a drill-down, not
  // dead text beside a chart that looks interactive.
  const segments = React.useMemo(() => {
    return [
      { label: t('Pass'), value: totals.pass, color: DONUT_GREEN, filter: 'PASS' },
      { label: t('Fail'), value: totals.fail, color: DONUT_RED, filter: 'FAIL' },
      { label: t('Manual'), value: totals.manual, color: DONUT_ORANGE, filter: 'MANUAL' },
      { label: t('Info'), value: totals.info, color: DONUT_INFO, filter: 'INFO' },
      { label: t('Inconsistent'), value: totals.inconsistent, color: DONUT_CUSTOM, filter: 'INCONSISTENT' },
      { label: t('Error'), value: totals.error, color: DONUT_ORANGERED, filter: 'ERROR' },
      { label: t('Waived'), value: totals.waived, color: DONUT_TEAL, filter: 'WAIVED' },
      { label: t('Not applicable'), value: totals.notApplicable, color: DONUT_GREY, filter: 'NOT-APPLICABLE' },
    ].filter((s) => s.value > 0);
  }, [totals, t]);

  // expiringWaivers parses an expiresAt per waiver (up to 256), so a bare call
  // re-did that on every render, including the CCR-watch churn that re-renders
  // Overview without touching the waiver list. waiversKey is the content key
  // useWaiverExpiryClock above already derived from the same name and expiry of
  // every entry, and waiverClock advances when time passes, so an expiry that
  // enters or leaves the two-week window still re-derives. Above the early
  // returns below, which would otherwise skip the hook entirely.
  const expiring = React.useMemo(
    () => expiringWaivers(waivers, 2 * WEEK_MS),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key + clock
    [waiversKey, waiverClock],
  );

  if (!loaded) {
    return (
      <LoadingCards
        cardMinWidth={DASHBOARD_CARD_MIN_WIDTH}
        skeletonHeight={DONUT_SKELETON_HEIGHT}
      />
    );
  }
  if (!baseline) {
    return baselineError ? (
      <BaselineUnavailableSection error={baselineError} />
    ) : (
      <PageSection>
        <BaselineNotConfigured />
      </PageSection>
    );
  }

  const degraded = baseline.status?.conditions?.find(
    (c) => c.type === 'Degraded' && c.status === 'True',
  );
  const progressing = baseline.status?.conditions?.find(
    (c) => c.type === 'Progressing' && c.status === 'True',
  );
  const coReady = baseline.status?.conditions?.find((c) => c.type === 'ComplianceOperatorReady');
  const coVersion = baseline.status?.complianceOperatorVersion;
  // Prefer version when present. Otherwise map terminal/stalled reasons so the
  // Details card does not say "Installing" after InstallStalled or CSVFailed.
  let coLabel = t('Installing');
  if (coVersion) {
    coLabel = coVersion;
  } else if (coReady?.reason === 'NotInstalled') {
    coLabel = t('Not installed');
  } else if (coReady?.reason === 'CSVFailed') {
    coLabel = t('Failed');
  } else if (degraded?.reason === 'InstallStalled') {
    coLabel = t('Install stalled');
  } else if (coReady?.status === 'True') {
    coLabel = t('Installed');
  }
  // normalizeScore, not the raw field: a non-finite or out-of-range
  // status.score must paint the unscored neutral (dash title, neutral trend
  // fill), never "NaN" or an empty donut center.
  const score = normalizeScore(baseline.status?.score);
  // PascalCase so TSX treats Charts.MiniTrend as a component, not an HTML tag.
  const Charts = charts.status === 'ready' ? charts.module : null;

  // Prefer status.diffBaseScanTime (set once a prior completed scan exists for
  // regression diff). History length alone is wrong when the first scan had no
  // countable score (history stays short) but a second scan already compared.
  const hasPriorScan =
    !!baseline.status?.diffBaseScanTime || (baseline.status?.history?.length ?? 0) > 1;

  // Compact score chip shown as a per-profile card action (built-in + tailored).
  const scoreLabel = (pScore: number | null) =>
    pScore != null ? (
      <Label
        isCompact
        color={scoreLabelColor(pScore)}
        aria-label={t('Compliance score {{score}} of {{max}}', {
          score: formatCount(pScore, locale),
          max: 100,
          formattedMax: formatCount(100, locale),
        })}
      >
        {formatCount(pScore, locale)}
      </Label>
    ) : undefined;

  // One card renderer for built-in and tailored profile rows so layout, score
  // chip, count rows, and sparkline cannot drift between the two lists.
  // Plain function (not a component element): no remount churn per render.
  const scoreCard = (
    filterKey: string,
    title: React.ReactNode,
    counts: ResultCounts,
    history?: ScoreSnapshot[],
  ) => {
    // Flat: counts only. SeverityWeighted: memoized weightedScores map.
    const pScore = weightedScores
      ? (weightedScores.get(filterKey) ?? null)
      : profileScore(counts);
    return (
      <Card key={filterKey}>
        <CardHeader
          actions={{
            actions: scoreLabel(pScore),
            hasNoOffset: true,
          }}
        >
          <CardTitle>{title}</CardTitle>
        </CardHeader>
        <CardBody style={{ display: 'flex', flexDirection: 'column' }}>
          <ProfileCounts counts={counts} filterKey={filterKey} />
          {Charts ? (
            <Charts.MiniTrend history={history} />
          ) : charts.status === 'loading' ? (
            // Reserve the sparkline slot so cards stay bottom-aligned while
            // the charts chunk is in flight (the height MiniTrend draws at).
            <div style={{ height: SPARKLINE_HEIGHT, marginTop: 'auto' }} />
          ) : null}
        </CardBody>
      </Card>
    );
  };

  // One list renderer for both Recent-changes groups so the marker icon, the
  // dir="auto" link, and the row shape cannot drift between newly failing and
  // fixed. The term is passed already translated so each key stays a literal.
  const changeList = (
    term: string,
    items: ChangedCheck[],
    status: 'danger' | 'success',
    Marker: React.ElementType,
  ) => (
    <DescriptionListGroup>
      <DescriptionListTerm>{term}</DescriptionListTerm>
      <DescriptionListDescription>
        {items.map((c) => (
          <div key={c.name}>
            <Icon status={status} isInline>
              <Marker />
            </Icon>{' '}
            <ConsoleLink href={c.href} dir="auto">
              {c.title}
            </ConsoleLink>
          </div>
        ))}
      </DescriptionListDescription>
    </DescriptionListGroup>
  );

  return (
    <PageSection>
      {scanningDisabled(baseline) && (
        <Alert
          variant="info"
          isInline
          isLiveRegion
          title={t('Scanning is disabled')}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {t('No profiles are selected. Enable a profile to resume scanning.')}{' '}
          <ConsoleLink href="/baseline-security/profiles">{t('Go to Profiles')}</ConsoleLink>
        </Alert>
      )}
      {degraded && (
        <Alert
          variant="warning"
          isInline
          isLiveRegion
          title={t('Scanning degraded')}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {/* Condition messages are untrusted CR text and may be absent; a bare
              title told the admin nothing about what went wrong. */}
          {degraded.message ? (
            <span dir="auto">{degraded.message}</span>
          ) : (
            t('The operator could not complete a reconcile. Check the baseline status for details.')
          )}
        </Alert>
      )}
      {progressing && !degraded && (
        <Alert
          variant="info"
          isInline
          isLiveRegion
          title={t('Baseline is progressing')}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {progressing.message ? (
            <span dir="auto">{progressing.message}</span>
          ) : (
            t('Installing or configuring dependencies.')
          )}
        </Alert>
      )}
      {newlyFailed.length > 0 && (
        <Alert
          variant="danger"
          isInline
          isLiveRegion
          title={t('{{count}} check newly failing since the last scan', {
            // count must stay numeric for i18next plural selection; formattedCount
            // is the locale-aware display value in the translated string. It is
            // the resolved count, the same one the Recent changes card shows, so
            // the banner and the card cannot report two different totals. With
            // nothing linkable the resolved count is zero, and a "0 checks newly
            // failing" title on a banner that fired contradicts itself; fall back
            // to the operator's own count and say how many have no result left.
            count: newlyFailedItems.length || newlyFailed.length,
            formattedCount: formatCount(
              newlyFailedItems.length || newlyFailed.length,
              locale,
            ),
          })}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {/* Named checks, not a FAIL-only Results chip: newlyFailed tracks
              raw FAIL (including checks currently WAIVED for score), so a
              FAIL filter would hide waived regressions this alert counts. */}
          {newlyFailedItems.length > 0 ? (
            visibleNewlyFailed.map((c, i) => (
              <React.Fragment key={c.name}>
                {/* Locale list punctuation, not a literal ", ": de/fr want
                    "und"/"et" before the last item, ja/zh use no separator. */}
                {i > 0 && newlyFailedSeparators[i - 1]}
                {/* Check titles are untrusted CO text and may be RTL; dir=auto
                    keeps the surrounding punctuation and separators on the
                    right side of the title. */}
                <ConsoleLink href={c.href} dir="auto">
                  {c.title}
                </ConsoleLink>
              </React.Fragment>
            ))
          ) : (
            <ConsoleLink href="/baseline-security/results">{t('Review check results')}</ConsoleLink>
          )}
          {changesTruncated && newlyFailedItems.length > 0 && (
            <>
              {' '}
              {t('and {{formattedCount}} more', {
                formattedCount: formatCount(
                  newlyFailedItems.length - visibleNewlyFailed.length,
                  locale,
                ),
              })}
            </>
          )}
          {/* A name in status.newlyFailed with no current check result cannot be
              linked; say so instead of silently dropping it from the count. */}
          {unresolvedNewlyFailed > 0 && (
            <>
              {' '}
              {resultsUnreadable
                ? t('({{count}} unmatched: check results could not be read)', {
                    count: unresolvedNewlyFailed,
                    formattedCount: formatCount(unresolvedNewlyFailed, locale),
                  })
                : t('({{count}} no longer in the results)', {
                    count: unresolvedNewlyFailed,
                    formattedCount: formatCount(unresolvedNewlyFailed, locale),
                  })}
            </>
          )}
          {fixed.length > 0 && (
            <>
              {' '}
              {t('({{count}} fixed)', {
                // Same fallback as the title: an all-unresolved set would
                // otherwise read "(0 fixed)".
                count: fixedItems.length || fixed.length,
                formattedCount: formatCount(fixedItems.length || fixed.length, locale),
              })}
            </>
          )}
        </Alert>
      )}
      {expiring.length > 0 && (
        <Alert
          variant="warning"
          isInline
          isLiveRegion
          title={t('{{count}} waiver expiring within two weeks', {
            count: expiring.length,
            formattedCount: formatCount(expiring.length, locale),
          })}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {/* Locale list punctuation; the waiver name is admin-entered free
              text, so dir=auto keeps an RTL name's separators on its side. */}
          <span dir="auto">
            {formatList(
              expiring.map((w) => w.name),
              locale,
            )}
          </span>
          <div>
            <ConsoleLink href={resultsHref('WAIVED')}>{t('Review waived checks')}</ConsoleLink>
          </div>
        </Alert>
      )}
      <Gallery hasGutter minWidths={{ default: DASHBOARD_CARD_MIN_WIDTH }}>
        <Card>
          <CardTitle>{t('Compliance score')}</CardTitle>
          {/* Center the donut: the card stretches to the taller Details card in
              the row, so a top-anchored fixed-height donut leaves a big empty
              gap below. minHeight keeps the donut readable; flex-center balances
              any extra height. */}
          <CardBody
            style={{
              minHeight: DONUT_CARD_MIN_HEIGHT,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            {charts.status === 'failed' ? (
              <ChunkError error={charts.error} onRetry={() => setChartAttempt((n) => n + 1)} />
            ) : Charts === null ? (
              <Skeleton
                height={DONUT_SKELETON_HEIGHT}
                screenreaderText={t('Loading compliance data')}
              />
            ) : (
              <Charts.CompositionDonut
                score={score}
                segments={segments}
                locale={locale}
              />
            )}
          </CardBody>
        </Card>
        <Card>
          <CardTitle>{t('Details')}</CardTitle>
          <CardBody>
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Last scan')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {baseline.status?.lastScanTime ? (
                    <ClusterTimestamp
                      value={baseline.status.lastScanTime}
                      locale={locale}
                    />
                  ) : (
                    // Bare em dash is silent or read as "dash"; name the empty state.
                    <span aria-label={t('Not scanned')}>—</span>
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Next scan')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {scanningDisabled(baseline) ? (
                    <span aria-label={t('Scanning is disabled')}>—</span>
                  ) : baseline.status?.nextScanTime ? (
                    <ClusterTimestamp value={baseline.status.nextScanTime} locale={locale} />
                  ) : (
                    <span aria-label={t('n/a')}>—</span>
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Schedule')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {/* Empty/whitespace schedule is defaulted to DEFAULT_SCAN_SCHEDULE. */}
                  <ScheduleEditor baseline={baseline} />
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Compliance Operator')}</DescriptionListTerm>
                <DescriptionListDescription>{coLabel}</DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Scoring mode')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {scoringMode === 'SeverityWeighted'
                    ? t('Severity-weighted')
                    : t('Flat (equal weight)')}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Remediations')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <ConsoleLink href="/baseline-security/remediations">{t('Manage remediations')}</ConsoleLink>
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
        {historyChartData.length > 1 &&
          (charts.status === 'failed' ? (
            <Card>
              <CardTitle>{t('Score trend')}</CardTitle>
              <CardBody>
                <ChunkError error={charts.error} onRetry={() => setChartAttempt((n) => n + 1)} />
              </CardBody>
            </Card>
          ) : Charts === null ? (
            <Card>
              <CardTitle>{t('Score trend')}</CardTitle>
              <CardBody>
                <Skeleton
                  height={TREND_SKELETON_HEIGHT}
                  screenreaderText={t('Loading compliance data')}
                />
              </CardBody>
            </Card>
          ) : (
            <Charts.OverallTrendChart
              historyChartData={historyChartData}
              historyModeMismatch={historyModeMismatch}
              locale={locale}
              score={score}
            />
          ))}
        <Card>
          <CardTitle>{t('Recent changes')}</CardTitle>
          <CardBody>
            {newlyFailedItems.length === 0 && fixedItems.length === 0 ? (
              <EmptyState
                titleText={
                  // A scan can report a regression whose check result is already
                  // gone. Claiming "no changes" there contradicts the banner
                  // above, which counts it.
                  unresolvedNewlyFailed > 0
                    ? resultsUnreadable
                      ? t('{{count}} newly failing checks could not be matched to results', {
                          count: unresolvedNewlyFailed,
                          formattedCount: formatCount(unresolvedNewlyFailed, locale),
                        })
                      : t('{{count}} newly failing check is no longer in the results', {
                          count: unresolvedNewlyFailed,
                          formattedCount: formatCount(unresolvedNewlyFailed, locale),
                        })
                    : hasPriorScan
                      ? t('No changes since the last scan')
                      : t('No previous scan to compare yet')
                }
                headingLevel="h2"
              >
                <EmptyStateBody>
                  {unresolvedNewlyFailed > 0
                    ? resultsUnreadable
                      ? t(
                          'Check results could not be read, so the count on the banner above could not be matched to a result. It stays until the list loads again.',
                        )
                      : t(
                          'The rule was removed or its profile unbound, so there is no result to open. The count stays on the banner above until the next scan.',
                        )
                    : hasPriorScan
                      ? t('Fail and fix deltas will appear here after the next completed scan.')
                      : t('Recent changes appear after two completed scans.')}
                </EmptyStateBody>
              </EmptyState>
            ) : (
              // Scrollable region is keyboard-focusable (same pattern as Remediations table).
              <div
                style={{ maxHeight: CHANGES_MAX_HEIGHT, overflow: 'auto' }}
                tabIndex={0}
                role="region"
                aria-label={t('Recent changes')}
                {...regionFocusProps}
              >
                <DescriptionList isCompact>
                  {newlyFailedItems.length > 0 &&
                    changeList(
                      t('Newly failing ({{formattedCount}})', {
                        formattedCount: formatCount(newlyFailedItems.length, locale),
                      }),
                      visibleNewlyFailed,
                      'danger',
                      ExclamationCircleIcon,
                    )}
                  {fixedItems.length > 0 &&
                    changeList(
                      t('Fixed ({{formattedCount}})', {
                        formattedCount: formatCount(fixedItems.length, locale),
                      }),
                      visibleFixed,
                      'success',
                      CheckCircleIcon,
                    )}
                </DescriptionList>
              </div>
            )}
            {changesTruncated && (
              <Button
                variant="link"
                isInline
                onClick={() => {
                  setShowAllChanges(true);
                }}
              >
                {t('Show all changes')}
              </Button>
            )}
            {showAllChanges &&
              (newlyFailedItems.length > CHANGES_RENDER_LIMIT ||
                fixedItems.length > CHANGES_RENDER_LIMIT) && (
                <Button
                  variant="link"
                  isInline
                  onClick={() => {
                    setShowAllChanges(false);
                  }}
                >
                  {t('Show fewer changes')}
                </Button>
              )}
          </CardBody>
        </Card>
      </Gallery>
      {/* Per-profile score cards in their own row so they stay uniform height
          instead of stretching to match the tall donut/details/trend cards. */}
      <Gallery
        hasGutter
        minWidths={{ default: SCORE_CARD_MIN_WIDTH }}
        style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
      >
        {(baseline.status?.profiles ?? []).map((p) =>
          scoreCard(p.key, t(profileTitle(p.key)), p, p.history),
        )}
        {(baseline.status?.tailoredProfiles ?? []).map((tp) =>
          scoreCard(
            `tp-${tp.name}`,
            <>
              {tp.name} <Label isCompact color="blue">{t('Tailored')}</Label>
            </>,
            tp,
            tp.history,
          ),
        )}
      </Gallery>
    </PageSection>
  );
};

export default Overview;
