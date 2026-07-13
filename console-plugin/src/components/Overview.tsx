import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sPatch,
  Timestamp,
  useAccessReview,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Chart,
  ChartArea,
  ChartAxis,
  ChartDonut,
} from '@patternfly/react-charts/victory';
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
  ClusterBaselineModel,
  ComplianceCheckResult,
  DEFAULT_SCAN_SCHEDULE,
  profileTitle,
  ResultCounts,
  ScoreSnapshot,
  suiteFilterKey,
} from '../models';
import { isValidCron } from '../cron';
import { formatChartDate, formatCount, safeLocale } from '../dates';
import { errorMessage } from '../errors';
import { resultsHref } from '../links';
import { resourceVersionTest, schedulePatch } from '../patches';
import { changedChecksMany } from '../results';
import {
  aggregateCounts,
  effectiveScoringMode,
  historyScoringModeMismatch,
  profileScore,
  scoreLabelColor,
} from '../scoring';
import {
  activeWaivedNames,
  expiringWaivers,
  futureWaiverDeadlineMs,
  soonestDeadlineDelayMs,
} from '../waivers';
import BaselineNotConfigured from './BaselineNotConfigured';
import { regionFocusProps } from './DisabledTip';
import { SUCCESS_DISMISS_MS } from './feedback';

// Stable empty list so optional status arrays do not allocate each render.
const EMPTY_NAMES: readonly string[] = [];
const EMPTY_RESULTS: ComplianceCheckResult[] = [];
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

// Donut segment colors (module-level so CCR churn does not rebind CSS var strings).
const DONUT_GREEN = 'var(--pf-t--global--icon--color--status--success--default)';
const DONUT_RED = 'var(--pf-t--global--icon--color--status--danger--default)';
const DONUT_ORANGE = 'var(--pf-t--global--icon--color--status--warning--default)';
const DONUT_PURPLE = 'var(--pf-t--global--icon--color--status--custom--default)';
const DONUT_GREY = 'var(--pf-t--global--icon--color--disabled)';
const DONUT_BLUE = 'var(--pf-t--global--icon--color--status--info--default)';
// Distinct hues so Error is not confused with Fail, nor Waived with N/A.
const DONUT_ORANGERED = 'var(--pf-t--global--color--nonstatus--orangered--default)';
const DONUT_TEAL = 'var(--pf-t--global--color--nonstatus--teal--default)';

