import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  HorizontalNav,
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  Button,
  PageSection,
  Content,
  Split,
  SplitItem,
  Title,
} from '@patternfly/react-core';
import {
  ClusterBaseline,
  ClusterBaselineGVK,
  ComplianceScanGVK,
  ComplianceScanModel,
  isOwnedByBaseline,
} from '../models';
import Overview from './Overview';
import ResultsTab from './ResultsTab';
import ProfilesTab from './ProfilesTab';
import RemediationsTab from './RemediationsTab';

type Scan = {
  metadata: { name: string; namespace: string; labels?: Record<string, string> };
};

const CompliancePage: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });
  const [scans] = useK8sWatchResource<Scan[]>({
    groupVersionKind: ComplianceScanGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const baseline = baselines?.[0];
  const [rescanning, setRescanning] = React.useState(false);
  const [rescanError, setRescanError] = React.useState<string | null>(null);
  const [canRescan] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'compliancescans',
    verb: 'patch',
    namespace: 'openshift-compliance',
  });

  const ownedScans = React.useMemo(
    () =>
      (scans ?? []).filter((s) =>
        isOwnedByBaseline(s.metadata.labels, baseline?.spec.profiles),
      ),
    [scans, baseline?.spec.profiles],
  );

  const rescan = async () => {
    setRescanning(true);
    setRescanError(null);
    try {
      const results = await Promise.allSettled(
        ownedScans.map((s) =>
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
      const failed = results.filter((r) => r.status === 'rejected');
      if (failed.length) {
        setRescanError(
          t('Failed to rescan {{count}} of {{total}} scan(s). Check permissions and try again.', {
            count: failed.length,
            total: results.length,
          }),
        );
      }
    } finally {
      setRescanning(false);
    }
  };

  const pages = [
    {
      href: '',
      name: t('Overview'),
      component: () => <Overview baseline={baseline} loaded={loaded} />,
    },
    {
      href: 'results',
      name: t('Results'),
      component: () => <ResultsTab baseline={baseline} />,
    },
    {
      href: 'remediations',
      name: t('Remediations'),
      component: () => <RemediationsTab baseline={baseline} />,
    },
    {
      href: 'profiles',
      name: t('Profiles'),
      component: () => <ProfilesTab baseline={baseline} />,
    },
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
              isDisabled={rescanning || !ownedScans.length || !canRescan}
              isLoading={rescanning}
            >
              {t('Rescan now')}
            </Button>
          </SplitItem>
        </Split>
        {rescanError && (
          <Alert
            variant="danger"
            isInline
            title={rescanError}
            style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            onClose={() => setRescanError(null)}
          />
        )}
      </PageSection>
      <HorizontalNav pages={pages} />
    </>
  );
};

export default CompliancePage;
