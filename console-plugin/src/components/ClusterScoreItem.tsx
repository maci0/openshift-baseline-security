import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import { ClusterBaseline, ClusterBaselineGVK } from '../models';
import { clusterScore, scoreColor } from '../utils';

/**
 * Value for the "Compliance score" item added to the cluster Overview Details
 * card (console.dashboards/custom/overview/detail/item). Links to the full
 * Compliance page. Renders nothing meaningful until the ClusterBaseline exists.
 */
const ClusterScoreItem: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, error] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });

  if (!loaded || error) {
    return <>—</>;
  }
  const score = clusterScore(baselines);
  return (
    <a href="/baseline-security">
      {score != null ? (
        <span style={{ color: scoreColor(score) }}>{t('{{score}} / 100', { score })}</span>
      ) : (
        t('Not scanned')
      )}
    </a>
  );
};

export default ClusterScoreItem;
