import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { k8sPatch, Timestamp, useAccessReview } from '@openshift-console/dynamic-plugin-sdk';
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
import { ClusterBaseline, ClusterBaselineModel, ResultCounts } from '../models';
import {
  aggregateCounts,
  errorMessage,
  expiringWaivers,
  isValidCron,
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
        data: schedulePatch(!!baseline.spec.schedule, value.trim()),
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
          <Button variant="link" isInline isDisabled={busy} onClick={() => setEditing(false)}>
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

const Overview: React.FC<{ baseline?: ClusterBaseline; loaded: boolean }> = ({
  baseline,
  loaded,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');

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
  const coLabel =
    coVersion ||
    (coReady?.reason === 'NotInstalled'
      ? t('Not installed')
      : coReady?.reason === 'CSVFailed'
        ? t('Failed')
        : degraded?.reason === 'InstallStalled'
          ? t('Install stalled')
          : coReady?.status === 'True'
            ? t('Installed')
            : t('Installing'));
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
  const segments = [
    { label: t('Pass'), value: totals.pass, color: green },
    { label: t('Fail'), value: totals.fail, color: red },
    { label: t('Manual'), value: totals.manual, color: orange },
    { label: t('Info'), value: totals.info, color: blue },
    { label: t('Inconsistent'), value: totals.inconsistent, color: purple },
    { label: t('Error'), value: totals.error, color: red },
    { label: t('Waived'), value: totals.waived, color: grey },
    { label: t('Not applicable'), value: totals.notApplicable, color: grey },
  ].filter((s) => s.value > 0);
  const totalChecks = segments.reduce((n, s) => n + s.value, 0);

  const WEEK = 7 * 24 * 60 * 60 * 1000;
  const expiring = expiringWaivers(baseline.spec.waivers, 2 * WEEK);
  const newlyFailed = baseline.status?.newlyFailed ?? [];
  const fixed = baseline.status?.fixed ?? [];

  return (
    <PageSection>
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
          {progressing.message || t('installing or configuring dependencies')}
        </Alert>
      )}
      {newlyFailed.length > 0 && (
        <Alert
          variant="danger"
          isInline
          title={t('{{count}} check(s) newly failing since the last scan', {
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
          title={t('{{count}} waiver(s) expiring within two weeks', { count: expiring.length })}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        />
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
                  subTitle={t('score / 100')}
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
              >
                <ChartAxis
                  tickFormat={(x: string) => new Date(x).toLocaleDateString()}
                  fixLabelOverlap
                />
                <ChartAxis dependentAxis />
                <ChartArea
                  data={(baseline.status?.history ?? []).map((h) => ({
                    x: h.time,
                    y: h.score,
                  }))}
                />
              </Chart>
            </CardBody>
          </Card>
        )}
      </Gallery>
      {/* Per-profile score cards in their own row so they stay uniform height
          instead of stretching to match the tall donut/details/trend cards. */}
      <Gallery
        hasGutter
        minWidths={{ default: '260px' }}
        style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
      >
        {(baseline.status?.profiles ?? []).map((p) => {
          // Zero-fill missing fields from older status so scores never go NaN.
          // Floor to match the operator's integer score (pass*100/(pass+fail)).
          const pass = p.pass ?? 0;
          const fail = p.fail ?? 0;
          const denom = pass + fail;
          const pScore = denom > 0 ? Math.floor((pass * 100) / denom) : null;
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
              </CardBody>
            </Card>
          );
        })}
        {(baseline.status?.tailoredProfiles ?? []).map((tp) => {
          const pass = tp.pass ?? 0;
          const fail = tp.fail ?? 0;
          const denom = pass + fail;
          const pScore = denom > 0 ? Math.floor((pass * 100) / denom) : null;
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
              </CardBody>
            </Card>
          );
        })}
      </Gallery>
    </PageSection>
  );
};

export default Overview;
