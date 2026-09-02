import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import { formatCount } from '../dates';
import { ClusterBaseline, ClusterBaselineGVK } from '../models';
import { clusterScore, scoreColor } from '../scoring';

/**
 * Value for the "Compliance score" item added to the cluster Overview Details
 * card (console.dashboards/custom/overview/detail/item). Links to the full
 * Compliance page. Renders nothing meaningful until the ClusterBaseline exists.
 */
const ClusterScoreItem: React.FC = () => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, error] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });

  if (!loaded) {
    return (
      <span aria-busy="true" aria-label={t('Loading compliance data')}>
        —
      </span>
    );
  }
  if (error) {
    // Distinct from loading "—": API/watch failures must not look like an empty score.
    return (
      <a href="/baseline-security" aria-label={t('Compliance score unavailable')}>
        {t('Unavailable')}
      </a>
    );
  }
  const score = clusterScore(baselines);
  if (score == null) {
    return (
      <a href="/baseline-security" aria-label={t('Compliance score not scanned')}>
        {t('Not scanned')}
      </a>
    );
  }
  // Locale-aware digits/grouping so ar/fa/hi (and others) match console locale.
  // Format the scale the same way as the score so ar-SA (and similar) do not
  // mix native-digit scores with a Latin "100".
  const scoreText = formatCount(score, i18n.language);
  const maxText = formatCount(100, i18n.language);
  return (
    <a
      href="/baseline-security"
      aria-label={t('Compliance score {{score}} of {{max}}', {
        score: scoreText,
        max: 100,
        formattedMax: maxText,
      })}
    >
      <span style={{ color: scoreColor(score) }}>
        {t('{{score}} / {{max}}', { score: scoreText, max: 100, formattedMax: maxText })}
      </span>
    </a>
  );
};

export default ClusterScoreItem;
