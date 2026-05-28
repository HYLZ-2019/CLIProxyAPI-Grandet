import type { ProviderQuotaSeries } from '@/services/api';

export type QuickRangeKey = '24h' | '7d';

export type UsageWindow = {
  from: number;
  to: number;
};

export type TrendSeriesVisibility = {
  requests: boolean;
  inputTokens: boolean;
  outputTokens: boolean;
  cachedTokens: boolean;
};

export type QuotaWindowClass = '5h' | '7d';

export type QuotaAuthOption = {
  key: string;
  provider: string;
  authID: string;
  hasFiveHourData: boolean;
  hasSevenDayData: boolean;
  fiveHourWindowType?: string;
  sevenDayWindowType?: string;
  hasPriceMetadata: boolean;
};

export type PriceAuthOption = {
  key: string;
  provider: string;
  authID: string;
};

export type TrendPoint = {
  x_ts: number;
  requests: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  errors: number;
};

export type TokenBreakdownPoint = {
  key: string;
  label: string;
  value: number;
};

export type ClientChartPoint = {
  client_key_id: number;
  client_key_name?: string;
  client_key_label?: string;
  request_count: number;
  success_count: number;
  error_count: number;
  input_tokens_sum: number;
  output_tokens_sum: number;
  cached_tokens_sum: number;
  total_tokens_sum: number;
  label: string;
};

export type ProviderQuotaTooltipPoint = {
  x_ts?: number;
  bucket_seconds?: number;
  quota_used_percent?: number;
  cliproxy_hour_usd?: number;
  cliproxy_cumulative_usd?: number;
  quota_events_count?: number;
  eventDot?: number;
  resetDot?: number;
};

export type ProviderQuotaTooltipPayload = {
  dataKey?: string | number;
  payload?: ProviderQuotaTooltipPoint;
};

export type SelectedQuotaSeries = {
  fiveHour: ProviderQuotaSeries | null;
  sevenDay: ProviderQuotaSeries | null;
};
