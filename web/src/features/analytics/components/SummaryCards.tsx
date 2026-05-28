import { useTranslation } from 'react-i18next';
import type { AnalyticsSummary } from '@/services/api';
import { formatNumber } from '../formatters';
import { StatCard } from './StatCard';
import styles from '../Analytics.module.scss';

export function SummaryCards(props: { summary: AnalyticsSummary | null; quotaEventCount: number }) {
  const { t } = useTranslation();
  const successRate =
    props.summary && props.summary.requests > 0
      ? ((props.summary.success_count / props.summary.requests) * 100).toFixed(1) + '%'
      : '—';

  return (
    <section className={styles.statsGrid}>
      <StatCard
        label={t('analytics.cards.requests')}
        value={props.summary ? formatNumber(props.summary.requests) : '—'}
        accent="primary"
      />
      <StatCard
        label={t('analytics.cards.success_rate')}
        value={successRate}
        accent="success"
        sublabel={props.summary ? `${props.summary.success_count} / ${props.summary.requests}` : ''}
      />
      <StatCard
        label={t('analytics.cards.total_tokens')}
        value={props.summary ? formatNumber(props.summary.total_tokens) : '—'}
        accent="primary"
        sublabel={
          props.summary
            ? `${t('analytics.cards.input_tokens_short')} ${formatNumber(props.summary.input_tokens)} · ${t('analytics.cards.output_tokens_short')} ${formatNumber(props.summary.output_tokens)} · ${t('analytics.cards.cached_tokens_short')} ${formatNumber(props.summary.cached_tokens)}`
            : ''
        }
      />
      <StatCard
        label={t('analytics.cards.errors')}
        value={props.summary ? formatNumber(props.summary.error_count) : '—'}
        accent={props.summary && props.summary.error_count > 0 ? 'error' : 'primary'}
        sublabel={t('analytics.cards.quota_events_count', { count: props.quotaEventCount })}
      />
    </section>
  );
}
