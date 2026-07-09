import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  HorizontalNav,
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
} from '@openshift-console/dynamic-plugin-sdk';
import { Button, PageSection, Content, Split, SplitItem, Title } from '@patternfly/react-core';
import { ClusterBaseline, ClusterBaselineGVK, ComplianceScanGVK, ComplianceScanModel } from '../models';
import Overview from './Overview';
import ResultsTab from './ResultsTab';
import ProfilesTab from './ProfilesTab';
import RemediationsTab from './RemediationsTab';

const CompliancePage: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });
  const [scans] = useK8sWatchResource<{ metadata: { name: string; namespace: string } }[]>({
    groupVersionKind: ComplianceScanGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const baseline = baselines?.[0];
  const [rescanning, setRescanning] = React.useState(false);
  const [canRescan] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'compliancescans',
    verb: 'patch',
    namespace: 'openshift-compliance',
  });

  const rescan = async () => {
    setRescanning(true);
    try {
      await Promise.all(
        (scans ?? []).map((s) =>
          k8sPatch({
            model: ComplianceScanModel,
            resource: s,
            data: [
              {
                op: 'add',
                path: '/metadata/annotations/compliance.openshift.io~1rescan',
                value: '',
              },
            ],
          }),
        ),
      );
    } finally {
      setRescanning(false);
    }
  };

  const pages = [
    { href: '', name: t('Overview'), component: () => <Overview baseline={baseline} loaded={loaded} /> },
    { href: 'results', name: t('Results'), component: ResultsTab },
    {
      href: 'remediations',
      name: t('Remediations'),
      component: () => <RemediationsTab baseline={baseline} />,
    },
    { href: 'profiles', name: t('Profiles'), component: () => <ProfilesTab baseline={baseline} /> },
  ];

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Split hasGutter>
          <SplitItem isFilled>
            <Title headingLevel="h1">{t('Compliance')}</Title>
            <Content component="p">
              {t('Cluster benchmark compliance, scanned by the Compliance Operator.')}
            </Content>
          </SplitItem>
          <SplitItem>
            <Button
              variant="secondary"
              onClick={rescan}
              isDisabled={rescanning || !scans?.length || !canRescan}
              isLoading={rescanning}
            >
              {t('Rescan now')}
            </Button>
          </SplitItem>
        </Split>
      </PageSection>
      <HorizontalNav pages={pages} />
    </>
  );
};

export default CompliancePage;
