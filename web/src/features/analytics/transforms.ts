import type { ByClientRow, ByModelRow, HourlyAggregateRow, ProviderQuotaSeries, TokenPriceRow } from '@/services/api';
import { DAY_SECONDS } from './constants';
import type {
  ClientChartPoint,
  PriceAuthOption,
  ProviderQuotaTooltipPoint,
  QuotaAuthOption,
  QuotaWindowClass,
  TokenBreakdownPoint,
  TrendPoint,
  UsageWindow,
} from './types';

export function floorToBucket(ts: number, bucketSeconds: number): number {
  return ts - (ts % bucketSeconds);
}

export function trendBucketSeconds(window?: UsageWindow | null): number {
  const spanSeconds = window && window.to > window.from ? window.to - window.from : DAY_SECONDS;
  return spanSeconds <= DAY_SECONDS ? 5 * 60 : 60 * 60;
}

export function aggregateByBucket(rows: HourlyAggregateRow[], window: UsageWindow | null): TrendPoint[] {
  if (rows.length === 0) return [];

  const bucketSeconds = rows.find((r) => r.bucket_seconds)?.bucket_seconds || trendBucketSeconds(window);
  const byBucket = new Map<number, TrendPoint>();

  if (window && window.to > window.from) {
    const start = floorToBucket(window.from, bucketSeconds);
    for (let bucket = start; bucket < window.to; bucket += bucketSeconds) {
      byBucket.set(bucket, {
        x_ts: bucket,
        requests: 0,
        totalTokens: 0,
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
        errors: 0,
      });
    }
  }

  for (const r of rows) {
    const xTs = r.bucket_ts ?? r.hour_ts;
    const cur = byBucket.get(xTs) || {
      x_ts: xTs,
      requests: 0,
      totalTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      cachedTokens: 0,
      errors: 0,
    };
    cur.requests += r.request_count;
    cur.totalTokens += r.total_tokens_sum;
    cur.inputTokens += r.input_tokens_sum;
    cur.outputTokens += r.output_tokens_sum;
    cur.cachedTokens += r.cached_tokens_sum;
    cur.errors += r.error_count;
    byBucket.set(xTs, cur);
  }

  return Array.from(byBucket.values()).sort((a, b) => a.x_ts - b.x_ts);
}

export function priceStatusKey(status: string): string {
  return status.trim().toLowerCase().replace(/[^a-z0-9_]+/g, '_') || 'unknown';
}

export function quotaAuthKey(series: Pick<ProviderQuotaSeries, 'provider' | 'auth_id'>): string {
  return `${series.provider}|${series.auth_id || ''}`;
}

export function quotaSeriesKey(series: Pick<ProviderQuotaSeries, 'provider' | 'auth_id' | 'window_type'>): string {
  return `${quotaAuthKey(series)}|${series.window_type || ''}`;
}

export function quotaWindowClass(windowType: string): QuotaWindowClass | '' {
  const value = windowType.trim().toLowerCase();
  if (!value) return '';
  if (value.includes('five_hour') || value.includes('fivehour') || value.includes('five-hour')) return '5h';
  if (value.includes('seven_day') || value.includes('sevenday') || value.includes('seven-day') || value.includes('weekly')) return '7d';
  return '';
}

export function isQuotaSeriesForWindow(series: Pick<ProviderQuotaSeries, 'window_type'>, expectedWindow: QuotaWindowClass): boolean {
  const actualWindow = quotaWindowClass(series.window_type || '');
  return actualWindow === '' || actualWindow === expectedWindow;
}

export function filterQuotaSeriesForWindow(series: ProviderQuotaSeries[], expectedWindow: QuotaWindowClass): ProviderQuotaSeries[] {
  return series.filter((item) => isQuotaSeriesForWindow(item, expectedWindow));
}

export function priceAuthKey(row: Pick<TokenPriceRow, 'provider' | 'auth_id'>): string {
  return `${row.provider}|${row.auth_id || ''}`;
}

export function modelKey(row: Pick<ByModelRow, 'provider' | 'model'>): string {
  return `${row.provider}|${row.model}`;
}

export function buildPriceAuthOptions(rows: TokenPriceRow[]): PriceAuthOption[] {
  const options = new Map<string, PriceAuthOption>();
  for (const row of rows) {
    const key = priceAuthKey(row);
    if (!options.has(key)) {
      options.set(key, {
        key,
        provider: row.provider,
        authID: row.auth_id || '',
      });
    }
  }
  return Array.from(options.values()).sort(
    (a, b) => a.provider.localeCompare(b.provider) || a.authID.localeCompare(b.authID),
  );
}

export function hasQuotaSeriesData(series: ProviderQuotaSeries): boolean {
  return (
    series.points.some(
      (p) =>
        p.quota_used_percent > 0 ||
        p.cliproxy_cumulative_usd > 0 ||
        p.quota_events_count > 0,
    ) || (series.reset_markers || []).length > 0
  );
}

