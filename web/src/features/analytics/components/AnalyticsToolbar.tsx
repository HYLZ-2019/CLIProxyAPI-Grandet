import type { Dispatch, SetStateAction } from 'react';
import { useTranslation } from 'react-i18next';
import { formatDateTimeLocal, parseDateTimeLocal } from '../formatters';
import type { QuickRangeKey } from '../types';
import styles from '../Analytics.module.scss';

export function AnalyticsToolbar(props: {
  fromTS: number;
  toTS: number;
  setFromTS: Dispatch<SetStateAction<number>>;
  setToTS: Dispatch<SetStateAction<number>>;
  applyQuickRange: (key: QuickRangeKey) => void;
  loading: boolean;
  show429Dots: boolean;
  setShow429Dots: Dispatch<SetStateAction<boolean>>;
  showResetMarkers: boolean;
  setShowResetMarkers: Dispatch<SetStateAction<boolean>>;
  resetOn429: boolean;
  setResetOn429: Dispatch<SetStateAction<boolean>>;
  resetOnRefresh: boolean;
  setResetOnRefresh: Dispatch<SetStateAction<boolean>>;
}) {
  const { t } = useTranslation();

  return (
    <div className={styles.toolbar}>
      <div className={styles.timeRangeControls}>
        <label className={styles.timeInputLabel}>
          <span>{t('analytics.range.from')}</span>
          <input
            className={styles.timeInput}
            type="datetime-local"
            value={formatDateTimeLocal(props.fromTS)}
            onChange={(event) => {
              const next = parseDateTimeLocal(event.target.value);
              if (next !== null) props.setFromTS(next);
            }}
          />
        </label>
        <label className={styles.timeInputLabel}>
          <span>{t('analytics.range.to')}</span>
          <input
            className={styles.timeInput}
            type="datetime-local"
            value={formatDateTimeLocal(props.toTS)}
            onChange={(event) => {
              const next = parseDateTimeLocal(event.target.value);
              if (next !== null) props.setToTS(next);
            }}
          />
        </label>
        <div className={styles.rangeGroup}>
          {(['24h', '7d'] as QuickRangeKey[]).map((key) => (
            <button key={key} type="button" className={styles.rangeBtn} onClick={() => props.applyQuickRange(key)}>
              {t(`analytics.range.quick_${key}`)}
            </button>
          ))}
        </div>
      </div>
      <div className={styles.toggleGroup}>
        <button
          type="button"
          aria-pressed={props.show429Dots}
          className={`${styles.toggleBtn} ${props.show429Dots ? styles.toggleBtnActive : ''}`}
          onClick={() => props.setShow429Dots((value) => !value)}
        >
          {props.show429Dots ? t('analytics.quota_lines.hide_429') : t('analytics.quota_lines.show_429')}
        </button>
        <button
          type="button"
          aria-pressed={props.showResetMarkers}
          className={`${styles.toggleBtn} ${props.showResetMarkers ? styles.toggleBtnActive : ''}`}
          onClick={() => props.setShowResetMarkers((value) => !value)}
        >
          {props.showResetMarkers
            ? t('analytics.quota_lines.hide_refresh_markers')
            : t('analytics.quota_lines.show_refresh_markers')}
        </button>
        <button
          type="button"
          aria-pressed={props.resetOn429}
          className={`${styles.toggleBtn} ${props.resetOn429 ? styles.toggleBtnActive : ''}`}
          onClick={() => props.setResetOn429((value) => !value)}
        >
          {t('analytics.quota_lines.reset_on_429')}
        </button>
        <button
          type="button"
          aria-pressed={props.resetOnRefresh}
          className={`${styles.toggleBtn} ${props.resetOnRefresh ? styles.toggleBtnActive : ''}`}
          onClick={() => props.setResetOnRefresh((value) => !value)}
        >
          {t('analytics.quota_lines.reset_on_refresh')}
        </button>
      </div>
      {props.loading && <span className={styles.loadingTag}>{t('common.loading')}</span>}
    </div>
  );
}
