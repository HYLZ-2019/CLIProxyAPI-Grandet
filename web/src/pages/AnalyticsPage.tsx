import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  aggregateByBucket,
  AnalyticsToolbar,
  ProviderQuotaSection,
  SummaryCards,
  TokenPriceTable,
  TopClientsChart,
  TopModelsChart,
  UsageTrendChart,
  useAnalyticsData,
  useAnalyticsRange,
  useAnalyticsSelections,
} from '@/features/analytics';
import { useAuthStore } from '@/stores';
import styles from '@/features/analytics/Analytics.module.scss';

export function AnalyticsPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const disconnected = connectionStatus !== 'connected';
  const range = useAnalyticsRange();
  const data = useAnalyticsData({
    disconnected,
    usageWindow: range.usageWindow,
    isInvalidRange: range.isInvalidRange,
    invalidRangeMessage: t('analytics.errors.invalid_time_range'),
    loadFailedMessage: t('analytics.errors.load_failed'),
  });
  const selections = useAnalyticsSelections(data.quotaSeries5h, data.quotaSeries7d, data.tokenPrices);

  const trendData = useMemo(() => aggregateByBucket(data.hourly, range.usageWindow), [data.hourly, range.usageWindow]);
  const quotaEventCount = useMemo(
    () =>
      data.quotaSeries5h.reduce(
        (sum, series) => sum + series.points.reduce((inner, point) => inner + point.quota_events_count, 0),
        0,
      ),
    [data.quotaSeries5h],
  );

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

  if (data.notEnabled) {
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

      <AnalyticsToolbar
        fromTS={range.fromTS}
        toTS={range.toTS}
        setFromTS={range.setFromTS}
        setToTS={range.setToTS}
        applyQuickRange={range.applyQuickRange}
        loading={data.loading}
        show429Dots={selections.show429Dots}
        setShow429Dots={selections.setShow429Dots}
        showResetMarkers={selections.showResetMarkers}
        setShowResetMarkers={selections.setShowResetMarkers}
        resetOn429={data.resetOn429}
        setResetOn429={data.setResetOn429}
        resetOnRefresh={data.resetOnRefresh}
        setResetOnRefresh={data.setResetOnRefresh}
      />

      {data.error && <div className={styles.errorBox}>{data.error}</div>}

      <SummaryCards summary={data.summary} quotaEventCount={quotaEventCount} />
      <UsageTrendChart
        data={trendData}
        usageWindow={range.usageWindow}
        visibility={selections.trendSeriesVisibility}
        setVisibility={selections.setTrendSeriesVisibility}
      />

      <div className={styles.twoCol}>
        <TopModelsChart
          byModel={data.byModel}
          expandedModelKey={selections.expandedModelKey}
          setExpandedModelKey={selections.setExpandedModelKey}
        />
        <TopClientsChart byClient={data.byClient} />
      </div>

      <ProviderQuotaSection
        quotaAuthOptions={selections.quotaAuthOptions}
        selectedQuotaAuthKey={selections.selectedQuotaAuthKey}
        selectedQuotaOption={selections.selectedQuotaOption}
        setSelectedQuotaAuthKey={selections.setSelectedQuotaAuthKey}
        selectedQuotaSeries5h={selections.selectedQuotaSeries5h}
        selectedQuotaSeries7d={selections.selectedQuotaSeries7d}
        usageWindow={range.usageWindow}
        show429Dots={selections.show429Dots}
        showResetMarkers={selections.showResetMarkers}
      />

      <TokenPriceTable
        tokenPrices={data.tokenPrices}
        filteredTokenPrices={selections.filteredTokenPrices}
        priceAuthOptions={selections.priceAuthOptions}
        selectedPriceAuthKey={selections.selectedPriceAuthKey}
        setSelectedPriceAuthKey={selections.setSelectedPriceAuthKey}
      />
    </div>
  );
}
