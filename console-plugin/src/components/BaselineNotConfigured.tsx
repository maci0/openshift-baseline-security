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
import { mayWrite } from '../permissions';
import { withDisabledTip } from './DisabledTip';

const BaselineNotConfigured: React.FC<{ style?: React.CSSProperties }> = ({ style }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [canCreate, canCreateLoading] = useAccessReview(clusterBaselineCreateAccess);
  const [busy, setBusy] = React.useState(false);
  const busyRef = React.useRef(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  const create = async () => {
    if (busyRef.current) return;
    // Same gate the button carries, so a click after the review flipped to denied
    // does not spend the create.
    if (!mayWrite({ allowed: canCreate })) {
      setErr(t('You do not have permission to create the baseline.'));
      return;
    }
    busyRef.current = true;
    setBusy(true);
    setErr(null);
    setNotice(null);
    try {
      await k8sCreate({
        model: ClusterBaselineModel,
        data: defaultClusterBaselineManifest(),
      });
    } catch (e) {
      // A race with the operator default-create is not a failure, but it is not
      // a silent one either: on a broken watch the create-rejected branch used
      // to set nothing, leaving the click with no output and this empty state
      // still up. Say what happened so the click is never a dead end.
      if (isAlreadyExists(e)) {
        setNotice(
          t('ClusterBaseline/cluster already exists; this page updates when the watch sees it.'),
        );
      } else {
        setErr(errorMessage(e) ?? t('Failed to create the compliance baseline.'));
      }
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  // The create action stays visible without the create verb: hiding it leaves
  // a viewer with an empty state and no reason, and every other write control
  // in the plugin renders disabled with its reason instead.
  const createDisabled = busy || !canCreate || canCreateLoading;
  const createDisabledReason = canCreateLoading
    ? t('Checking permissions…')
    : !canCreate
      ? t('You do not have permission to create the baseline.')
      : undefined;

  return (
    <EmptyState titleText={t('Baseline not configured')} headingLevel="h2" style={style}>
      <EmptyStateBody>
        {t(
          'No ClusterBaseline resource found. The operator creates ClusterBaseline/cluster automatically. This page updates when it appears.',
        )}
      </EmptyStateBody>
      <EmptyStateFooter>
        {notice && (
          <Alert
            variant="info"
            isInline
            isLiveRegion
            title={notice}
            style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          />
        )}
        {err && (
          <Alert
            variant="danger"
            isInline
            isLiveRegion
            title={<span dir="auto">{err}</span>}
            style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          />
        )}
        {withDisabledTip(
          createDisabledReason,
          <Button
            variant="primary"
            isDisabled={createDisabled}
            isLoading={busy}
            onClick={() => void create()}
          >
            {t('Create default baseline')}
          </Button>,
        )}
      </EmptyStateFooter>
    </EmptyState>
  );
};

export default BaselineNotConfigured;
