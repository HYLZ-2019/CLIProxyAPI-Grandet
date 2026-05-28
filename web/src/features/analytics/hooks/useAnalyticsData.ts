import { useCallback, useEffect, useState } from 'react';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import {
  analyticsApi,
  type AnalyticsSummary,
  type ByClientRow,
  type ByModelRow,
  type HourlyAggregateRow,
  type ProviderQuotaSeries,
  type TokenPriceRow,
} from '@/services/api';
import { filterQuotaSeriesForWindow } from '../transforms';
import type { UsageWindow } from '../types';

type Options = {
  disconnected: boolean;
  usageWindow: UsageWindow;
  isInvalidRange: boolean;
  invalidRangeMessage: string;
  loadFailedMessage: string;
};

export function useAnalyticsData({
  disconnected,
  usageWindow,
  isInvalidRange,
  invalidRangeMessage,
  loadFailedMessage,
}: Options) {
  const [summary, setSummary] = useState<AnalyticsSummary | null>(null);
  const [hourly, setHourly] = useState<HourlyAggregateRow[]>([]);
  const [byModel, setByModel] = useState<ByModelRow[]>([]);
  const [byClient, setByClient] = useState<ByClientRow[]>([]);
  const [quotaSeries5h, setQuotaSeries5h] = useState<ProviderQuotaSeries[]>([]);
  const [quotaSeries7d, setQuotaSeries7d] = useState<ProviderQuotaSeries[]>([]);
  const [tokenPrices, setTokenPrices] = useState<TokenPriceRow[]>([]);
  const [resetOn429, setResetOn429] = useState(false);
  const [resetOnRefresh, setResetOnRefresh] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [notEnabled, setNotEnabled] = useState(false);

  const refresh = useCallback(async () => {
    if (disconnected) return;

    setError('');
    setNotEnabled(false);

    if (isInvalidRange) {
      setLoading(false);
      setError(invalidRangeMessage);
      return;
    }

    setLoading(true);

    const { from, to } = usageWindow;
    const [summaryResult, hourlyResult, byModelResult, byClientResult, quota5hResult, quota7dResult, tokenPricesResult] =
      await Promise.allSettled([
        analyticsApi.getSummary({ from, to }),
        analyticsApi.getHourly({ from, to }),
        analyticsApi.getByModel({ from, to }),
        analyticsApi.getByClient({ from, to }),
        analyticsApi.getProviderQuotaLines({
          from,
          to,
          reset_on_429: resetOn429,
          reset_on_refresh: resetOnRefresh,
          window: '5h',
        }),
        analyticsApi.getProviderQuotaLines({
          from,
          to,
          reset_on_429: resetOn429,
          reset_on_refresh: resetOnRefresh,
          window: '7d',
        }),
        analyticsApi.getTokenPrices({ from, to }),
      ]);

    const results = [
      summaryResult,
      hourlyResult,
      byModelResult,
      byClientResult,
      quota5hResult,
      quota7dResult,
      tokenPricesResult,
    ];
    const allDisabled = results.every(
      (result) => result.status === 'rejected' && (result.reason as { status?: number })?.status === 503,
    );
    if (allDisabled) {
      setNotEnabled(true);
      setLoading(false);
      return;
    }

    const firstErr = results.find((result) => result.status === 'rejected') as PromiseRejectedResult | undefined;
    if (firstErr && results.every((result) => result.status === 'rejected')) {
      setError((firstErr.reason as Error)?.message || loadFailedMessage);
      setLoading(false);
      return;
    }

    if (summaryResult.status === 'fulfilled') setSummary(summaryResult.value);
    if (hourlyResult.status === 'fulfilled') setHourly(hourlyResult.value || []);
    if (byModelResult.status === 'fulfilled') setByModel(byModelResult.value || []);
    if (byClientResult.status === 'fulfilled') setByClient(byClientResult.value || []);
    if (quota5hResult.status === 'fulfilled') setQuotaSeries5h(filterQuotaSeriesForWindow(quota5hResult.value.series || [], '5h'));
    if (quota7dResult.status === 'fulfilled') setQuotaSeries7d(filterQuotaSeriesForWindow(quota7dResult.value.series || [], '7d'));
    if (tokenPricesResult.status === 'fulfilled') setTokenPrices(tokenPricesResult.value || []);

    setLoading(false);
  }, [
    disconnected,
    invalidRangeMessage,
    isInvalidRange,
    loadFailedMessage,
    resetOn429,
    resetOnRefresh,
    usageWindow,
  ]);

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      void refresh();
    }, 0);
    return () => window.clearTimeout(timeout);
  }, [refresh]);

  useHeaderRefresh(refresh);

  return {
    summary,
    hourly,
    byModel,
    byClient,
    quotaSeries5h,
    quotaSeries7d,
    tokenPrices,
    resetOn429,
    setResetOn429,
    resetOnRefresh,
    setResetOnRefresh,
    loading,
    error,
    notEnabled,
    refresh,
  };
}
