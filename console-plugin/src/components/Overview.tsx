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
  isOwnedByBaseline,
  ResultCounts,
  ScoreSnapshot,
  suiteFilterKey,
} from '../models';
import {
  aggregateCounts,
  changedChecks,
  errorMessage,
  expiringWaivers,
  isValidCron,
  profileScore,
  resourceVersionTest,
  resultsHref,
  schedulePatch,
} from '../utils';

// Inline editor for spec.schedule in the Details card, gated on patch permission.
const ScheduleEditor: React.FC<{ baseline: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const current = baseline.spec.schedule || '0 1 * * *';
  const [editing, setEditing] = React.useState(false);
  const [value, setValue] = React.useState(current);
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [canEdit, canEditLoading] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const valid = isValidCron(value);

  if (!editing) {
    return (
      <Split hasGutter>
        <SplitItem>
          <code>{current}</code>
        </SplitItem>
        {canEdit && !canEditLoading && (
          <SplitItem>
            <Button
              variant="link"
              isInline
              onClick={() => {
                setValue(current);
                setErr(null);
                setEditing(true);
              }}
            >
              {t('Edit')}
            </Button>
          </SplitItem>
        )}
      </Split>
    );
  }
  const save = async () => {
    setBusy(true);
    setErr(null);
    try {
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        data: [
          ...resourceVersionTest(baseline.metadata.resourceVersion),
          ...schedulePatch(!!baseline.spec.schedule, value.trim()),
        ],
      });
      setEditing(false);
    } catch (e) {
      setErr(errorMessage(e) ?? t('Failed to update schedule.'));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Split hasGutter>
        <SplitItem isFilled>
          <TextInput
            aria-label={t('Schedule')}
            value={value}
            onChange={(_e, v) => setValue(v)}
            validated={valid ? 'default' : 'error'}
          />
        </SplitItem>
        <SplitItem>
          <Button variant="primary" isInline isDisabled={!valid || busy} isLoading={busy} onClick={() => void save()}>
            {t('Save')}
          </Button>
        </SplitItem>
        <SplitItem>
          <Button
            variant="link"
            isInline
            isDisabled={busy}
            onClick={() => {
              setValue(current);
              setErr(null);
              setEditing(false);
            }}
          >
            {t('Cancel')}
          </Button>
        </SplitItem>
      </Split>
      {!valid && (
        <HelperText>
          <HelperTextItem variant="error">{t('Enter a 5-field cron schedule.')}</HelperTextItem>
        </HelperText>
      )}
      {err && <Alert variant="danger" isInline title={err} style={{ marginTop: 4 }} />}
    </>
  );
};

const CountRow: React.FC<{
  icon: React.ReactElement;
  status: React.ComponentProps<typeof Icon>['status'];
  label: string;
  count: number;
  href?: string;
}> = ({ icon, status, label, count, href }) => (
  <Flex gap={{ default: 'gapSm' }} alignItems={{ default: 'alignItemsCenter' }}>
    <FlexItem>
      <Icon status={status} size="sm">
        {icon}
      </Icon>
    </FlexItem>
    <FlexItem grow={{ default: 'grow' }}>{label}</FlexItem>
    <FlexItem>{href && count > 0 ? <a href={href}>{count}</a> : count}</FlexItem>
  </Flex>
);

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

