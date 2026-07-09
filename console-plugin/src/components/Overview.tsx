import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { Timestamp, useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import {
  Chart,
  ChartArea,
  ChartAxis,
  ChartDonutUtilization,
} from '@patternfly/react-charts/victory';
import {
  Alert,
  Card,
  CardBody,
  CardTitle,
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
  Spinner,
} from '@patternfly/react-core';
import {
  CheckCircleIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
} from '@patternfly/react-icons';
import {
  ClusterBaseline,
  ComplianceCheckResult,
  ComplianceCheckResultGVK,
  ComplianceRemediation,
  ComplianceRemediationGVK,
  isOwnedByBaseline,
} from '../models';
import { scoreColor } from '../utils';

const resultsHref = (filter: string) =>
  `/baseline-security/results?rowFilter-result-status=${filter}`;

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
  const [remediations] = useK8sWatchResource<ComplianceRemediation[]>({
    groupVersionKind: ComplianceRemediationGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const [results] = useK8sWatchResource<ComplianceCheckResult[]>({
    groupVersionKind: ComplianceCheckResultGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });

  if (!loaded) {
    return (
      <PageSection>
        <Spinner />
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
  const score = baseline.status?.score;

  const ownedRemediations = (remediations ?? []).filter((r) =>
    isOwnedByBaseline(r.metadata.labels, baseline.spec.profiles),
  );
  const ownedResults = (results ?? []).filter((r) =>
    isOwnedByBaseline(r.metadata.labels, baseline.spec.profiles),
  );

  const failBySeverity: Record<string, number> = {};
  for (const r of ownedResults) {
    if (r.status === 'FAIL') failBySeverity[r.severity] = (failBySeverity[r.severity] ?? 0) + 1;
  }

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
      <Gallery hasGutter minWidths={{ default: '300px' }}>
        <Card>
          <CardTitle>{t('Compliance score')}</CardTitle>
          <CardBody style={{ height: 230 }}>
            <ChartDonutUtilization
              ariaTitle={t('Compliance score')}
              data={{ x: t('Score'), y: score ?? 0 }}
              title={score != null ? `${score}` : '—'}
              subTitle={t('of 100')}
              colorScale={[scoreColor(score)]}
              height={200}
              width={300}
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
                <DescriptionListDescription>
                  {baseline.status?.complianceOperatorVersion || t('Installing')}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Remediations')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <a href="/baseline-security/remediations">
                    {t('{{available}} available, {{applied}} applied', {
                      available: ownedRemediations.filter((r) => !r.spec.apply).length,
                      applied: ownedRemediations.filter((r) => r.spec.apply).length,
                    })}
                  </a>
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
        {Object.keys(failBySeverity).length > 0 && (
          <Card>
            <CardTitle>{t('Failed checks by severity')}</CardTitle>
            <CardBody>
              {['high', 'medium', 'low', 'unknown']
                .filter((s) => failBySeverity[s])
                .map((s) => (
                  <CountRow
                    key={s}
                    icon={s === 'high' ? <ExclamationCircleIcon /> : <ExclamationTriangleIcon />}
                    status={s === 'high' ? 'danger' : 'warning'}
                    label={s}
                    count={failBySeverity[s]}
                    href={resultsHref('FAIL')}
                  />
                ))}
            </CardBody>
          </Card>
        )}
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
        {(baseline.status?.profiles ?? []).map((p) => (
          <Card key={p.key}>
            <CardTitle>{p.key.toUpperCase()}</CardTitle>
            <CardBody>
              <CountRow
                icon={<CheckCircleIcon />}
                status="success"
                label={t('Pass')}
                count={p.pass}
                href={resultsHref('PASS')}
              />
              <CountRow
                icon={<ExclamationCircleIcon />}
                status="danger"
                label={t('Fail')}
                count={p.fail}
                href={resultsHref('FAIL')}
              />
              <CountRow
                icon={<ExclamationTriangleIcon />}
                status="warning"
                label={t('Manual')}
                count={p.manual}
                href={resultsHref('MANUAL')}
              />
            </CardBody>
          </Card>
        ))}
      </Gallery>
    </PageSection>
  );
};

export default Overview;
