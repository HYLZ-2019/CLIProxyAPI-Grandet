import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Scatter,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useAuthStore } from '@/stores';
import {
  analyticsApi,
  type AnalyticsSummary,
  type ByClientRow,
  type ByModelRow,
  type HourlyAggregateRow,
  type ProviderQuotaSeries,
  type TokenPriceRow,
} from '@/services/api';
import styles from './AnalyticsPage.module.scss';

type RangeKey = '1h' | '24h' | '7d';

const RANGE_WINDOWS: Record<RangeKey, number> = {
  '1h': 60 * 60,
  '24h': 24 * 60 * 60,
  '7d': 7 * 24 * 60 * 60,
};

const COLORS = {
  primary: '#8b8680',
  success: '#10b981',
  error: '#c65746',
  axis: '#9ca3af',
  grid: 'rgba(150,150,150,0.18)',
  quota: '#7a6d9a',
  cumulative: '#5b8a72',
  bars: ['#8b8680', '#5b8a72', '#c1834d', '#7a6d9a', '#a86c6c', '#5d8aa8', '#9d8d62', '#6c8e9d'],
};

function formatNumber(n: number): string {
  if (n === 0) return '0';
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (Math.abs(n) >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (Math.abs(n) >= 1e3) return (n / 1e3).toFixed(1) + 'k';
  return String(Math.round(n * 100) / 100);
}

function formatTimestamp(ts: number, range: RangeKey): string {
  const d = new Date(ts * 1000);
  if (range === '1h' || range === '24h') {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
}

function formatDateTime(ts: number): string {
  return ts > 0 ? new Date(ts * 1000).toLocaleString() : '—';
}

function aggregateByHour(rows: HourlyAggregateRow[]): Array<{
  hour: number;
  requests: number;
  totalTokens: number;
  errors: number;
}> {
  const byHour = new Map<
    number,
    { hour: number; requests: number; totalTokens: number; errors: number }
  >();
  for (const r of rows) {
    const cur = byHour.get(r.hour_ts) || {
      hour: r.hour_ts,
      requests: 0,
      totalTokens: 0,
      errors: 0,
    };
    cur.requests += r.request_count;
    cur.totalTokens += r.total_tokens_sum;
    cur.errors += r.error_count;
    byHour.set(r.hour_ts, cur);
  }
  return Array.from(byHour.values()).sort((a, b) => a.hour - b.hour);
}

function priceStatusKey(status: string): string {
  return status.trim().toLowerCase().replace(/[^a-z0-9_]+/g, '_') || 'unknown';
}

export function AnalyticsPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const disconnected = connectionStatus !== 'connected';

  const [range, setRange] = useState<RangeKey>('24h');
  const [show429Dots, setShow429Dots] = useState(true);
  const [showResetMarkers, setShowResetMarkers] = useState(true);
  const [resetOn429, setResetOn429] = useState(false);
  const [resetOnRefresh, setResetOnRefresh] = useState(false);
  const [summary, setSummary] = useState<AnalyticsSummary | null>(null);
  const [hourly, setHourly] = useState<HourlyAggregateRow[]>([]);
  const [byModel, setByModel] = useState<ByModelRow[]>([]);
  const [byClient, setByClient] = useState<ByClientRow[]>([]);
  const [providerSeries, setProviderSeries] = useState<ProviderQuotaSeries[]>([]);
  const [tokenPrices, setTokenPrices] = useState<TokenPriceRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [notEnabled, setNotEnabled] = useState(false);

  const refresh = useCallback(async () => {
    if (disconnected) return;
    setLoading(true);
    setError('');
    setNotEnabled(false);

    const now = Math.floor(Date.now() / 1000);
    const from = now - RANGE_WINDOWS[range];
    const to = now;

    const results = await Promise.allSettled([
      analyticsApi.getSummary({ from, to }),
      analyticsApi.getHourly({ from, to }),
      analyticsApi.getByModel({ from, to }),
      analyticsApi.getByClient({ from, to }),
      analyticsApi.getProviderQuotaLines({
        from,
        to,
        reset_on_429: resetOn429,
        reset_on_refresh: resetOnRefresh,
      }),
      analyticsApi.getTokenPrices(),
    ]);

    const allDisabled = results.every(
      (r) => r.status === 'rejected' && (r.reason as { status?: number })?.status === 503,
    );
    if (allDisabled) {
      setNotEnabled(true);
      setLoading(false);
      return;
    }

    const firstErr = results.find((r) => r.status === 'rejected') as
      | PromiseRejectedResult
      | undefined;
    if (firstErr && results.every((r) => r.status === 'rejected')) {
      setError((firstErr.reason as Error)?.message || t('analytics.errors.load_failed'));
      setLoading(false);
      return;
    }

    if (results[0].status === 'fulfilled') setSummary(results[0].value);
    if (results[1].status === 'fulfilled') setHourly(results[1].value || []);
    if (results[2].status === 'fulfilled') setByModel(results[2].value || []);
    if (results[3].status === 'fulfilled') setByClient(results[3].value || []);
    if (results[4].status === 'fulfilled') setProviderSeries(results[4].value.series || []);
    if (results[5].status === 'fulfilled') setTokenPrices(results[5].value || []);

    setLoading(false);
  }, [disconnected, range, resetOn429, resetOnRefresh, t]);

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      void refresh();
    }, 0);
    return () => window.clearTimeout(timeout);
  }, [refresh]);

  useHeaderRefresh(refresh);

  const trendData = useMemo(
    () =>
      aggregateByHour(hourly).map((p) => ({
        ...p,
        label: formatTimestamp(p.hour, range),
      })),
    [hourly, range],
  );

  const quotaEventCount = useMemo(
    () =>
      providerSeries.reduce(
        (sum, series) => sum + series.points.reduce((inner, p) => inner + p.quota_events_count, 0),
        0,
      ),
    [providerSeries],
  );

  const successRate =
    summary && summary.requests > 0
      ? ((summary.success_count / summary.requests) * 100).toFixed(1) + '%'
      : '—';

  if (disconnected) {
    return (
      <div className={styles.container}>
        <div className={styles.pageHeader}>
          <h1 className={styles.pageTitle}>{t('analytics.title')}</h1>
        </div>
        <div className={styles.emptyState}>{t('analytics.disconnected')}</div>
      </div>
    );
  }

  if (notEnabled) {
    return (
      <div className={styles.container}>
        <div className={styles.pageHeader}>
          <h1 className={styles.pageTitle}>{t('analytics.title')}</h1>
        </div>
        <div className={styles.emptyState}>
          <p>{t('analytics.not_enabled')}</p>
          <p className={styles.subtle}>{t('analytics.not_enabled_hint')}</p>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.container}>
      <div className={styles.pageHeader}>
        <h1 className={styles.pageTitle}>{t('analytics.title')}</h1>
        <p className={styles.description}>{t('analytics.description')}</p>
      </div>

      <div className={styles.toolbar}>
        <div className={styles.rangeGroup} role="tablist">
          {(['1h', '24h', '7d'] as RangeKey[]).map((r) => (
            <button
              key={r}
              type="button"
              role="tab"
              aria-selected={range === r}
              className={`${styles.rangeBtn} ${range === r ? styles.rangeBtnActive : ''}`}
              onClick={() => setRange(r)}
            >
              {t(`analytics.range.${r}`)}
            </button>
          ))}
        </div>
        <div className={styles.toggleGroup}>
          <button
            type="button"
            aria-pressed={show429Dots}
            className={`${styles.toggleBtn} ${show429Dots ? styles.toggleBtnActive : ''}`}
            onClick={() => setShow429Dots((v) => !v)}
          >
            {show429Dots ? t('analytics.quota_lines.hide_429') : t('analytics.quota_lines.show_429')}
          </button>
          <button
            type="button"
            aria-pressed={showResetMarkers}
            className={`${styles.toggleBtn} ${showResetMarkers ? styles.toggleBtnActive : ''}`}
            onClick={() => setShowResetMarkers((v) => !v)}
          >
            {showResetMarkers
              ? t('analytics.quota_lines.hide_refresh_markers')
              : t('analytics.quota_lines.show_refresh_markers')}
          </button>
          <button
            type="button"
            aria-pressed={resetOn429}
            className={`${styles.toggleBtn} ${resetOn429 ? styles.toggleBtnActive : ''}`}
            onClick={() => setResetOn429((v) => !v)}
          >
            {t('analytics.quota_lines.reset_on_429')}
          </button>
          <button
            type="button"
            aria-pressed={resetOnRefresh}
            className={`${styles.toggleBtn} ${resetOnRefresh ? styles.toggleBtnActive : ''}`}
            onClick={() => setResetOnRefresh((v) => !v)}
          >
            {t('analytics.quota_lines.reset_on_refresh')}
          </button>
        </div>
        {loading && <span className={styles.loadingTag}>{t('common.loading')}</span>}
      </div>

      {error && <div className={styles.errorBox}>{error}</div>}

      <section className={styles.statsGrid}>
        <StatCard
          label={t('analytics.cards.requests')}
          value={summary ? formatNumber(summary.requests) : '—'}
          accent="primary"
        />
        <StatCard
          label={t('analytics.cards.success_rate')}
          value={successRate}
          accent="success"
          sublabel={summary ? `${summary.success_count} / ${summary.requests}` : ''}
        />
        <StatCard
          label={t('analytics.cards.total_tokens')}
          value={summary ? formatNumber(summary.total_tokens) : '—'}
          accent="primary"
          sublabel={
            summary
              ? `↑ ${formatNumber(summary.input_tokens)}  ↓ ${formatNumber(summary.output_tokens)}`
              : ''
          }
        />
        <StatCard
          label={t('analytics.cards.errors')}
          value={summary ? formatNumber(summary.error_count) : '—'}
          accent={summary && summary.error_count > 0 ? 'error' : 'primary'}
          sublabel={t('analytics.cards.quota_events_count', { count: quotaEventCount })}
        />
      </section>

      <section className={styles.card}>
        <div className={styles.cardHeader}>
          <h2 className={styles.cardTitle}>{t('analytics.charts.trend_title')}</h2>
          <p className={styles.cardHint}>{t('analytics.charts.trend_hint')}</p>
        </div>
        {trendData.length === 0 ? (
          <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
        ) : (
          <ResponsiveContainer width="100%" height={240}>
            <AreaChart data={trendData} margin={{ top: 10, right: 18, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="reqGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={COLORS.primary} stopOpacity={0.45} />
                  <stop offset="100%" stopColor={COLORS.primary} stopOpacity={0.05} />
                </linearGradient>
                <linearGradient id="tokGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={COLORS.success} stopOpacity={0.45} />
                  <stop offset="100%" stopColor={COLORS.success} stopOpacity={0.05} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" />
              <XAxis dataKey="label" stroke={COLORS.axis} fontSize={11} />
              <YAxis yAxisId="left" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
              <YAxis
                yAxisId="right"
                orientation="right"
                stroke={COLORS.axis}
                fontSize={11}
                tickFormatter={formatNumber}
              />
              <Tooltip
                contentStyle={{
                  background: 'var(--bg-primary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 8,
                  fontSize: 12,
                }}
                formatter={(value) => formatNumber(Number(value))}
              />
              <Legend wrapperStyle={{ fontSize: 12 }} />
              <Area
                yAxisId="left"
                type="monotone"
                dataKey="requests"
                name={t('analytics.charts.requests_axis')}
                stroke={COLORS.primary}
                fill="url(#reqGradient)"
                strokeWidth={2}
              />
              <Area
                yAxisId="right"
                type="monotone"
                dataKey="totalTokens"
                name={t('analytics.charts.tokens_axis')}
                stroke={COLORS.success}
                fill="url(#tokGradient)"
                strokeWidth={2}
              />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </section>

      <div className={styles.twoCol}>
        <section className={styles.card}>
          <div className={styles.cardHeader}>
            <h2 className={styles.cardTitle}>{t('analytics.charts.by_model_title')}</h2>
            <p className={styles.cardHint}>{t('analytics.charts.by_model_hint')}</p>
          </div>
          {byModel.length === 0 ? (
            <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
          ) : (
            <ResponsiveContainer width="100%" height={Math.max(190, byModel.length * 30 + 28)}>
              <BarChart data={byModel.slice(0, 10)} layout="vertical" margin={{ top: 2, right: 18, left: 4, bottom: 2 }}>
                <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" horizontal={false} />
                <XAxis type="number" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
                <YAxis type="category" dataKey="model" stroke={COLORS.axis} fontSize={11} width={140} />
                <Tooltip
                  contentStyle={{
                    background: 'var(--bg-primary)',
                    border: '1px solid var(--border-color)',
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                  formatter={(value) => formatNumber(Number(value))}
                />
                <Bar dataKey="request_count" name={t('analytics.charts.requests_axis')}>
                  {byModel.slice(0, 10).map((_, i) => (
                    <Cell key={i} fill={COLORS.bars[i % COLORS.bars.length]} />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          )}
        </section>

        <section className={styles.card}>
          <div className={styles.cardHeader}>
            <h2 className={styles.cardTitle}>{t('analytics.charts.by_client_title')}</h2>
            <p className={styles.cardHint}>{t('analytics.charts.by_client_hint')}</p>
          </div>
          {byClient.length === 0 ? (
            <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
          ) : (
            <ResponsiveContainer width="100%" height={Math.max(190, byClient.length * 30 + 28)}>
              <BarChart
                data={byClient.slice(0, 10).map((c) => ({ ...c, label: `#${c.client_key_id}` }))}
                layout="vertical"
                margin={{ top: 2, right: 18, left: 4, bottom: 2 }}
              >
                <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" horizontal={false} />
                <XAxis type="number" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
                <YAxis type="category" dataKey="label" stroke={COLORS.axis} fontSize={11} width={80} />
                <Tooltip
                  contentStyle={{
                    background: 'var(--bg-primary)',
                    border: '1px solid var(--border-color)',
                    borderRadius: 8,
                    fontSize: 12,
                  }}
                  formatter={(value) => formatNumber(Number(value))}
                />
                <Bar dataKey="request_count" name={t('analytics.charts.requests_axis')}>
                  {byClient.slice(0, 10).map((_, i) => (
                    <Cell key={i} fill={COLORS.bars[i % COLORS.bars.length]} />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          )}
        </section>
      </div>

      <section className={styles.card}>
        <div className={styles.cardHeader}>
          <h2 className={styles.cardTitle}>{t('analytics.quota_lines.title')}</h2>
          <p className={styles.cardHint}>{t('analytics.quota_lines.hint')}</p>
        </div>
        {providerSeries.length === 0 ? (
          <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
        ) : (
          <div className={styles.providerGrid}>
            {providerSeries.map((series) => (
              <ProviderQuotaChart
                key={series.provider}
                series={series}
                range={range}
                show429Dots={show429Dots}
                showResetMarkers={showResetMarkers}
              />
            ))}
          </div>
        )}
      </section>

      <section className={styles.card}>
        <div className={styles.cardHeader}>
          <h2 className={styles.cardTitle}>{t('analytics.prices.title')}</h2>
          <p className={styles.cardHint}>{t('analytics.prices.hint')}</p>
        </div>
        {tokenPrices.length === 0 ? (
          <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
        ) : (
          <div className={styles.tableWrap}>
            <table className={styles.dataTable}>
              <thead>
                <tr>
                  <th>{t('analytics.prices.col_date')}</th>
                  <th>{t('analytics.prices.col_provider')}</th>
                  <th>{t('analytics.prices.col_model')}</th>
                  <th>{t('analytics.prices.col_token_type')}</th>
                  <th>{t('analytics.prices.col_price')}</th>
                  <th>{t('analytics.prices.col_status')}</th>
                  <th>{t('analytics.prices.col_equations')}</th>
                  <th>{t('analytics.prices.col_residual')}</th>
                  <th>{t('analytics.prices.col_solved_at')}</th>
                </tr>
              </thead>
              <tbody>
                {tokenPrices.map((row) => {
                  const statusKey = priceStatusKey(row.status);
                  return (
                    <tr key={`${row.price_date}|${row.provider}|${row.model}|${row.token_type}`}>
                      <td className={styles.mono}>{row.price_date}</td>
                      <td>{row.provider}</td>
                      <td>{row.model}</td>
                      <td className={styles.mono}>{t(`analytics.prices.token_type.${row.token_type}`, row.token_type)}</td>
                      <td className={styles.mono}>
                        {row.price_points_per_million == null ? '—' : formatNumber(row.price_points_per_million)}
                      </td>
                      <td>
                        <span className={`${styles.statusBadge} ${styles[`status_${statusKey}`] || ''}`}>
                          {t(`analytics.prices.status.${statusKey}`, row.status || '—')}
                        </span>
                      </td>
                      <td className={styles.mono}>{row.equation_count}</td>
                      <td className={styles.mono}>{formatNumber(row.residual_rms)}</td>
                      <td className={styles.mono}>{formatDateTime(row.solved_at)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function ProviderQuotaChart(props: {
  series: ProviderQuotaSeries;
  range: RangeKey;
  show429Dots: boolean;
  showResetMarkers: boolean;
}) {
  const { t } = useTranslation();
  const data = props.series.points.map((p) => ({
    ...p,
    label: formatTimestamp(p.hour_ts, props.range),
  }));
  const eventData = props.show429Dots
    ? data.filter((p) => p.quota_events_count > 0).map((p) => ({ ...p, eventDot: p.quota_remaining_points }))
    : [];
  const resetData = props.showResetMarkers
    ? (props.series.reset_markers || []).map((p) => ({ hour_ts: p.reset_at, resetDot: p.points }))
    : [];

  return (
    <div className={styles.providerCard}>
      <div className={styles.providerHeader}>
        <div>
          <h3 className={styles.providerTitle}>{props.series.provider}</h3>
          <p className={styles.cardHint}>{props.series.window_type || t('analytics.quota_lines.no_window')}</p>
        </div>
        <div className={styles.providerStat}>
          <span>{t('analytics.quota_lines.weekly_capacity')}</span>
          <strong>
            {props.series.million_tokens_for_100_percent_quota > 0
              ? formatNumber(props.series.million_tokens_for_100_percent_quota)
              : '—'}{' '}
            M
          </strong>
        </div>
      </div>
      {data.length === 0 ? (
        <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
      ) : (
        <ResponsiveContainer width="100%" height={260}>
          <ComposedChart data={data} margin={{ top: 10, right: 18, left: 0, bottom: 0 }}>
            <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" />
            <XAxis
              dataKey="hour_ts"
              type="number"
              domain={['dataMin', 'dataMax']}
              stroke={COLORS.axis}
              fontSize={11}
              tickFormatter={(value) => formatTimestamp(Number(value), props.range)}
            />
            <YAxis domain={[0, 10000]} stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
            <Tooltip
              contentStyle={{
                background: 'var(--bg-primary)',
                border: '1px solid var(--border-color)',
                borderRadius: 8,
                fontSize: 12,
              }}
              formatter={(value, name) => [formatNumber(Number(value)), String(name)]}
              labelFormatter={(value) => formatDateTime(Number(value))}
            />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Line
              type="monotone"
              dataKey="quota_remaining_points"
              name={t('analytics.quota_lines.quota_remaining')}
              stroke={COLORS.quota}
              dot={false}
              strokeWidth={2}
            />
            <Line
              type="monotone"
              dataKey="cliproxy_cumulative_points"
              name={t('analytics.quota_lines.cliproxy_cumulative')}
              stroke={COLORS.cumulative}
              dot={false}
              strokeWidth={2}
            />
            <Scatter
              data={eventData}
              dataKey="eventDot"
              name={t('analytics.quota_lines.quota_429')}
              fill={COLORS.error}
              shape="circle"
            />
            <Scatter
              data={resetData}
              dataKey="resetDot"
              name={t('analytics.quota_lines.expected_refresh')}
              fill={COLORS.success}
              shape="circle"
            />
          </ComposedChart>
        </ResponsiveContainer>
      )}
    </div>
  );
}

function StatCard(props: {
  label: string;
  value: string;
  sublabel?: string;
  accent: 'primary' | 'success' | 'error';
}) {
  return (
    <div className={`${styles.statCard} ${styles[`accent_${props.accent}`]}`}>
      <span className={styles.statLabel}>{props.label}</span>
      <span className={styles.statValue}>{props.value}</span>
      {props.sublabel && <span className={styles.statSub}>{props.sublabel}</span>}
    </div>
  );
}