const Overview: React.FC<{
  baseline?: ClusterBaseline;
  loaded: boolean;
  // Shared from CompliancePage (single watch); used for Recent changes titles
  // and SeverityWeighted per-profile scores.
  checkResults?: ComplianceCheckResult[];
}> = ({ baseline, loaded, checkResults }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');

  // SeverityWeighted per-profile scores need to scan check results. Group owned
  // results by filter key once here instead of letting each profile card's
  // profileScore re-scan every result (O(cards x results)); each card then only
  // weighs its own bucket. null in Flat mode, where scores use counts alone.
  const weighted = baseline?.spec.scoring?.mode === 'SeverityWeighted';
  const resultsByKey = React.useMemo(() => {
    if (!weighted) {
      return null;
    }
    const profiles = baseline?.spec.profiles;
    const tailored = baseline?.spec.tailoredProfiles;
    const m = new Map<string, ComplianceCheckResult[]>();
    for (const r of checkResults ?? []) {
      if (!isOwnedByBaseline(r.metadata.labels, profiles, tailored)) {
        continue;
      }
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
  }, [weighted, checkResults, baseline?.spec.profiles, baseline?.spec.tailoredProfiles]);

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
        <EmptyState titleText={t('Baseline not configured')}>
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
  const newlyFailed = baseline.status?.newlyFailed ?? [];
  const fixed = baseline.status?.fixed ?? [];
  const newlyFailedItems = changedChecks(newlyFailed, checkResults);
  const fixedItems = changedChecks(fixed, checkResults);
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
          title={t('Scanning is disabled')}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          {t('No profiles are selected. Enable a profile under the Profiles tab to resume scanning.')}
        </Alert>
      )}
      {degraded && (
        <Alert
          variant="warning"
          isInline
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
          title={t('{{count}} check newly failing since the last scan', {
            count: newlyFailed.length,
          })}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        >
          <a href={resultsHref('FAIL')}>{t('Review failing checks')}</a>
          {fixed.length > 0 && ` • ${t('{{count}} fixed', { count: fixed.length })}`}
        </Alert>
      )}
      {expiring.length > 0 && (
        <Alert
          variant="warning"
          isInline
          title={t('{{count}} waiver expiring within two weeks', { count: expiring.length })}
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
                data={[{ x: t('No results'), y: 1 }]}
                colorScale={[grey]}
                title={score != null ? `${score}` : '—'}
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
                  labels={({ datum }) => `${datum.x}: ${datum.y}`}
                  title={score != null ? `${score}` : '—'}
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
                      <span>{`${s.label} (${s.value})`}</span>
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
                  {/* Empty schedule is defaulted by the operator to 0 1 * * *. */}
                  <ScheduleEditor baseline={baseline} />
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Compliance Operator')}</DescriptionListTerm>
                <DescriptionListDescription>{coLabel}</DescriptionListDescription>
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
            <CardBody style={{ height: 230 }}>
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
                  tickFormat={(x: Date) => new Date(x).toLocaleDateString()}
                  fixLabelOverlap
                />
                <ChartAxis dependentAxis />
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
              />
            ) : (
              <DescriptionList isCompact>
                {newlyFailedItems.length > 0 && (
                  <DescriptionListGroup>
                    <DescriptionListTerm>
                      {t('Newly failing ({{count}})', { count: newlyFailedItems.length })}
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
                      {t('Fixed ({{count}})', { count: fixedItems.length })}
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
          // Match operator scoring.mode: Flat uses counts; SeverityWeighted uses
          // the same weight table over watched check results.
          const pScore = profileScore(p, {
            mode: baseline.spec.scoring?.mode,
            filterKey: p.key,
            results: resultsByKey ? resultsByKey.get(p.key) ?? [] : checkResults,
            profiles: baseline.spec.profiles,
            tailoredProfiles: baseline.spec.tailoredProfiles,
            waivers: baseline.spec.waivers,
          });
          return (
            <Card key={p.key}>
              <CardHeader
                actions={{
                  actions:
                    pScore != null ? (
                      <Label isCompact color={pScore >= 90 ? 'green' : pScore >= 60 ? 'orange' : 'red'}>
                        {pScore}
                      </Label>
                    ) : undefined,
                  hasNoOffset: true,
                }}
              >
                <CardTitle>{p.key.toUpperCase()}</CardTitle>
              </CardHeader>
              <CardBody>
                <ProfileCounts counts={p} filterKey={p.key} />
                <MiniTrend history={p.history} />
              </CardBody>
            </Card>
          );
        })}
        {(baseline.status?.tailoredProfiles ?? []).map((tp) => {
          const pScore = profileScore(tp, {
            mode: baseline.spec.scoring?.mode,
            filterKey: `tp-${tp.name}`,
            results: resultsByKey ? resultsByKey.get(`tp-${tp.name}`) ?? [] : checkResults,
            profiles: baseline.spec.profiles,
            tailoredProfiles: baseline.spec.tailoredProfiles,
            waivers: baseline.spec.waivers,
          });
          return (
            <Card key={`tp-${tp.name}`}>
              <CardHeader
                actions={{
                  actions:
                    pScore != null ? (
                      <Label isCompact color={pScore >= 90 ? 'green' : pScore >= 60 ? 'orange' : 'red'}>
                        {pScore}
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
