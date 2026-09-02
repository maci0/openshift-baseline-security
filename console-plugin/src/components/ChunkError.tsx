import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, PageSection, Skeleton } from '@patternfly/react-core';
import { ChunkState, useChunk } from './chunkLoad';

// Visible failure for a dropped or 404'd async chunk. Retry re-invokes the
// same import(); webpack resets a failed chunk id so the GET runs again.
export const ChunkError: React.FC<{ onRetry: () => void }> = ({ onRetry }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  return (
    <Alert variant="danger" isInline isLiveRegion title={t('Failed to load this view.')}>
      <Button variant="link" isInline onClick={onRetry}>
        {t('Retry')}
      </Button>
    </Alert>
  );
};

type ChunkGateProps<T> = {
  load: () => Promise<T>;
  children: (module: T) => React.ReactNode;
};

// HorizontalNav tab body: skeleton while the chunk is in flight, Retry on
// failure, otherwise the tab. PageSection matches the tabs' own wrapping so
// a failed load does not jump the layout.
export function ChunkGate<T>(props: ChunkGateProps<T>): React.ReactElement {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [attempt, setAttempt] = React.useState(0);
  const chunk: ChunkState<T> = useChunk(props.load, attempt);
  if (chunk.status === 'failed') {
    return (
      <PageSection>
        <ChunkError onRetry={() => setAttempt((n) => n + 1)} />
      </PageSection>
    );
  }
  if (chunk.status === 'loading') {
    return (
      <PageSection>
        <Skeleton height="200px" screenreaderText={t('Loading compliance data')} />
      </PageSection>
    );
  }
  return <>{props.children(chunk.module)}</>;
}
