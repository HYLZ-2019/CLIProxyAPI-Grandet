import type { Dispatch, SetStateAction } from 'react';
import { useTranslation } from 'react-i18next';
import { Area, AreaChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { COLORS } from '../constants';
import { formatNumber, formatTimestamp } from '../formatters';
import type { TrendPoint, TrendSeriesVisibility, UsageWindow } from '../types';
import styles from '../Analytics.module.scss';

export function UsageTrendChart(props: {
  data: TrendPoint[];
  usageWindow: UsageWindow;
  visibility: TrendSeriesVisibility;
  setVisibility: Dispatch<SetStateAction<TrendSeriesVisibility>>;
}) {
  const { t } = useTranslation();

  return (
    <section className={styles.card}>
      <div className={styles.cardHeaderWithAction}>
        <div className={styles.cardHeaderText}>
          <h2 className={styles.cardTitle}>{t('analytics.charts.trend_title')}</h2>
          <p className={styles.cardHint}>{t('analytics.charts.trend_hint')}</p>
        </div>
        <div className={styles.trendSeriesToggles} aria-label={t('analytics.charts.series_toggles')}>
          {([
            ['requests', t('analytics.charts.requests_axis')],
            ['inputTokens', t('analytics.charts.input_tokens_axis')],
            ['outputTokens', t('analytics.charts.output_tokens_axis')],
            ['cachedTokens', t('analytics.charts.cached_tokens_axis')],
          ] as const).map(([key, label]) => (
            <label key={key} className={styles.trendSeriesToggle}>
              <input
                type="checkbox"
                checked={props.visibility[key]}
                onChange={(event) =>
                  props.setVisibility((current) => ({
                    ...current,
                    [key]: event.target.checked,
                  }))
                }
              />
              <span>{label}</span>
            </label>
          ))}
        </div>
      </div>
      {props.data.length === 0 ? (
        <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
      ) : (
        <ResponsiveContainer width="100%" height={240}>
          <AreaChart data={props.data} margin={{ top: 10, right: 18, left: 0, bottom: 0 }}>
            <defs>
              <linearGradient id="reqGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={COLORS.primary} stopOpacity={0.45} />
                <stop offset="100%" stopColor={COLORS.primary} stopOpacity={0.05} />
              </linearGradient>
              <linearGradient id="inputTokGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={COLORS.inputTokens} stopOpacity={0.35} />
                <stop offset="100%" stopColor={COLORS.inputTokens} stopOpacity={0.04} />
              </linearGradient>
              <linearGradient id="outputTokGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={COLORS.outputTokens} stopOpacity={0.35} />
                <stop offset="100%" stopColor={COLORS.outputTokens} stopOpacity={0.04} />
              </linearGradient>
              <linearGradient id="cachedTokGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={COLORS.cachedTokens} stopOpacity={0.28} />
                <stop offset="100%" stopColor={COLORS.cachedTokens} stopOpacity={0.03} />
              </linearGradient>
            </defs>
            <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" />
            <XAxis
              dataKey="x_ts"
              type="number"
              domain={['dataMin', 'dataMax']}
              stroke={COLORS.axis}
              fontSize={11}
              tickFormatter={(value) => formatTimestamp(Number(value), props.usageWindow)}
            />
            <YAxis yAxisId="left" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
            <YAxis yAxisId="right" orientation="right" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
            <Tooltip
              contentStyle={{
                background: 'var(--bg-primary)',
                border: '1px solid var(--border-color)',
                borderRadius: 8,
                fontSize: 12,
              }}
              formatter={(value) => formatNumber(Number(value))}
              labelFormatter={(value) => formatTimestamp(Number(value), props.usageWindow)}
            />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            {props.visibility.requests && (
              <Area yAxisId="left" type="monotone" dataKey="requests" name={t('analytics.charts.requests_axis')} stroke={COLORS.primary} fill="url(#reqGradient)" strokeWidth={2} />
            )}
            {props.visibility.inputTokens && (
              <Area yAxisId="right" type="monotone" dataKey="inputTokens" name={t('analytics.charts.input_tokens_axis')} stroke={COLORS.inputTokens} fill="url(#inputTokGradient)" strokeWidth={2} />
            )}
            {props.visibility.outputTokens && (
              <Area yAxisId="right" type="monotone" dataKey="outputTokens" name={t('analytics.charts.output_tokens_axis')} stroke={COLORS.outputTokens} fill="url(#outputTokGradient)" strokeWidth={2} />
            )}
            {props.visibility.cachedTokens && (
              <Area yAxisId="right" type="monotone" dataKey="cachedTokens" name={t('analytics.charts.cached_tokens_axis')} stroke={COLORS.cachedTokens} fill="url(#cachedTokGradient)" strokeWidth={2} />
            )}
          </AreaChart>
        </ResponsiveContainer>
      )}
    </section>
  );
}
