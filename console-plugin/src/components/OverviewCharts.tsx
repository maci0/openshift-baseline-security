// Victory charts for Overview. Loaded as an async chunk so the score cards,
// alerts, and details paint before the charting library is fetched.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  Chart,
  ChartArea,
  ChartAxis,
  ChartDonut,
  ChartLabel,
} from '@patternfly/react-charts/victory';
import {
  Card,
  CardBody,
  CardTitle,
  HelperText,
  HelperTextItem,
} from '@patternfly/react-core';
import { ScoreSnapshot } from '../models';
import { formatChartDate, formatCount } from '../dates';
import { resultsHref } from '../links';
import { scoreColor } from '../scoring';
import { historyContentKey, toTrendData } from './overviewTrend';

// Empty-ring fill only. Segment colors live on Overview (the legend is HTML
// next to this chart, not a Victory colorScale built here).
const DONUT_GREY = 'var(--pf-t--global--icon--color--disabled)';

// Center title uses the same 60/90 text-status token as ClusterScoreItem.
// ChartDonut cloneElement-overwrites style; merge fill onto the title slot
// (index 0) and leave the subtitle slot alone.
type DonutScoreTitleProps = React.ComponentProps<typeof ChartLabel> & {
  score: number | null | undefined;
};

const DonutScoreTitle: React.FC<DonutScoreTitleProps> = ({ score, style, ...rest }) => {
  const fill = score == null ? undefined : scoreColor(score);
  if (fill === undefined) {
    return <ChartLabel {...rest} style={style} />;
  }
  if (Array.isArray(style)) {
    const head = style[0];
    const titled = head ? { ...head, fill } : { fill };
    return <ChartLabel {...rest} style={[titled, ...style.slice(1)]} />;
  }
  const titled = style ? { ...style, fill } : { fill };
  return <ChartLabel {...rest} style={titled} />;
};
DonutScoreTitle.displayName = 'DonutScoreTitle';

type DonutSegment = {
  label: string;
  value: number;
  color: string;
  filter: string;
};

// Memoized composition donut: CCR list-watch identity changes re-render Overview
// without changing score/totals. Victory re-layout of the donut is expensive
// relative to a shallow prop check when counts are content-stable.
export const CompositionDonut = React.memo<{
  score: number | null | undefined;
  totalChecks: number;
  donutData: { x: string; y: number }[];
  donutColors: string[];
  segments: readonly DonutSegment[];
  locale: string | undefined;
}>(({ score, totalChecks, donutData, donutColors, segments, locale }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  if (totalChecks === 0) {
    return (
      <ChartDonut
        ariaTitle={t('Compliance score')}
        ariaDesc={t('No check results yet. Score is unavailable until a scan completes.')}
        // Static: score composition is status data, not a motion cue (WCAG 2.3.3).
        animate={false}
        data={[{ x: t('No results'), y: 1 }]}
        colorScale={[DONUT_GREY]}
        // Always "—" here: with zero evaluated checks the score is 0/0 and
        // meaningless, and the ariaDesc already says it is unavailable. A stale
        // non-null status.score must not paint a number over a "No results" ring.
        title="—"
        subTitle={t('of 100')}
        titleComponent={<DonutScoreTitle score={null} />}
        height={200}
        width={300}
        constrainToVisibleArea
      />
    );
  }
  return (
    // Wrap on narrow viewports so the legend is not clipped beside a fixed-width donut.
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--pf-t--global--spacer--md)',
        flexWrap: 'wrap',
      }}
    >
      <ChartDonut
        ariaTitle={t('Check results')}
        ariaDesc={t('Compliance score {{score}} of 100. Composition of compliance check results.', {
          score: score != null ? formatCount(score, locale) : t('unavailable'),
        })}
        animate={false}
        constrainToVisibleArea
        data={donutData}
        colorScale={donutColors}
        labels={({ datum }) =>
          t('{{label}}: {{value}}', {
            label: datum.x,
            value: formatCount(Number(datum.y), locale),
          })
        }
        title={score != null ? formatCount(score, locale) : '—'}
        subTitle={t('of 100')}
        titleComponent={<DonutScoreTitle score={score} />}
        height={200}
        width={200}
        padding={{ top: 10, bottom: 10, left: 10, right: 10 }}
      />
      {/* HTML legend: Victory's built-in legend clips long labels and
          caps at a fixed width; a plain list wraps and never truncates. */}
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, minWidth: 0 }}>
        {segments.map((s) => {
          const num = formatCount(s.value, locale);
          const text = t('{{label}} ({{num}})', { label: s.label, num });
          return (
            <li
              key={s.label}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--pf-t--global--spacer--sm)',
                padding: '2px 0',
              }}
            >
              <span
                aria-hidden
                style={{
                  width: 12,
                  height: 12,
                  flex: '0 0 auto',
                  borderRadius: 2,
                  backgroundColor: s.color,
                }}
              />
              {/* Same deep-link as profile CountRow so the primary
                  score card drills into Results on click/tap (touch
                  devices cannot hover Victory slices). */}
              <a
                href={resultsHref(s.filter)}
                aria-label={t('{{label}}: {{value}}', { label: s.label, value: num })}
              >
                {text}
              </a>
            </li>
          );
        })}
      </ul>
    </div>
  );
});
CompositionDonut.displayName = 'CompositionDonut';

