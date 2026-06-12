import { Dispatch, SetStateAction } from 'react';
import { useTranslation } from 'react-i18next';
import type { ProviderQuotaSeries } from '@/services/api';
import { emptyQuotaSeriesForAuth } from '../transforms';
import type { QuotaAuthOption, QuotaSeriesVisibility, UsageWindow } from '../types';
import { ProviderQuotaChart } from './ProviderQuotaChart';
import styles from '../Analytics.module.scss';

export function ProviderQuotaSection(props: {
  quotaAuthOptions: QuotaAuthOption[];
  selectedQuotaAuthKey: string;
  selectedQuotaOption: QuotaAuthOption | null;
  setSelectedQuotaAuthKey: Dispatch<SetStateAction<string>>;
  selectedQuotaSeries5h: ProviderQuotaSeries | null;
  selectedQuotaSeries7d: ProviderQuotaSeries | null;
  usageWindow: UsageWindow;
  show429Dots: boolean;
  showResetMarkers: boolean;
  quotaSeriesVisibility: QuotaSeriesVisibility;
  setQuotaSeriesVisibility: Dispatch<SetStateAction<QuotaSeriesVisibility>>;
}) {
  const { t } = useTranslation();

  return (
    <section className={styles.card}>
      <div className={styles.cardHeaderWithAction}>
        <div className={styles.cardHeaderText}>
          <h2 className={styles.cardTitle}>{t('analytics.quota_lines.title')}</h2>
          <p className={styles.cardHint}>{t('analytics.quota_lines.hint')}</p>
        </div>
        <div className={styles.quotaHeaderControls}>
          <div className={styles.trendSeriesToggles} aria-label={t('analytics.quota_lines.series_toggles')}>
            {([
              ['quotaUsed', t('analytics.quota_lines.quota_used')],
              ['cumulativeUSD', t('analytics.quota_lines.cliproxy_cumulative')],
              ['estimatedQuotaUSD', t('analytics.quota_lines.estimated_quota_usd_point')],
            ] as const).map(([key, label]) => (
              <label key={key} className={styles.trendSeriesToggle}>
                <input
                  type="checkbox"
                  checked={props.quotaSeriesVisibility[key]}
                  onChange={(event) =>
                    props.setQuotaSeriesVisibility((current) => ({
                      ...current,
                      [key]: event.target.checked,
                    }))
                  }
                />
                <span>{label}</span>
              </label>
            ))}
          </div>
          <label className={styles.quotaSelectLabel}>
            <span>{t('analytics.quota_lines.select_auth')}</span>
            <select
              className={styles.quotaSelect}
              value={props.selectedQuotaAuthKey}
              onChange={(event) => props.setSelectedQuotaAuthKey(event.target.value)}
              disabled={props.quotaAuthOptions.length === 0}
            >
              {props.quotaAuthOptions.length === 0 ? (
                <option value="">{t('analytics.quota_lines.no_auth_options')}</option>
              ) : (
                props.quotaAuthOptions.map((option) => (
                  <option key={option.key} value={option.key}>
                    {`${option.authID || t('analytics.quota_lines.unknown_auth')} — ${option.provider}`}
                  </option>
                ))
              )}
            </select>
          </label>
        </div>
      </div>
      {props.quotaAuthOptions.length === 0 || !props.selectedQuotaOption ? (
        <div className={styles.chartEmpty}>{t('analytics.quota_lines.no_auth_options')}</div>
      ) : (
        <div className={styles.providerGrid}>
          <ProviderQuotaChart
            series={props.selectedQuotaSeries5h || emptyQuotaSeriesForAuth(props.selectedQuotaOption, '5h')}
            displayWindow={props.usageWindow}
            rangeLabel={t('analytics.quota_lines.range_5h')}
            show429Dots={props.show429Dots}
            showResetMarkers={props.showResetMarkers}
            seriesVisibility={props.quotaSeriesVisibility}
          />
          <ProviderQuotaChart
            series={props.selectedQuotaSeries7d || emptyQuotaSeriesForAuth(props.selectedQuotaOption, '7d')}
            displayWindow={props.usageWindow}
            rangeLabel={t('analytics.quota_lines.range_7d')}
            show429Dots={props.show429Dots}
            showResetMarkers={props.showResetMarkers}
            seriesVisibility={props.quotaSeriesVisibility}
          />
        </div>
      )}
    </section>
  );
}
