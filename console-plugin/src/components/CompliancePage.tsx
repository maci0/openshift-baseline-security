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
import { rescanPatch } from '../utils';
import {
  BaselineContext,
  OverviewRoute,
  ProfilesRoute,
  RemediationsRoute,
  ResultsRoute,
} from '../baselineContext';

type Scan = {
  metadata: {
    name: string;
    namespace: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
  };
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

  const profiles = baseline?.spec.profiles;
  const ownedScans = React.useMemo(
    () => (scans ?? []).filter((s) => isOwnedByBaseline(s.metadata.labels, profiles)),
    [scans, profiles],
  );

  const rescan = async () => {
    setRescanning(true);
    setRescanError(null);
    // Unique value so a second click still mutates the annotation (CO watches changes).
    const token = String(Date.now());
    try {
      const results = await Promise.allSettled(
        ownedScans.map((s) =>
          k8sPatch({
            model: ComplianceScanModel,
            resource: s,
            data: rescanPatch(s.metadata.annotations != null, token),
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

  const ctx = React.useMemo(() => ({ baseline, loaded }), [baseline, loaded]);

  // Page component types are module-level (stable). Only labels depend on t.
  const pages = React.useMemo(
    () => [
      { href: '', name: t('Overview'), component: OverviewRoute },
      { href: 'results', name: t('Results'), component: ResultsRoute },
      { href: 'remediations', name: t('Remediations'), component: RemediationsRoute },
      { href: 'profiles', name: t('Profiles'), component: ProfilesRoute },
    ],
    [t],
  );

  return (
    <BaselineContext.Provider value={ctx}>
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
    </BaselineContext.Provider>
  );
};

export default CompliancePage;
