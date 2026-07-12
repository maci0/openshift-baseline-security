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
import { formatCount, safeLocale } from '../dates';
import { errorMessage } from '../errors';
import { resultsHref } from '../links';
import { resourceVersionTest, schedulePatch } from '../patches';
import { changedChecks } from '../results';
import {
  aggregateCounts,
  effectiveScoringMode,
  historyScoringModeMismatch,
  profileScore,
} from '../scoring';
import { activeWaivedNames, expiringWaivers } from '../waivers';

// Stable empty list so optional status arrays do not allocate each render.
const EMPTY_NAMES: readonly string[] = [];
const EMPTY_RESULTS: ComplianceCheckResult[] = [];

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
  // Auto-clear "Schedule updated" so success feedback does not stick forever.
  React.useEffect(() => {
    if (!saved) return;
    const id = window.setTimeout(() => setSaved(false), 5000);
    return () => window.clearTimeout(id);
  }, [saved]);
  const [canEdit, canEditLoading] = useAccessReview({
    group: 'baselinesecurity.io',
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
      <Split hasGutter>
        <SplitItem isFilled>
          <TextInput
            ref={inputRef}
            id="schedule-cron"
            aria-label={t('Schedule')}
            aria-invalid={!valid}
            aria-describedby="schedule-cron-help"
            value={value}
            onChange={(_e, v) => setValue(v)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                void save();
              } else if (e.key === 'Escape') {
                e.preventDefault();
                cancelEdit();
              }
            }}
            validated={valid ? 'default' : 'error'}
          />
        </SplitItem>
        <SplitItem>
          <Button variant="primary" isInline isDisabled={!valid || busy} isLoading={busy} onClick={() => void save()}>
            {t('Save')}
          </Button>
        </SplitItem>
        <SplitItem>
          <Button variant="link" isInline isDisabled={busy} onClick={cancelEdit}>
            {t('Cancel')}
          </Button>
        </SplitItem>
      </Split>
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
  return (
    <Flex gap={{ default: 'gapSm' }} alignItems={{ default: 'alignItemsCenter' }}>
      <FlexItem>
        <Icon status={status} size="sm">
          {icon}
        </Icon>
      </FlexItem>
      <FlexItem grow={{ default: 'grow' }}>{label}</FlexItem>
      <FlexItem>
        {href && count > 0 ? (
          // Link text is only the number; name it so screen readers announce
          // the localized "Fail: 5" pattern, not a context-free "5" (WCAG 2.4.4).
          <a href={href} aria-label={t('{{label}}: {{value}}', { label, value: countText })}>
            {countText}
          </a>
        ) : (
          countText
        )}
      </FlexItem>
    </Flex>
  );
};

// Compact score sparkline for a profile card from its history (>=2 points).
const MiniTrend: React.FC<{ history?: ScoreSnapshot[] }> = ({ history }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  if (!history || history.length < 2) {
    return null;
  }
  return (
    <div style={{ height: 40, marginTop: 'var(--pf-t--global--spacer--sm)' }}>
      <Chart
        ariaTitle={t('Score trend')}
        height={40}
        padding={{ top: 4, bottom: 4, left: 0, right: 0 }}
        minDomain={{ y: 0 }}
        maxDomain={{ y: 100 }}
        scale={{ x: 'time', y: 'linear' }}
      >
        <ChartArea data={history.map((h) => ({ x: new Date(h.time), y: h.score }))} />
      </Chart>
    </div>
  );
};