// Inline editor for spec.schedule in the Details card, gated on patch permission.
const ScheduleEditor: React.FC<{ baseline: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const current = (baseline.spec.schedule ?? '').trim() || DEFAULT_SCAN_SCHEDULE;
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
  React.useEffect(() => {
    if (!saved) return;
    const id = window.setTimeout(() => setSaved(false), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [saved]);
  const [canEdit, canEditLoading] = useAccessReview({
    group: 'baselinesecurity.openshift.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
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
          {canEdit && !canEditLoading && (
            <SplitItem>
              <Button
                ref={editButtonRef}
                variant="link"
                isInline
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
              </Button>
            </SplitItem>
          )}
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
    // Presence is != null (not !!): empty string is still a present field.
    // Empty schedule ops would leave only an RV test: a successful no-op that
    // looks like the schedule was updated when nothing changed.
    const scheduleOps = schedulePatch(baseline.spec.schedule != null, value.trim());
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
        <FlexItem grow={{ default: 'grow' }} style={{ minWidth: 160 }}>
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
      {err && <Alert variant="danger" isInline isLiveRegion title={err} style={{ marginTop: 4 }} />}
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
      <a
        href={href}
        // Named for screen readers: "Fail: 5", not a bare number (WCAG 2.4.4).
        aria-label={t('{{label}}: {{value}}', { label, value: countText })}
        style={{ color: 'inherit', textDecoration: 'none', display: 'block' }}
      >
        {row}
      </a>
    );
  }
  return row;
};

// Compact score sparkline for a profile card from its history (>=2 points).
// History snapshots to Victory {x: Date, y: score} points.
// Drop points with an unparseable time or non-finite score: a single bad
// snapshot otherwise makes Victory's time-scale domain NaN and silently blanks
// the whole chart (hand-edited / partial status can carry missing scores).
const toTrendData = (history?: ScoreSnapshot[]) =>
  (history ?? [])
    .map((h) => ({ x: new Date(h.time), y: h.score }))
    .filter(
      (p) =>
        !Number.isNaN(p.x.getTime()) &&
        typeof p.y === 'number' &&
        Number.isFinite(p.y),
    );

// Content key for history rings: status-only CR updates reallocate the array
// with the same points; identity deps would rebuild Victory Date/path data on
// every reconcile even when the trend did not change (max 30 snapshots).
const historyContentKey = (history?: ScoreSnapshot[]): string => {
  if (!history?.length) {
    return '';
  }
  let key = '';
  for (let i = 0; i < history.length; i++) {
    const h = history[i];
    if (i > 0) {
      key += '\x01';
    }
    key += `${h.time ?? ''}\0${h.score ?? ''}`;
  }
  return key;
};

// Memoized composition donut: CCR list-watch identity changes re-render Overview
// without changing score/totals. Victory re-layout of the donut is expensive
// relative to a shallow prop check when counts are content-stable.
type DonutSegment = {
  label: string;
  value: number;
  color: string;
  filter: string;
};
const CompositionDonut = React.memo<{
  score: number | null | undefined;
  totalChecks: number;
  donutData: { x: string; y: number }[];
  donutColors: string[];
  segments: readonly DonutSegment[];
  locale: string | undefined;
}>(({ score, totalChecks, donutData, donutColors, segments, locale }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  if (totalChecks === 0) {
    return (
      <ChartDonut
        ariaTitle={t('Compliance score')}
        ariaDesc={t('No check results yet. Score is unavailable until a scan completes.')}
        // Static: score composition is status data, not a motion cue (WCAG 2.3.3).
        animate={false}
        data={[{ x: t('No results'), y: 1 }]}
        colorScale={[DONUT_GREY]}
        title={score != null ? formatCount(score, locale) : '—'}
        subTitle={t('of 100')}
        height={200}
        width={300}
        constrainToVisibleArea
      />
    );
  }
  return (
    // Wrap on narrow viewports so the legend is not clipped beside a fixed-width donut.
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
      <ChartDonut
        ariaTitle={t('Check results')}
        ariaDesc={t('Compliance score {{score}} of 100. Composition of compliance check results.', {
          score: score != null ? formatCount(score, locale) : t('unavailable'),
        })}
        animate={false}
        constrainToVisibleArea
        data={donutData}
        colorScale={donutColors}
        labels={({ datum }) =>
          t('{{label}}: {{value}}', {
            label: datum.x,
            value: formatCount(Number(datum.y), locale),
          })
        }
        title={score != null ? formatCount(score, locale) : '—'}
        subTitle={t('of 100')}
        height={200}
        width={200}
        padding={{ top: 10, bottom: 10, left: 10, right: 10 }}
      />
      {/* HTML legend: Victory's built-in legend clips long labels and
          caps at a fixed width; a plain list wraps and never truncates. */}
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, minWidth: 0 }}>
        {segments.map((s) => {
          const num = formatCount(s.value, locale);
          const text = t('{{label}} ({{num}})', { label: s.label, num });
          return (
            <li
              key={s.label}
              style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '2px 0' }}
            >
              <span
                aria-hidden
                style={{
                  width: 12,
                  height: 12,
                  flex: '0 0 auto',
                  borderRadius: 2,
                  backgroundColor: s.color,
                }}
              />
              {/* Same deep-link as profile CountRow so the primary
                  score card drills into Results on click/tap (touch
                  devices cannot hover Victory slices). */}
              <a
                href={resultsHref(s.filter)}
                aria-label={t('{{label}}: {{value}}', { label: s.label, value: num })}
              >
                {text}
              </a>
            </li>
          );
        })}
      </ul>
    </div>
  );
});
CompositionDonut.displayName = 'CompositionDonut';

