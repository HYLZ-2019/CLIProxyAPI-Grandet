import { useMemo, useState } from 'react';
import type { TokenPriceRow } from '@/services/api';
import { DEFAULT_TREND_SERIES_VISIBILITY } from '../constants';
import {
  buildPriceAuthOptions,
  buildQuotaAuthOptions,
  findQuotaSeries,
  priceAuthKey,
} from '../transforms';
import type { TrendSeriesVisibility } from '../types';
import type { ProviderQuotaSeries } from '@/services/api';

export function useAnalyticsSelections(
  quotaSeries5h: ProviderQuotaSeries[],
  quotaSeries7d: ProviderQuotaSeries[],
  tokenPrices: TokenPriceRow[],
) {
  const [show429Dots, setShow429Dots] = useState(true);
  const [showResetMarkers, setShowResetMarkers] = useState(true);
  const [trendSeriesVisibility, setTrendSeriesVisibility] = useState<TrendSeriesVisibility>(
    DEFAULT_TREND_SERIES_VISIBILITY,
  );
  const [expandedModelKey, setExpandedModelKey] = useState('');
  const [selectedQuotaAuthKey, setSelectedQuotaAuthKey] = useState('');
  const [selectedPriceAuthKey, setSelectedPriceAuthKey] = useState('');

  const quotaAuthOptions = useMemo(
    () => buildQuotaAuthOptions(quotaSeries5h, quotaSeries7d),
    [quotaSeries5h, quotaSeries7d],
  );

  const effectiveQuotaAuthKey = quotaAuthOptions.some((option) => option.key === selectedQuotaAuthKey)
    ? selectedQuotaAuthKey
    : quotaAuthOptions[0]?.key || '';

  const selectedQuotaOption = quotaAuthOptions.find((option) => option.key === effectiveQuotaAuthKey) || null;

  const selectedQuotaSeries5h = useMemo(
    () => findQuotaSeries(quotaSeries5h, effectiveQuotaAuthKey, '5h'),
    [quotaSeries5h, effectiveQuotaAuthKey],
  );
  const selectedQuotaSeries7d = useMemo(
    () => findQuotaSeries(quotaSeries7d, effectiveQuotaAuthKey, '7d'),
    [quotaSeries7d, effectiveQuotaAuthKey],
  );

  const priceAuthOptions = useMemo(() => buildPriceAuthOptions(tokenPrices), [tokenPrices]);

  const effectivePriceAuthKey = priceAuthOptions.some((option) => option.key === selectedPriceAuthKey)
    ? selectedPriceAuthKey
    : priceAuthOptions[0]?.key || '';

  const filteredTokenPrices = useMemo(
    () => tokenPrices.filter((row) => !effectivePriceAuthKey || priceAuthKey(row) === effectivePriceAuthKey),
    [effectivePriceAuthKey, tokenPrices],
  );

  return {
    show429Dots,
    setShow429Dots,
    showResetMarkers,
    setShowResetMarkers,
    trendSeriesVisibility,
    setTrendSeriesVisibility,
    expandedModelKey,
    setExpandedModelKey,
    quotaAuthOptions,
    selectedQuotaAuthKey: effectiveQuotaAuthKey,
    selectedQuotaOption,
    setSelectedQuotaAuthKey,
    selectedQuotaSeries5h,
    selectedQuotaSeries7d,
    priceAuthOptions,
    selectedPriceAuthKey: effectivePriceAuthKey,
    setSelectedPriceAuthKey,
    filteredTokenPrices,
  };
}
