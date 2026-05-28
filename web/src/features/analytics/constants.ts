import type { QuickRangeKey, TrendSeriesVisibility } from './types';

export const DAY_SECONDS = 24 * 60 * 60;

export const QUICK_RANGE_SECONDS: Record<QuickRangeKey, number> = {
  '24h': DAY_SECONDS,
  '7d': 7 * DAY_SECONDS,
};

export const DEFAULT_TREND_SERIES_VISIBILITY: TrendSeriesVisibility = {
  requests: true,
  inputTokens: true,
  outputTokens: true,
  cachedTokens: true,
};

export const COLORS = {
  primary: '#8b8680',
  success: '#10b981',
  error: '#c65746',
  axis: '#9ca3af',
  grid: 'rgba(150,150,150,0.18)',
  quota: '#7a6d9a',
  cumulative: '#5b8a72',
  inputTokens: '#5d8aa8',
  outputTokens: '#c1834d',
  cachedTokens: '#7a6d9a',
  bars: ['#8b8680', '#5b8a72', '#c1834d', '#7a6d9a', '#a86c6c', '#5d8aa8', '#9d8d62', '#6c8e9d'],
};
