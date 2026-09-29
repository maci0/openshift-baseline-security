// Shown when the ClusterBaseline watch failed, so no baseline can be read. The
// page forces `loaded` true on a watch error to stop skeletoning forever, which
// left every tab falling into BaselineNotConfigured: a 403 on
// clusterbaselines.baselinesecurity.openshift.io or a missing CRD rendered "Baseline
// not configured" plus a Create button, claiming a resource the operator may
// have already created. A failure and an absent CR need different words, so
// this is the branch that carries the reason.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, PageSection } from '@patternfly/react-core';
import { errorMessage } from '../errors';
import { bidiIsolate } from '../text';

const BaselineUnavailable: React.FC<{ error: unknown; style?: React.CSSProperties }> = ({
  error,
  style,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const detail = errorMessage(error);
  return (
    <Alert
      variant="danger"
      isInline
      isLiveRegion
      title={t('Failed to read the compliance baseline.')}
      style={style}
    >
      {t(
        'The ClusterBaseline resource could not be read, so this view cannot tell whether the baseline exists.',
      )}
      {detail && (
        <p>
          {/* The reason is an apiserver message, not plugin copy, so it is the
              one part of the sentence whose direction and script are unknown.
              The isolate carries that direction; dir="auto" on the wrapper
              would settle on the English "Reason" and leave the message an
              unisolated run in an LTR sentence. */}
          <span dir="auto">{t('Reason: {{detail}}', { detail: bidiIsolate(detail) })}</span>
        </p>
      )}
    </Alert>
  );
};

export default BaselineUnavailable;

// PageSection-wrapped form for the tabs that render their own PageSection.
export const BaselineUnavailableSection: React.FC<{ error: unknown }> = ({ error }) => (
  <PageSection>
    <BaselineUnavailable error={error} />
  </PageSection>
);
