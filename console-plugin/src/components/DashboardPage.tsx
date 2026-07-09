import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import {
  Card,
  CardBody,
  CardTitle,
  EmptyState,
  EmptyStateBody,
  Gallery,
  PageSection,
  Spinner,
  Title,
} from '@patternfly/react-core';
import { ClusterBaseline, ClusterBaselineGVK } from '../models';

const DashboardPage: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, error] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });

  const baseline = baselines?.[0];

  return (
    <PageSection>
      <Title headingLevel="h1">{t('Compliance')}</Title>
      {!loaded && <Spinner />}
      {loaded && (error || !baseline) && (
        <EmptyState titleText={t('Baseline not configured')}>
          <EmptyStateBody>
            {t(
              'No ClusterBaseline resource found. Install the baseline-security operator and create a ClusterBaseline to start scanning.',
            )}
          </EmptyStateBody>
        </EmptyState>
      )}
      {loaded && baseline && (
        <Gallery hasGutter>
          <Card>
            <CardTitle>{t('Compliance score')}</CardTitle>
            <CardBody>
              <Title headingLevel="h2" size="4xl">
                {baseline.status?.score ?? '—'}
              </Title>
            </CardBody>
          </Card>
          {(baseline.status?.profiles ?? []).map((p) => (
            <Card key={p.key}>
              <CardTitle>{p.key}</CardTitle>
              <CardBody>
                {t('{{pass}} pass / {{fail}} fail / {{manual}} manual', p)}
              </CardBody>
            </Card>
          ))}
        </Gallery>
      )}
    </PageSection>
  );
};

export default DashboardPage;
