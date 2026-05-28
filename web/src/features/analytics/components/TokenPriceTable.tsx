import { Dispatch, SetStateAction } from 'react';
import { useTranslation } from 'react-i18next';
import type { TokenPriceRow } from '@/services/api';
import { formatNumber } from '../formatters';
import { priceStatusKey } from '../transforms';
import type { PriceAuthOption } from '../types';
import styles from '../Analytics.module.scss';

export function TokenPriceTable(props: {
  tokenPrices: TokenPriceRow[];
  filteredTokenPrices: TokenPriceRow[];
  priceAuthOptions: PriceAuthOption[];
  selectedPriceAuthKey: string;
  setSelectedPriceAuthKey: Dispatch<SetStateAction<string>>;
}) {
  const { t } = useTranslation();

  return (
    <section className={styles.card}>
      <div className={styles.cardHeaderWithAction}>
        <div className={styles.cardHeaderText}>
          <h2 className={styles.cardTitle}>{t('analytics.prices.title')}</h2>
          <p className={styles.cardHint}>{t('analytics.prices.hint')}</p>
        </div>
        <label className={styles.quotaSelectLabel}>
          <span>{t('analytics.prices.select_auth')}</span>
          <select
            className={styles.quotaSelect}
            value={props.selectedPriceAuthKey}
            onChange={(event) => props.setSelectedPriceAuthKey(event.target.value)}
            disabled={props.priceAuthOptions.length === 0}
          >
            {props.priceAuthOptions.length === 0 ? (
              <option value="">{t('analytics.prices.no_auth_options')}</option>
            ) : (
              props.priceAuthOptions.map((option) => (
                <option key={option.key} value={option.key}>
                  {`${option.authID || t('analytics.quota_lines.unknown_auth')} — ${option.provider}`}
                </option>
              ))
            )}
          </select>
        </label>
      </div>
      {props.filteredTokenPrices.length === 0 ? (
        <div className={styles.chartEmpty}>
          {props.tokenPrices.length > 0 ? t('analytics.prices.no_auth_options') : t('analytics.no_data')}
        </div>
      ) : (
        <div className={styles.tableWrap}>
          <table className={styles.dataTable}>
            <thead>
              <tr>
                <th>{t('analytics.prices.col_provider')}</th>
                <th>{t('analytics.prices.col_auth')}</th>
                <th>{t('analytics.prices.col_model')}</th>
                <th>{t('analytics.prices.col_token_type')}</th>
                <th>{t('analytics.prices.col_price_usd')}</th>
                <th>{t('analytics.prices.col_status')}</th>
                <th>{t('analytics.prices.col_source')}</th>
              </tr>
            </thead>
            <tbody>
              {props.filteredTokenPrices.map((row) => {
                const statusKey = priceStatusKey(row.status);
                return (
                  <tr key={`${row.provider}|${row.auth_id}|${row.model}|${row.token_type}`}>
                    <td>{row.provider}</td>
                    <td className={styles.mono}>{row.auth_id || '—'}</td>
                    <td>{row.model}</td>
                    <td className={styles.mono}>{t(`analytics.prices.token_type.${row.token_type}`, row.token_type)}</td>
                    <td className={styles.mono}>
                      {row.price_usd_per_million == null ? '—' : `$${formatNumber(row.price_usd_per_million)}`}
                    </td>
                    <td>
                      <span className={`${styles.statusBadge} ${styles[`status_${statusKey}`] || ''}`}>
                        {t(`analytics.prices.status.${statusKey}`, row.status || '—')}
                      </span>
                    </td>
                    <td className={styles.mono}>{row.source || '—'}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
