// Empty state shown when no ClusterBaseline CR exists. Shared by Overview,
// Profiles, and Remediations so the copy and OperatorHub link stay in sync.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  EmptyStateFooter,
} from '@patternfly/react-core';

const BaselineNotConfigured: React.FC<{ style?: React.CSSProperties }> = ({ style }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  return (
    <EmptyState titleText={t('Baseline not configured')} headingLevel="h2" style={style}>
      <EmptyStateBody>
        {t(
          'No ClusterBaseline resource found. Install the baseline-security operator from OperatorHub to start scanning.',
        )}
      </EmptyStateBody>
      <EmptyStateFooter>
        <Button component="a" href="/operatorhub" variant="primary">
          {t('Browse OperatorHub')}
        </Button>
      </EmptyStateFooter>
    </EmptyState>
  );
};

export default BaselineNotConfigured;
