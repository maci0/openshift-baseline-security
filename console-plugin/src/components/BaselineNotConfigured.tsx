// Empty state shown when no ClusterBaseline CR exists. Shared by several tabs
// (Overview, Results, Profiles, Remediations) so the copy and create action
// stay in sync. The plugin is served by this operator, so OperatorHub is the
// wrong next step; the operator creates ClusterBaseline/cluster, and the
// button recovers when that default create did not land.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { k8sCreate, useAccessReview } from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  Button,
  EmptyState,
  EmptyStateBody,
  EmptyStateFooter,
} from '@patternfly/react-core';
import {
  ClusterBaselineModel,
  clusterBaselineCreateAccess,
  defaultClusterBaselineManifest,
} from '../models';
import { errorMessage, isAlreadyExists } from '../errors';

const BaselineNotConfigured: React.FC<{ style?: React.CSSProperties }> = ({ style }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [canCreate, canCreateLoading] = useAccessReview(clusterBaselineCreateAccess);
  const [busy, setBusy] = React.useState(false);
  const busyRef = React.useRef(false);
  const [err, setErr] = React.useState<string | null>(null);

  const create = async () => {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setErr(null);
    try {
      await k8sCreate({
        model: ClusterBaselineModel,
        data: defaultClusterBaselineManifest(),
      });
    } catch (e) {
      // A race with the operator default-create is success: the watch will
      // replace this empty state once ClusterBaseline/cluster is visible.
      if (!isAlreadyExists(e)) {
        setErr(errorMessage(e) ?? t('Failed to create the compliance baseline.'));
      }
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  return (
    <EmptyState titleText={t('Baseline not configured')} headingLevel="h2" style={style}>
      <EmptyStateBody>
        {t(
          'No ClusterBaseline resource found. The operator creates ClusterBaseline/cluster automatically. This page updates when it appears.',
        )}
      </EmptyStateBody>
      <EmptyStateFooter>
        {err && (
          <Alert
            variant="danger"
            isInline
            isLiveRegion
            title={err}
            style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          />
        )}
        {canCreate && !canCreateLoading && (
          <Button
            variant="primary"
            isDisabled={busy}
            isLoading={busy}
            onClick={() => void create()}
          >
            {t('Create default baseline')}
          </Button>
        )}
      </EmptyStateFooter>
    </EmptyState>
  );
};

export default BaselineNotConfigured;