// Memoized overall trend chart: same CCR-churn stability as MiniTrend.
const OverallTrendChart = React.memo<{
  historyChartData: { x: Date; y: number }[];
  historyModeMismatch: boolean;
  locale: string | undefined;
}>(({ historyChartData, historyModeMismatch, locale }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  return (
    <Card>
      <CardTitle>{t('Score trend')}</CardTitle>
      <CardBody style={{ height: historyModeMismatch ? 260 : 230 }}>
        {historyModeMismatch && (
          <HelperText style={{ marginBottom: 'var(--pf-t--global--spacer--sm)' }}>
            <HelperTextItem variant="warning">
              {t(
                'Scoring mode changed since some history points were recorded. Older points may not be comparable; the next completed scan starts a fresh trend under the new mode.',
              )}
            </HelperTextItem>
          </HelperText>
        )}
        <Chart
          ariaTitle={t('Score trend')}
          ariaDesc={t('Score moved from {{from}} to {{to}} over {{scans}} scans.', {
            from: formatCount(historyChartData[0].y, locale),
            to: formatCount(historyChartData[historyChartData.length - 1].y, locale),
            scans: formatCount(historyChartData.length, locale),
          })}
          // Static trend: motion does not add meaning and can delay reading (WCAG 2.3.3).
          animate={false}
          height={200}
          width={300}
          padding={{ top: 10, bottom: 40, left: 40, right: 20 }}
          domain={{ y: [0, 100] }}
          // Time scale so ticks are spaced by date (a few, deduplicated),
          // not one categorical tick per snapshot with repeated labels.
          scale={{ x: 'time', y: 'linear' }}
        >
          <ChartAxis
            tickFormat={(x: Date) => formatChartDate(x, locale)}
            fixLabelOverlap
          />
          <ChartAxis dependentAxis tickFormat={(y: number) => formatCount(y, locale)} />
          <ChartArea data={historyChartData} />
        </Chart>
      </CardBody>
    </Card>
  );
});
OverallTrendChart.displayName = 'OverallTrendChart';

