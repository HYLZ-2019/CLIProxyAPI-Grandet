import { apiClient } from './client';

export interface AnalyticsSummary {
  requests: number;
  success_count: number;
  error_count: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  total_tokens: number;
}

export interface HourlyAggregateRow {
  hour_ts: number;
  bucket_ts?: number;
  bucket_seconds?: number;
  client_key_id: number;
  provider: string;
  auth_id: string;
  model: string;
  request_count: number;
  success_count: number;
  error_count: number;
  input_tokens_sum: number;
  output_tokens_sum: number;
  cached_tokens_sum: number;
  reasoning_tokens_sum?: number;
  cache_read_tokens_sum?: number;
  cache_creation_tokens_sum?: number;
  total_tokens_sum: number;
}

export interface ByModelRow {
  model: string;
  provider: string;
  request_count: number;
  success_count: number;
  error_count: number;
  input_tokens_sum: number;
  output_tokens_sum: number;
  cached_tokens_sum: number;
  reasoning_tokens_sum?: number;
  cache_read_tokens_sum?: number;
  cache_creation_tokens_sum?: number;
  total_tokens_sum: number;
}

export interface ByClientRow {
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
}

export interface QuotaEventRow {
  id: number;
  ts: number;
  provider: string;
  auth_id: string;
  model: string;
  reset_at: number;
}

export interface QuotaSnapshotRow {
  id: number;
  ts: number;
  provider: string;
  auth_id: string;
  window_type: string;
  used_percent: number;
  reset_at: number;
}

export interface TokenPriceRow {
  price_date?: string;
  provider: string;
  auth_id: string;
  model: string;
  token_type: string;
  price_usd_per_million: number | null;
  status: string;
  source: string;
  source_from_ts?: number;
  source_to_ts?: number;
}

export interface ProviderQuotaLinePoint {
  hour_ts: number;
  bucket_ts?: number;
  bucket_seconds?: number;
  quota_remaining_percent: number;
  quota_used_percent: number;
  cliproxy_hour_usd: number;
  cliproxy_cumulative_usd: number;
  quota_events_count: number;
}

export interface ProviderQuotaResetMarker {
  reset_at: number;
  points: number;
}

export interface ProviderQuotaSeries {
  provider: string;
  auth_id: string;
  window_type: string;
  most_expensive_usd_per_million: number;
  input_usd_per_million: number;
  input_price_model: string;
  estimated_quota_usd: number;
  points: ProviderQuotaLinePoint[];
  reset_markers: ProviderQuotaResetMarker[];
}

export interface ProviderQuotaLinesResponse {
  series: ProviderQuotaSeries[];
}

export interface AnalyticsConfig {
  enabled: boolean;
  'raw-log-retention-days': number;
}

type Range = { from?: number; to?: number };
type QuotaWindowClass = '5h' | '7d';

const path = '/analytics';

export const analyticsApi = {
  getSummary: (r: Range = {}) =>
    apiClient.get<AnalyticsSummary>(`${path}/summary`, { params: r }),

  getHourly: (r: Range = {}) =>
    apiClient.get<HourlyAggregateRow[] | null>(`${path}/hourly`, { params: r }),

  getByModel: (r: Range = {}) =>
    apiClient.get<ByModelRow[] | null>(`${path}/by-model`, { params: r }),

  getByClient: (r: Range = {}) =>
    apiClient.get<ByClientRow[] | null>(`${path}/by-client`, { params: r }),

  getQuotaEvents: (limit = 100) =>
    apiClient.get<QuotaEventRow[] | null>(`${path}/quota-events`, { params: { limit } }),

  getQuotaSnapshots: (provider = '', limit = 200) =>
    apiClient.get<QuotaSnapshotRow[] | null>(`${path}/quota-snapshots`, {
      params: provider ? { provider, limit } : { limit },
    }),

  getTokenPrices: (r: Range = {}) =>
    apiClient.get<TokenPriceRow[] | null>(`${path}/token-prices`, { params: r }),

  getProviderQuotaLines: (
    r: Range & { reset_on_429?: boolean; reset_on_refresh?: boolean; window?: QuotaWindowClass } = {},
  ) => apiClient.get<ProviderQuotaLinesResponse>(`${path}/provider-quota-lines`, { params: r }),

  getConfig: () => apiClient.get<AnalyticsConfig>(`${path}/config`),

  updateConfig: (cfg: Partial<AnalyticsConfig>) =>
    apiClient.put<AnalyticsConfig>(`${path}/config`, cfg),
};
