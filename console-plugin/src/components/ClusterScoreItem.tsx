import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import { formatCount } from '../dates';
import { ClusterBaseline, ClusterBaselineGVK } from '../models';
import { clusterScore, scoreColor } from '../scoring';
import ConsoleLink from './ConsoleLink';

// Module-level so every render passes the same watch options object. A literal
// built in the component body is a new reference on each render, and the SDK
// re-subscribes when the options it is handed change: the dashboard card would
// re-subscribe on every state change of the item. The page and tab watches are
// built with useMemo for the same reason.
const BASELINES_WATCH = {
  groupVersionKind: ClusterBaselineGVK,
  isList: true,
} as const;

/**
 * Value for the "Compliance score" item added to the cluster Overview Details
 * card (console.dashboards/custom/overview/detail/item). Links to the full
 * Compliance page. Renders nothing meaningful until the ClusterBaseline exists.
 */
const ClusterScoreItem: React.FC = () => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, error] =
    useK8sWatchResource<ClusterBaseline[]>(BASELINES_WATCH);

  // Error first: a watch that fails before its first successful list never sets
  // loaded, so checking !loaded ahead of error would strand the card on the
  // loading "—" forever. Matches the loaded || !!error guard in CompliancePage.
  if (error) {
    // Distinct from loading "—": API/watch failures must not look like an empty score.
    return (
      <ConsoleLink href="/baseline-security" aria-label={t('Compliance score unavailable')}>
        {t('Unavailable')}
      </ConsoleLink>
    );
  }
  if (!loaded) {
    return (
      <span aria-busy="true" aria-label={t('Loading compliance data')}>
        —
      </span>
    );
  }
  const score = clusterScore(baselines);
  if (score == null) {
    return (
      <ConsoleLink href="/baseline-security" aria-label={t('Compliance score not scanned')}>
        {t('Not scanned')}
      </ConsoleLink>
    );
  }
  // Locale-aware digits/grouping so ar/fa/hi (and others) match console locale.
  // Format the scale the same way as the score so ar-SA (and similar) do not
  // mix native-digit scores with a Latin "100".
  const scoreText = formatCount(score, i18n.language);
  const maxText = formatCount(100, i18n.language);
  return (
    // ConsoleLink, like the unavailable and not-scanned branches above: a bare
    // href to a console route leaves the SPA and reboots the whole shell, so a
    // click that starts on the cluster Overview page cost a full page load and
    // dropped every watch this card and the Compliance page hold open.
    <ConsoleLink
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
    </ConsoleLink>
  );
};

export default ClusterScoreItem;