// Per-profile status rows for a score card. Pass/Fail always show; the rest
// (Manual, Info, Inconsistent, Error, Waived, N/A) show only when non-zero, so
// a card's rows match the statuses the composition donut aggregates.
const ProfileCounts: React.FC<{ counts: ResultCounts; filterKey: string }> = ({
  counts,
  filterKey,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const rows: Array<{
    k: keyof ResultCounts;
    f: string;
    label: string;
    icon: React.ReactElement;
    status: React.ComponentProps<typeof Icon>['status'];
    always?: boolean;
  }> = [
    { k: 'pass', f: 'PASS', label: t('Pass'), icon: <CheckCircleIcon />, status: 'success', always: true },
    { k: 'fail', f: 'FAIL', label: t('Fail'), icon: <ExclamationCircleIcon />, status: 'danger', always: true },
    { k: 'manual', f: 'MANUAL', label: t('Manual'), icon: <ExclamationTriangleIcon />, status: 'warning' },
    { k: 'info', f: 'INFO', label: t('Info'), icon: <InfoCircleIcon />, status: 'info' },
    { k: 'inconsistent', f: 'INCONSISTENT', label: t('Inconsistent'), icon: <ExclamationTriangleIcon />, status: 'custom' },
    { k: 'error', f: 'ERROR', label: t('Error'), icon: <ExclamationCircleIcon />, status: 'danger' },
    { k: 'waived', f: 'WAIVED', label: t('Waived'), icon: <MinusCircleIcon />, status: undefined },
    { k: 'notApplicable', f: 'NOT-APPLICABLE', label: t('Not applicable'), icon: <MinusCircleIcon />, status: undefined },
  ];
  return (
    <>
      {rows
        .filter((r) => r.always || (counts[r.k] ?? 0) > 0)
        .map((r) => (
          <CountRow
            key={r.k}
            icon={r.icon}
            status={r.status}
            label={r.label}
            count={counts[r.k] ?? 0}
            href={resultsHref(r.f, filterKey)}
          />
        ))}
    </>
  );
};

// PatternFly Label color for a profile score, using the same 60/90 thresholds
// as scoreColor (which returns CSS vars, not Label color tokens).
const scoreLabelColor = (score: number): 'green' | 'orange' | 'red' =>
  score >= 90 ? 'green' : score >= 60 ? 'orange' : 'red';

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
      const key = suiteFilterKey(r.metadata.labels);
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

  // One waiver Set + one score pass for all cards (avoids N Set builds and
  // re-scoring every Overview re-render during CCR watch churn).
  const weightedScores = React.useMemo(() => {
    if (!resultsByKey) {
      return null;
    }
    const waived = activeWaivedNames(baseline?.spec.waivers);
    const scores = new Map<string, number | null>();
    for (const p of baseline?.status?.profiles ?? []) {
      scores.set(
        p.key,
        profileScore(p, {
          mode: 'SeverityWeighted',
          filterKey: p.key,
          results: resultsByKey.get(p.key) ?? EMPTY_RESULTS,
          activeWaived: waived,
        }),
      );
    }
    for (const tp of baseline?.status?.tailoredProfiles ?? []) {
      const key = `tp-${tp.name}`;
      scores.set(
        key,
        profileScore(tp, {
          mode: 'SeverityWeighted',
          filterKey: key,
          results: resultsByKey.get(key) ?? EMPTY_RESULTS,
          activeWaived: waived,
        }),
      );
    }
    return scores;
  }, [
    resultsByKey,
    baseline?.spec.waivers,
    baseline?.status?.profiles,
    baseline?.status?.tailoredProfiles,
  ]);

  // Hooks must run before early returns. Resolve titles from the watched CCR
  // list; changedChecks early-exits on empty names and only indexes requested
  // check names (not every CCR).
  const newlyFailed = baseline?.status?.newlyFailed ?? EMPTY_NAMES;
  const fixed = baseline?.status?.fixed ?? EMPTY_NAMES;
  const newlyFailedItems = React.useMemo(
    () => changedChecks(newlyFailed, checkResults),
    [newlyFailed, checkResults],
  );
  const fixedItems = React.useMemo(
    () => changedChecks(fixed, checkResults),
    [fixed, checkResults],
  );

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
        <EmptyState titleText={t('Baseline not configured')} headingLevel="h2">
          <EmptyStateBody>
            {t(
              'No ClusterBaseline resource found. Install the baseline-security operator and create a ClusterBaseline to start scanning.',
            )}
          </EmptyStateBody>
        </EmptyState>
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

  // Aggregate per-status counts across built-in AND tailored profiles for the
  // composition donut, so the slices match the score (which includes both) and
  // failing/manual checks are shown as distinct slices.
  const totals = aggregateCounts(
    ...(baseline.status?.profiles ?? []),
    ...(baseline.status?.tailoredProfiles ?? []),
  );
  const green = 'var(--pf-t--global--icon--color--status--success--default)';
  const red = 'var(--pf-t--global--icon--color--status--danger--default)';
  const orange = 'var(--pf-t--global--icon--color--status--warning--default)';
  const purple = 'var(--pf-t--global--icon--color--status--custom--default)';
  const grey = 'var(--pf-t--global--icon--color--disabled)';
  const blue = 'var(--pf-t--global--icon--color--status--info--default)';
  // Distinct hues so Error is not confused with Fail, nor Waived with N/A.
  const orangered = 'var(--pf-t--global--color--nonstatus--orangered--default)';
  const teal = 'var(--pf-t--global--color--nonstatus--teal--default)';
  const segments = [
    { label: t('Pass'), value: totals.pass, color: green },
    { label: t('Fail'), value: totals.fail, color: red },
    { label: t('Manual'), value: totals.manual, color: orange },
    { label: t('Info'), value: totals.info, color: blue },
    { label: t('Inconsistent'), value: totals.inconsistent, color: purple },
    { label: t('Error'), value: totals.error, color: orangered },
    { label: t('Waived'), value: totals.waived, color: teal },
    { label: t('Not applicable'), value: totals.notApplicable, color: grey },
  ].filter((s) => s.value > 0);
  const totalChecks = segments.reduce((n, s) => n + s.value, 0);

  const WEEK = 7 * 24 * 60 * 60 * 1000;
  const expiring = expiringWaivers(baseline.spec.waivers, 2 * WEEK);
  // Prefer status.diffBaseScanTime (set once a prior completed scan exists for
  // regression diff). History length alone is wrong when the first scan had no
  // countable score (history stays short) but a second scan already compared.
  const hasPriorScan =
    !!baseline.status?.diffBaseScanTime || (baseline.status?.history?.length ?? 0) > 1;
  const scanningDisabled =
    (baseline.spec.profiles?.length ?? 0) === 0 &&
    (baseline.spec.tailoredProfiles?.length ?? 0) === 0;

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
          <a href={resultsHref('FAIL')}>{t('Review failing checks')}</a>
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
            {totalChecks === 0 ? (
              <ChartDonut
                ariaTitle={t('Compliance score')}
                ariaDesc={t('No check results yet. Score is unavailable until a scan completes.')}
                data={[{ x: t('No results'), y: 1 }]}
                colorScale={[grey]}
                title={score != null ? formatCount(score, locale) : '—'}
                subTitle={t('of 100')}
                height={200}
                width={300}
                constrainToVisibleArea
              />
            ) : (
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <ChartDonut
                  ariaTitle={t('Check results')}
                  ariaDesc={t('Composition of compliance check results')}
                  constrainToVisibleArea
                  data={segments.map((s) => ({ x: s.label, y: s.value }))}
                  colorScale={segments.map((s) => s.color)}
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
                  {segments.map((s) => (
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
                      <span>
                        {t('{{label}} ({{num}})', {
                          label: s.label,
                          num: formatCount(s.value, locale),
                        })}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
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
                    <Timestamp timestamp={baseline.status.lastScanTime} />
                  ) : (
                    '—'
                  )}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Next scan')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {baseline.status?.nextScanTime ? (
                    <Timestamp timestamp={baseline.status.nextScanTime} />
                  ) : (
                    '—'
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
        {(baseline.status?.history?.length ?? 0) > 1 && (
          <Card>
            <CardTitle>{t('Score trend')}</CardTitle>
            <CardBody style={{ height: historyModeMismatch ? 260 : 230 }}>
              {historyModeMismatch && (
                <HelperText style={{ marginBottom: 'var(--pf-t--global--spacer--sm)' }}>
                  <HelperTextItem variant="warning">
                    {t(
                      'Scoring mode changed since some history points were recorded. Older points may not be comparable until the next completed scan.',
                    )}
                  </HelperTextItem>
                </HelperText>
              )}
              <Chart
                ariaTitle={t('Score trend')}
                height={200}
                width={300}
                padding={{ top: 10, bottom: 40, left: 40, right: 20 }}
                domain={{ y: [0, 100] }}
                // Time scale so ticks are spaced by date (a few, deduplicated),
                // not one categorical tick per snapshot with repeated labels.
                scale={{ x: 'time', y: 'linear' }}
              >
                <ChartAxis
                  tickFormat={(x: Date) => new Date(x).toLocaleDateString(locale)}
                  fixLabelOverlap
                />
                <ChartAxis
                  dependentAxis
                  tickFormat={(y: number) => formatCount(y, locale)}
                />
                <ChartArea
                  data={(baseline.status?.history ?? []).map((h) => ({
                    x: new Date(h.time),
                    y: h.score,
                  }))}
                />
              </Chart>
            </CardBody>
          </Card>
        )}
        <Card>
          <CardTitle>{t('Recent changes')}</CardTitle>
          <CardBody style={{ maxHeight: 260, overflow: 'auto' }}>
            {newlyFailedItems.length === 0 && fixedItems.length === 0 ? (
              <EmptyState
                titleText={
                  hasPriorScan
                    ? t('No changes since the last scan')
                    : t('No previous scan to compare yet')
                }
                headingLevel="h4"
              >
                <EmptyStateBody>
                  {hasPriorScan
                    ? t('Fail and fix deltas will appear here after the next completed scan.')
                    : t('Run a scan, then rescan later to see newly failing and fixed checks.')}
                </EmptyStateBody>
              </EmptyState>
            ) : (
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
                  actions:
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
                    ) : undefined,
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
                  actions:
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
                    ) : undefined,
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
