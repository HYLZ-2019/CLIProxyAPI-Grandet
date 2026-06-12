import { useTranslation } from 'react-i18next';
import { CartesianGrid, ComposedChart, Legend, Line, ReferenceDot, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { ProviderQuotaSeries } from '@/services/api';
import { COLORS } from '../constants';
import { formatNumber, formatPercent, formatTimestamp, formatUSD } from '../formatters';
import { toQuotaTooltipData } from '../transforms';
import type { QuotaSeriesVisibility, UsageWindow } from '../types';
import { ProviderQuotaTooltip } from './ProviderQuotaTooltip';
import styles from '../Analytics.module.scss';

function median(values: number[]): number {
  if (values.length === 0) return 0;
  const middle = Math.floor(values.length / 2);
  if (values.length % 2 === 1) return values[middle];
  return (values[middle - 1] + values[middle]) / 2;
}

export function ProviderQuotaChart(props: {
  series: ProviderQuotaSeries;
  displayWindow: UsageWindow | null;
  rangeLabel: string;
  show429Dots: boolean;
  showResetMarkers: boolean;
  seriesVisibility: QuotaSeriesVisibility;
}) {
  const { t } = useTranslation();
  const data = toQuotaTooltipData(props.series);
  const eventData = props.show429Dots
    ? data.filter((point) => (point.quota_events_count ?? 0) > 0).map((point) => ({ ...point, eventDot: point.quota_used_percent }))
    : [];
  const resetData = props.showResetMarkers
    ? (props.series.reset_markers || []).map((point) => ({ x_ts: point.reset_at, resetDot: point.points }))
    : [];
  const maxCumulativeUSD = data.reduce((max, point) => Math.max(max, point.cliproxy_cumulative_usd ?? 0), 0);
  const estimatedPointUSDValues = data
    .map((point) => point.estimated_quota_usd_point)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value) && value > 0)
    .sort((a, b) => a - b);
  const medianEstimatedPointUSD = median(estimatedPointUSDValues);
  const inputUSDPerMillion =
    Number.isFinite(props.series.input_usd_per_million) && props.series.input_usd_per_million > 0
      ? props.series.input_usd_per_million
      : 0;
  const estimatedQuotaUSD =
    Number.isFinite(props.series.estimated_quota_usd) && props.series.estimated_quota_usd > 0
      ? props.series.estimated_quota_usd
      : 0;
  const inputPriceLabel =
    inputUSDPerMillion > 0
      ? props.series.input_price_model
        ? `${props.series.input_price_model} - ${formatUSD(inputUSDPerMillion)}`
        : formatUSD(inputUSDPerMillion)
      : props.series.input_price_model
        ? `${props.series.input_price_model} - —`
        : '—';
  const estimatedQuotaInputMTokens =
    inputUSDPerMillion > 0 && estimatedQuotaUSD > 0 ? estimatedQuotaUSD / inputUSDPerMillion : 0;
  const visualQuotaUSD = medianEstimatedPointUSD > 0 ? medianEstimatedPointUSD : estimatedQuotaUSD;
  const usdAxisMax = Math.max(visualQuotaUSD, maxCumulativeUSD * 1.05, 0.01);

  return (
    <div className={styles.providerCard}>
      <div className={styles.providerHeader}>
        <div>
          <h3 className={styles.providerTitle}>{props.series.auth_id || t('analytics.quota_lines.unknown_auth')}</h3>
          <p className={styles.cardHint}>
            {props.rangeLabel}
            {' · '}
            {t('analytics.quota_lines.provider_label', { provider: props.series.provider })}
            {' · '}
            {props.series.window_type || t('analytics.quota_lines.no_window')}
          </p>
        </div>
        <div className={styles.providerStats}>
          <div className={styles.providerStat}>
            <span>{t('analytics.quota_lines.input_token_price')}</span>
            <strong>{inputPriceLabel}</strong>
          </div>
          <div className={styles.providerStat}>
            <span>{t('analytics.quota_lines.estimated_quota_usd')}</span>
            <strong>{estimatedQuotaUSD > 0 ? formatUSD(estimatedQuotaUSD) : '—'}</strong>
          </div>
          <div className={styles.providerStat}>
            <span>{t('analytics.quota_lines.estimated_quota_input_mtokens')}</span>
            <strong>{estimatedQuotaInputMTokens > 0 ? formatNumber(estimatedQuotaInputMTokens) : '—'}</strong>
          </div>
        </div>
      </div>
      {data.length === 0 ? (
        <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
      ) : (
        <ResponsiveContainer width="100%" height={260}>
          <ComposedChart data={data} margin={{ top: 10, right: 18, left: 0, bottom: 0 }}>
            <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" />
            <XAxis
              dataKey="x_ts"
              type="number"
              domain={['dataMin', 'dataMax']}
              stroke={COLORS.axis}
              fontSize={11}
              tickFormatter={(value) => formatTimestamp(Number(value), props.displayWindow)}
            />
            <YAxis yAxisId="usd" domain={[0, usdAxisMax]} stroke={COLORS.cumulative} fontSize={11} tickFormatter={(value) => formatUSD(Number(value))} />
            <YAxis yAxisId="percent" orientation="right" domain={[0, 100]} stroke={COLORS.quota} fontSize={11} tickFormatter={(value) => formatPercent(Number(value))} />
            <Tooltip
              shared
              cursor={{ stroke: COLORS.axis, strokeDasharray: '3 3' }}
              content={
                <ProviderQuotaTooltip
                  data={data}
                  bucketSizeLabel={t('analytics.quota_lines.bucket_size')}
                  bucketUSDLabel={t('analytics.quota_lines.bucket_usd')}
                  quotaLabel={t('analytics.quota_lines.quota_used')}
                  cumulativeLabel={t('analytics.quota_lines.cliproxy_cumulative')}
                  estimatedPointLabel={t('analytics.quota_lines.estimated_quota_usd_point')}
                  eventsLabel={t('analytics.quota_lines.quota_429')}
                />
              }
            />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            {props.seriesVisibility.quotaUsed && (
              <Line yAxisId="percent" type="monotone" dataKey="quota_used_percent" name={t('analytics.quota_lines.quota_used')} stroke={COLORS.quota} dot={false} activeDot={{ r: 3 }} strokeWidth={2} />
            )}
            {props.seriesVisibility.cumulativeUSD && (
              <Line yAxisId="usd" type="monotone" dataKey="cliproxy_cumulative_usd" name={t('analytics.quota_lines.cliproxy_cumulative')} stroke={COLORS.cumulative} dot={false} activeDot={{ r: 3 }} strokeWidth={2} />
            )}
            {props.seriesVisibility.estimatedQuotaUSD && (
              <Line
                yAxisId="usd"
                type="monotone"
                dataKey="estimated_quota_usd_point"
                name={t('analytics.quota_lines.estimated_quota_usd_point')}
                stroke={COLORS.estimatedQuota}
                dot={false}
                activeDot={{ r: 3 }}
                strokeWidth={2}
                connectNulls
              />
            )}
            {eventData.map((point) => (
              <ReferenceDot key={`event-${point.x_ts}`} yAxisId="percent" x={point.x_ts} y={point.eventDot} r={4} fill={COLORS.error} stroke="none" />
            ))}
            {resetData.map((point) => (
              <ReferenceDot key={`reset-${point.x_ts}`} yAxisId="percent" x={point.x_ts} y={point.resetDot} r={4} fill={COLORS.success} stroke="none" />
            ))}
          </ComposedChart>
        </ResponsiveContainer>
      )}
    </div>
  );
}
