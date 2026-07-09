import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { Timestamp } from '@openshift-console/dynamic-plugin-sdk';
import {
  Chart,
  ChartArea,
  ChartAxis,
  ChartDonut,
  ChartLegend,
} from '@patternfly/react-charts/victory';
import {
  Alert,
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
  Icon,
  PageSection,
  Skeleton,
} from '@patternfly/react-core';
import {
  CheckCircleIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
} from '@patternfly/react-icons';
import { ClusterBaseline } from '../models';
import { aggregateCounts, resultsHref } from '../utils';

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
  const coLabel =
    coVersion ||
    (coReady?.reason === 'NotInstalled'
      ? t('Not installed')
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
  const grey = 'var(--pf-t--global--icon--color--disabled)';
  const segments = [
    { label: t('Pass'), value: totals.pass, color: green },
    { label: t('Fail'), value: totals.fail, color: red },
    { label: t('Manual'), value: totals.manual, color: orange },
    { label: t('Error'), value: totals.error, color: red },
    { label: t('Not applicable'), value: totals.notApplicable, color: grey },
  ].filter((s) => s.value > 0);
  const totalChecks = segments.reduce((n, s) => n + s.value, 0);

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
                width={300}
                legendData={segments.map((s) => ({ name: `${s.label} (${s.value})` }))}
                legendOrientation="vertical"
                legendPosition="right"
                legendComponent={<ChartLegend gutter={8} />}
                padding={{ top: 10, bottom: 10, left: 10, right: 140 }}
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
                  <code>{baseline.spec.schedule ?? '—'}</code>
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
        {(baseline.status?.profiles ?? []).map((p) => {
          const denom = p.pass + p.fail;
          const pScore = denom > 0 ? Math.round((p.pass * 100) / denom) : null;
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
              <CountRow
                icon={<CheckCircleIcon />}
                status="success"
                label={t('Pass')}
                count={p.pass}
                href={resultsHref('PASS', p.key)}
              />
              <CountRow
                icon={<ExclamationCircleIcon />}
                status="danger"
                label={t('Fail')}
                count={p.fail}
                href={resultsHref('FAIL', p.key)}
              />
              <CountRow
                icon={<ExclamationTriangleIcon />}
                status="warning"
                label={t('Manual')}
                count={p.manual}
                href={resultsHref('MANUAL', p.key)}
              />
              </CardBody>
            </Card>
          );
        })}
        {(baseline.status?.tailoredProfiles ?? []).map((tp) => {
          const denom = tp.pass + tp.fail;
          const pScore = denom > 0 ? Math.round((tp.pass * 100) / denom) : null;
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
                <CountRow
                  icon={<CheckCircleIcon />}
                  status="success"
                  label={t('Pass')}
                  count={tp.pass}
                  href={resultsHref('PASS')}
                />
                <CountRow
                  icon={<ExclamationCircleIcon />}
                  status="danger"
                  label={t('Fail')}
                  count={tp.fail}
                  href={resultsHref('FAIL')}
                />
                <CountRow
                  icon={<ExclamationTriangleIcon />}
                  status="warning"
                  label={t('Manual')}
                  count={tp.manual}
                  href={resultsHref('MANUAL')}
                />
              </CardBody>
            </Card>
          );
        })}
      </Gallery>
    </PageSection>
  );
};

export default Overview;
