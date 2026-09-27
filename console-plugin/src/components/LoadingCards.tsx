// Skeleton gallery rendered while the ClusterBaseline watch has not delivered
// yet. Shared by Overview and Profiles so both tabs show the same placeholder
// shape and the same screen-reader text instead of drifting per call site.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  Card,
  CardBody,
  Gallery,
  PageSection,
  Skeleton,
} from '@patternfly/react-core';

const LoadingCards: React.FC<{ cardMinWidth: string; skeletonHeight: string }> = ({
  cardMinWidth,
  skeletonHeight,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');

  return (
    <PageSection>
      <Gallery hasGutter minWidths={{ default: cardMinWidth }}>
        {[0, 1, 2].map((i) => (
          <Card key={i}>
            <CardBody>
              <Skeleton height={skeletonHeight} screenreaderText={t('Loading compliance data')} />
            </CardBody>
          </Card>
        ))}
      </Gallery>
    </PageSection>
  );
};

export default LoadingCards;