export function buildQuotaAuthOptions(
  fiveHourSeries: ProviderQuotaSeries[],
  sevenDaySeries: ProviderQuotaSeries[],
): QuotaAuthOption[] {
  const options = new Map<string, QuotaAuthOption>();

  const addSeries = (series: ProviderQuotaSeries, windowClass: QuotaWindowClass) => {
    const key = quotaAuthKey(series);
    const existing = options.get(key);
    const hasData = hasQuotaSeriesData(series);
    const hasPriceMetadata = series.most_expensive_usd_per_million > 0;

    options.set(key, {
      key,
      provider: series.provider,
      authID: series.auth_id || '',
      hasFiveHourData: existing?.hasFiveHourData || (windowClass === '5h' && hasData),
      hasSevenDayData: existing?.hasSevenDayData || (windowClass === '7d' && hasData),
      fiveHourWindowType: existing?.fiveHourWindowType || (windowClass === '5h' ? series.window_type : undefined),
      sevenDayWindowType: existing?.sevenDayWindowType || (windowClass === '7d' ? series.window_type : undefined),
      hasPriceMetadata: existing?.hasPriceMetadata || hasPriceMetadata,
    });
  };

  for (const series of fiveHourSeries) addSeries(series, '5h');
  for (const series of sevenDaySeries) addSeries(series, '7d');

  return Array.from(options.values()).sort(sortQuotaAuthOptions);
}

export function sortQuotaAuthOptions(a: QuotaAuthOption, b: QuotaAuthOption): number {
  const recentScore = Number(b.hasFiveHourData) - Number(a.hasFiveHourData);
  if (recentScore !== 0) return recentScore;

  const sevenDayScore = Number(b.hasSevenDayData) - Number(a.hasSevenDayData);
  if (sevenDayScore !== 0) return sevenDayScore;

  const priceScore = Number(b.hasPriceMetadata) - Number(a.hasPriceMetadata);
  if (priceScore !== 0) return priceScore;

  return a.provider.localeCompare(b.provider) || a.authID.localeCompare(b.authID);
}

export function findQuotaSeries(
  series: ProviderQuotaSeries[],
  selectedKey: string,
  expectedWindow?: QuotaWindowClass,
): ProviderQuotaSeries | null {
  return series.find((item) => quotaAuthKey(item) === selectedKey && (!expectedWindow || isQuotaSeriesForWindow(item, expectedWindow))) || null;
}

export function emptyQuotaSeriesForAuth(option: QuotaAuthOption, windowClass: QuotaWindowClass): ProviderQuotaSeries {
  return {
    provider: option.provider,
    auth_id: option.authID,
    window_type: windowClass,
    most_expensive_usd_per_million: 0,
    input_usd_per_million: 0,
    input_price_model: '',
    estimated_quota_usd: 0,
    points: [],
    reset_markers: [],
  };
}

export function buildModelTokenBreakdown(
  row: ByModelRow | null,
  labels: {
    input: string;
    output: string;
    cached: string;
    cacheRead: string;
    makeCache: string;
    reasoning: string;
  },
): TokenBreakdownPoint[] {
  if (!row) return [];
  return [
    { key: 'input', label: labels.input, value: row.input_tokens_sum },
    { key: 'output', label: labels.output, value: row.output_tokens_sum },
    { key: 'cached', label: labels.cached, value: row.cached_tokens_sum },
    { key: 'cacheRead', label: labels.cacheRead, value: row.cache_read_tokens_sum || 0 },
    { key: 'makeCache', label: labels.makeCache, value: row.cache_creation_tokens_sum || 0 },
    { key: 'reasoning', label: labels.reasoning, value: row.reasoning_tokens_sum || 0 },
  ].filter((item) => item.value > 0);
}

export function buildClientChartData(rows: ByClientRow[], unattributedLabel: string): ClientChartPoint[] {
  return rows.slice(0, 10).map((row) => ({
    ...row,
    label:
      row.client_key_id === 0
        ? unattributedLabel
        : row.client_key_label || row.client_key_name || `#${row.client_key_id}`,
  }));
}

export function toQuotaTooltipData(series: ProviderQuotaSeries): ProviderQuotaTooltipPoint[] {
  return series.points.map((point) => ({
    ...point,
    x_ts: point.bucket_ts ?? point.hour_ts,
    estimated_quota_usd_point:
      typeof point.estimated_quota_usd_point === 'number' ? point.estimated_quota_usd_point : null,
  }));
}

export function isQuotaBucketPoint(point?: ProviderQuotaTooltipPoint): boolean {
  return (
    point?.bucket_seconds !== undefined ||
    point?.quota_used_percent !== undefined ||
    point?.cliproxy_cumulative_usd !== undefined ||
    point?.estimated_quota_usd_point !== undefined
  );
}

export function findNearestQuotaPoint(
  data: ProviderQuotaTooltipPoint[],
  ts: number,
): ProviderQuotaTooltipPoint | undefined {
  if (!Number.isFinite(ts) || ts <= 0) return undefined;
  let best: ProviderQuotaTooltipPoint | undefined;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const point of data) {
    const pointTS = Number(point.x_ts ?? 0);
    if (!Number.isFinite(pointTS) || pointTS <= 0) continue;
    const distance = Math.abs(pointTS - ts);
    if (distance < bestDistance) {
      best = point;
      bestDistance = distance;
    }
  }
  return best;
}

export function findNearestQuotaEstimatePoint(
  data: ProviderQuotaTooltipPoint[],
  ts: number,
): ProviderQuotaTooltipPoint | undefined {
  if (!Number.isFinite(ts) || ts <= 0) return undefined;
  let best: ProviderQuotaTooltipPoint | undefined;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const point of data) {
    const pointTS = Number(point.x_ts ?? 0);
    const estimate = point.estimated_quota_usd_point;
    if (!Number.isFinite(pointTS) || pointTS <= 0) continue;
    if (typeof estimate !== 'number' || !Number.isFinite(estimate) || estimate <= 0) continue;
    const distance = Math.abs(pointTS - ts);
    if (distance < bestDistance) {
      best = point;
      bestDistance = distance;
    }
  }
  return best;
}