// Memoized: CCR watch churn re-renders Overview without touching history.
const MiniTrend = React.memo<{ history?: ScoreSnapshot[] }>(({ history }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  // Content-stable: status CR updates reallocate history with the same points.
  const histKey = historyContentKey(history);
  const chartData = React.useMemo(
    () => toTrendData(history),
    // history is read when histKey changes (same points => skip Date rebuild).
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key
    [histKey],
  );
  if (chartData.length < 2) {
    return null;
  }
  const first = chartData[0].y;
  const last = chartData[chartData.length - 1].y;
  return (
    <div style={{ height: 40, marginTop: 'var(--pf-t--global--spacer--sm)' }}>
      <Chart
        ariaTitle={t('Score trend')}
        ariaDesc={t('Score moved from {{from}} to {{to}} over {{scans}} scans.', {
          from: formatCount(first, i18n.language),
          to: formatCount(last, i18n.language),
          scans: formatCount(chartData.length, i18n.language),
        })}
        animate={false}
        height={40}
        padding={{ top: 4, bottom: 4, left: 0, right: 0 }}
        minDomain={{ y: 0 }}
        maxDomain={{ y: 100 }}
        scale={{ x: 'time', y: 'linear' }}
      >
        <ChartArea data={chartData} />
      </Chart>
    </div>
  );
});
MiniTrend.displayName = 'MiniTrend';

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
  // Shared from CompliancePage (single watch); used for Recent changes titles
  // and SeverityWeighted per-profile scores.
  checkResults?: ComplianceCheckResult[];
}> = ({ baseline, loaded, checkResults }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  // One BCP 47 tag for all score/count formatting (same path as report / dates).
  const locale = safeLocale(i18n.language);

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
  const waiversKey = (waivers ?? [])
    .map((w) => `${w.name ?? ''}\0${w.expiresAt ?? ''}`)
    .join('\x01');
  // Active waivers are time-sensitive: membership alone is not enough. A waiver
  // can expire (or enter the expiring-soon window) with no CR edit. Without a
  // tick, SeverityWeighted profile badges and the expiring-soon alert stay
  // wrong until CCR identity or waiversKey change. ResultsTab clocks expiry
  // only; Overview also clocks window entry for the 2-week alert.
  const [waiverClock, setWaiverClock] = React.useState(0);
  React.useEffect(() => {
    const now = Date.now();
    // Expiry drops score exclusions; -2w marks entry into the expiring-soon alert.
    const delay = soonestDeadlineDelayMs(
      now,
      futureWaiverDeadlineMs(waivers, now, [-2 * WEEK_MS]),
    );
    if (delay === 0) {
      return;
    }
    const id = window.setTimeout(() => setWaiverClock((c) => c + 1), delay);
    return () => window.clearTimeout(id);
    // waivers read when key or clock changes (content-stable + expiry).
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key
  }, [waiversKey, waiverClock]);
  // Last history tip per bucket (empty-CCR fallback in profileScore only).
  const statusProfiles = baseline?.status?.profiles;
  const statusTailored = baseline?.status?.tailoredProfiles;
  const profileHistKey = (() => {
    let key = '';
    for (const p of statusProfiles ?? []) {
      const h = p.history;
      const last = h && h.length > 0 ? h[h.length - 1] : undefined;
      key += `${p.key}\0${last?.score ?? ''}\x01`;
    }
    for (const tp of statusTailored ?? []) {
      const h = tp.history;
      const last = h && h.length > 0 ? h[h.length - 1] : undefined;
      key += `tp-${tp.name}\0${last?.score ?? ''}\x01`;
    }
    return key;
  })();

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
  }, [resultsByKey, waiversKey, profileHistKey, waiverClock]);

  // Hooks must run before early returns. Resolve titles from the watched CCR
  // list; one index pass for newlyFailed + fixed (no dual full-list scan).
  // Content keys: status updates reallocate these arrays with the same names.
  const newlyFailed = baseline?.status?.newlyFailed ?? EMPTY_NAMES;
  const fixed = baseline?.status?.fixed ?? EMPTY_NAMES;
  const newlyFailedKey = newlyFailed.join('\0');
  const fixedKey = fixed.join('\0');
  const recentChanges = React.useMemo(
    () => changedChecksMany([newlyFailed, fixed], checkResults),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys
    [newlyFailedKey, fixedKey, checkResults],
  );
  const newlyFailedItems = recentChanges[0];
  const fixedItems = recentChanges[1];

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
  // Content key: pass/fail/etc counts; identity of status.profiles flaps every
  // status update even when rollup numbers are unchanged.
  const countsKey = (() => {
    let key = '';
    for (const p of statusProfiles ?? []) {
      key += `${p.key}\0${p.pass ?? 0}\0${p.fail ?? 0}\0${p.manual ?? 0}\0${p.info ?? 0}\0${p.error ?? 0}\0${p.inconsistent ?? 0}\0${p.waived ?? 0}\0${p.notApplicable ?? 0}\x01`;
    }
    for (const tp of statusTailored ?? []) {
      key += `tp-${tp.name}\0${tp.pass ?? 0}\0${tp.fail ?? 0}\0${tp.manual ?? 0}\0${tp.info ?? 0}\0${tp.error ?? 0}\0${tp.inconsistent ?? 0}\0${tp.waived ?? 0}\0${tp.notApplicable ?? 0}\x01`;
    }
    return key;
  })();
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
      { label: t('Info'), value: totals.info, color: DONUT_BLUE, filter: 'INFO' },
      { label: t('Inconsistent'), value: totals.inconsistent, color: DONUT_PURPLE, filter: 'INCONSISTENT' },
      { label: t('Error'), value: totals.error, color: DONUT_ORANGERED, filter: 'ERROR' },
      { label: t('Waived'), value: totals.waived, color: DONUT_TEAL, filter: 'WAIVED' },
      { label: t('Not applicable'), value: totals.notApplicable, color: DONUT_GREY, filter: 'NOT-APPLICABLE' },
    ].filter((s) => s.value > 0);
  }, [totals, t]);
  const totalChecks = React.useMemo(
    () => segments.reduce((n, s) => n + s.value, 0),
    [segments],
  );
  const donutData = React.useMemo(
    () => segments.map((s) => ({ x: s.label, y: s.value })),
    [segments],
  );
  const donutColors = React.useMemo(() => segments.map((s) => s.color), [segments]);

  if (!loaded) {
    return (
      <PageSection>
        <Gallery hasGutter minWidths={{ default: '300px' }}>
          {[0, 1, 2].map((i) => (
            <Card key={i}>
              <CardBody>
                <Skeleton height="180px" screenreaderText={t('Loading compliance data')} />
              </CardBody>
            </Card>
          ))}
        </Gallery>
      </PageSection>
    );
  }
  if (!baseline) {
    return (
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
  const score = baseline.status?.score;

  const expiring = expiringWaivers(baseline.spec.waivers, 2 * WEEK_MS);
  // Prefer status.diffBaseScanTime (set once a prior completed scan exists for
  // regression diff). History length alone is wrong when the first scan had no
  // countable score (history stays short) but a second scan already compared.
  const hasPriorScan =
    !!baseline.status?.diffBaseScanTime || (baseline.status?.history?.length ?? 0) > 1;
  const scanningDisabled =
    (baseline.spec.profiles?.length ?? 0) === 0 &&
    (baseline.spec.tailoredProfiles?.length ?? 0) === 0;

  // Compact score chip shown as a per-profile card action (built-in + tailored).
  const scoreLabel = (pScore: number | null) =>
    pScore != null ? (
      <Label
        isCompact
        color={scoreLabelColor(pScore)}
        aria-label={t('Compliance score {{score}} of 100', {
          score: formatCount(pScore, locale),
        })}
      >
        {formatCount(pScore, locale)}
      </Label>
    ) : undefined;

  return (
    <PageSection>
      {scanningDisabled && (
        <Alert
          variant="info"
          isInline
          isLiveRegion
          title={t('Scanning is disabled')}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {t('No profiles are selected. Enable a profile to resume scanning.')}{' '}
          <a href="/baseline-security/profiles">{t('Go to Profiles')}</a>
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
          {degraded.message}
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
          {progressing.message || t('Installing or configuring dependencies.')}
        </Alert>
      )}
      {newlyFailed.length > 0 && (
        <Alert
          variant="danger"
          isInline
          isLiveRegion
          title={t('{{count}} check newly failing since the last scan', {
            // count must stay numeric for i18next plural selection; formattedCount
            // is the locale-aware display value in the translated string.
            count: newlyFailed.length,
            formattedCount: formatCount(newlyFailed.length, locale),
          })}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {/* Unfiltered Results: newlyFailed tracks raw FAIL (including checks
              that are currently WAIVED for score), so a FAIL-only chip would
              hide waived regressions the alert is counting. */}
          <a href="/baseline-security/results">{t('Review failing checks')}</a>
          {fixed.length > 0 && (
            <>
              {' '}
              {t('({{count}} fixed)', {
                count: fixed.length,
                formattedCount: formatCount(fixed.length, locale),
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
          {expiring.map((w) => w.name).join(', ')}
          <div>
            <a href={resultsHref('WAIVED')}>{t('Review waived checks')}</a>
          </div>
        </Alert>
      )}
      <Gallery hasGutter minWidths={{ default: '300px' }}>
        <Card>
          <CardTitle>{t('Compliance score')}</CardTitle>
          <CardBody style={{ height: 260 }}>
            <CompositionDonut
              score={score}
              totalChecks={totalChecks}
              donutData={donutData}
              donutColors={donutColors}
              segments={segments}
              locale={locale}
            />
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
                    <Timestamp timestamp={baseline.status.lastScanTime} />
                  ) : (
                    // Bare em dash is silent or read as "dash"; name the empty state.
                    <span aria-label={t('Not scanned')}>—</span>
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Next scan')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {baseline.status?.nextScanTime ? (
                    <Timestamp timestamp={baseline.status.nextScanTime} />
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
                  <a href="/baseline-security/remediations">{t('Manage remediations')}</a>
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
        {historyChartData.length > 1 && (
          <OverallTrendChart
            historyChartData={historyChartData}
            historyModeMismatch={historyModeMismatch}
            locale={locale}
          />
        )}
        <Card>
          <CardTitle>{t('Recent changes')}</CardTitle>
          <CardBody>
            {newlyFailedItems.length === 0 && fixedItems.length === 0 ? (
              <EmptyState
                titleText={
                  hasPriorScan
                    ? t('No changes since the last scan')
                    : t('No previous scan to compare yet')
                }
                headingLevel="h2"
              >
                <EmptyStateBody>
                  {hasPriorScan
                    ? t('Fail and fix deltas will appear here after the next completed scan.')
                    : t('Run a scan, then rescan later to see newly failing and fixed checks.')}
                </EmptyStateBody>
              </EmptyState>
            ) : (
              // Scrollable region is keyboard-focusable (same pattern as Remediations table).
              <div
                style={{ maxHeight: 260, overflow: 'auto' }}
                tabIndex={0}
                role="region"
                aria-label={t('Recent changes')}
                {...regionFocusProps}
              >
                <DescriptionList isCompact>
                  {newlyFailedItems.length > 0 && (
                    <DescriptionListGroup>
                      <DescriptionListTerm>
                        {t('Newly failing ({{count}})', {
                          count: newlyFailedItems.length,
                          formattedCount: formatCount(newlyFailedItems.length, locale),
                        })}
                      </DescriptionListTerm>
                      <DescriptionListDescription>
                        {newlyFailedItems.map((c) => (
                          <div key={c.name}>
                            <Icon status="danger" isInline>
                              <ExclamationCircleIcon />
                            </Icon>{' '}
                            <a href={c.href}>{c.title}</a>
                          </div>
                        ))}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  )}
                  {fixedItems.length > 0 && (
                    <DescriptionListGroup>
                      <DescriptionListTerm>
                        {t('Fixed ({{count}})', {
                          count: fixedItems.length,
                          formattedCount: formatCount(fixedItems.length, locale),
                        })}
                      </DescriptionListTerm>
                      <DescriptionListDescription>
                        {fixedItems.map((c) => (
                          <div key={c.name}>
                            <Icon status="success" isInline>
                              <CheckCircleIcon />
                            </Icon>{' '}
                            <a href={c.href}>{c.title}</a>
                          </div>
                        ))}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  )}
                </DescriptionList>
              </div>
            )}
          </CardBody>
        </Card>
      </Gallery>
      {/* Per-profile score cards in their own row so they stay uniform height
          instead of stretching to match the tall donut/details/trend cards. */}
      <Gallery
        hasGutter
        minWidths={{ default: '260px' }}
        style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
      >
        {(baseline.status?.profiles ?? []).map((p) => {
          // Flat: counts only. SeverityWeighted: memoized weightedScores map.
          const pScore = weightedScores
            ? (weightedScores.get(p.key) ?? null)
            : profileScore(p);
          return (
            <Card key={p.key}>
              <CardHeader
                actions={{
                  actions: scoreLabel(pScore),
                  hasNoOffset: true,
                }}
              >
                <CardTitle>{t(profileTitle(p.key))}</CardTitle>
              </CardHeader>
              <CardBody>
                <ProfileCounts counts={p} filterKey={p.key} />
                <MiniTrend history={p.history} />
              </CardBody>
            </Card>
          );
        })}
        {(baseline.status?.tailoredProfiles ?? []).map((tp) => {
          const tpKey = `tp-${tp.name}`;
          const pScore = weightedScores
            ? (weightedScores.get(tpKey) ?? null)
            : profileScore(tp);
          return (
            <Card key={`tp-${tp.name}`}>
              <CardHeader
                actions={{
                  actions: scoreLabel(pScore),
                  hasNoOffset: true,
                }}
              >
                <CardTitle>
                  {tp.name} <Label isCompact color="blue">{t('Tailored')}</Label>
                </CardTitle>
              </CardHeader>
              <CardBody>
                <ProfileCounts counts={tp} filterKey={`tp-${tp.name}`} />
                <MiniTrend history={tp.history} />
              </CardBody>
            </Card>
          );
        })}
      </Gallery>
    </PageSection>
  );
};

export default Overview;