// Memoized overall trend chart: same CCR-churn stability as MiniTrend.
export const OverallTrendChart = React.memo<{
  historyChartData: { x: Date; y: number }[];
  historyModeMismatch: boolean;
  locale: string | undefined;
}>(({ historyChartData, historyModeMismatch, locale }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  return (
    <Card>
      <CardTitle>{t('Score trend')}</CardTitle>
      <CardBody style={{ height: historyModeMismatch ? 260 : 230 }}>
        {historyModeMismatch && (
          <HelperText style={{ marginBottom: 'var(--pf-t--global--spacer--sm)' }}>
            <HelperTextItem variant="warning">
              {t(
                'Scoring mode changed since some history points were recorded. Older points may not be comparable; the next completed scan starts a fresh trend under the new mode.',
              )}
            </HelperTextItem>
          </HelperText>
        )}
        <Chart
          ariaTitle={t('Score trend')}
          ariaDesc={t('Score moved from {{from}} to {{to}} over {{scans}} scans.', {
            from: formatCount(historyChartData[0].y, locale),
            to: formatCount(historyChartData[historyChartData.length - 1].y, locale),
            scans: formatCount(historyChartData.length, locale),
          })}
          // Static trend: motion does not add meaning and can delay reading (WCAG 2.3.3).
          animate={false}
          height={200}
          width={300}
          // right: the last date tick is centered on the final point at the
          // plot edge, so it needs half a label of room or it clips at the card
          // edge (the SVG scales to the card width, magnifying the overflow).
          padding={{ top: 10, bottom: 40, left: 40, right: 45 }}
          domain={{ y: [0, 100] }}
          // Time scale so ticks are spaced by date (a few, deduplicated),
          // not one categorical tick per snapshot with repeated labels.
          scale={{ x: 'time', y: 'linear' }}
        >
          <ChartAxis
            tickFormat={(x: Date) => formatChartDate(x, locale)}
            fixLabelOverlap
          />
          <ChartAxis dependentAxis tickFormat={(y: number) => formatCount(y, locale)} />
          <ChartArea data={historyChartData} />
        </Chart>
      </CardBody>
    </Card>
  );
});
OverallTrendChart.displayName = 'OverallTrendChart';

// Memoized: CCR watch churn re-renders Overview without touching history.
export const MiniTrend = React.memo<{ history?: ScoreSnapshot[] }>(({ history }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  // Content-stable: status CR updates reallocate history with the same points.
  const histKey = historyContentKey(history);
  const chartData = React.useMemo(
    () => toTrendData(history),
    // history is read when histKey changes (same points => skip Date rebuild).
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key
    [histKey],
  );
  if (chartData.length < 2) {
    return null;
  }
  const first = chartData[0].y;
  const last = chartData[chartData.length - 1].y;
  return (
    // marginTop:auto bottom-aligns the sparkline within a flex-column CardBody,
    // so charts line up across cards whose stat-row counts differ (a card with
    // an extra Inconsistent/Waived row would otherwise sit its chart lower).
    <div style={{ height: 40, marginTop: 'auto', paddingTop: 'var(--pf-t--global--spacer--sm)' }}>
      <Chart
        ariaTitle={t('Score trend')}
        ariaDesc={t('Score moved from {{from}} to {{to}} over {{scans}} scans.', {
          from: formatCount(first, i18n.language),
          to: formatCount(last, i18n.language),
          scans: formatCount(chartData.length, i18n.language),
        })}
        animate={false}
        height={40}
        padding={{ top: 4, bottom: 4, left: 0, right: 0 }}
        minDomain={{ y: 0 }}
        maxDomain={{ y: 100 }}
        scale={{ x: 'time', y: 'linear' }}
      >
        <ChartArea data={chartData} />
      </Chart>
    </div>
  );
});
MiniTrend.displayName = 'MiniTrend';
