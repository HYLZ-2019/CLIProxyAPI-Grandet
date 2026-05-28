import { useCallback, useMemo, useState } from 'react';
import { DAY_SECONDS, QUICK_RANGE_SECONDS } from '../constants';
import type { QuickRangeKey, UsageWindow } from '../types';

export function useAnalyticsRange() {
  const [fromTS, setFromTS] = useState(() => Math.floor(Date.now() / 1000) - DAY_SECONDS);
  const [toTS, setToTS] = useState(() => Math.floor(Date.now() / 1000));

  const usageWindow: UsageWindow = useMemo(() => ({ from: fromTS, to: toTS }), [fromTS, toTS]);
  const isInvalidRange = fromTS >= toTS;

  const applyQuickRange = useCallback((key: QuickRangeKey) => {
    const to = Math.floor(Date.now() / 1000);
    setFromTS(to - QUICK_RANGE_SECONDS[key]);
    setToTS(to);
  }, []);

  return {
    fromTS,
    toTS,
    setFromTS,
    setToTS,
    usageWindow,
    isInvalidRange,
    applyQuickRange,
  };
}
