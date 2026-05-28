import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { ByClientRow } from '@/services/api';
import { COLORS } from '../constants';
import { formatNumber } from '../formatters';
import { buildClientChartData } from '../transforms';
import styles from '../Analytics.module.scss';

export function TopClientsChart(props: { byClient: ByClientRow[] }) {
  const { t } = useTranslation();
  const clientChartData = useMemo(
    () => buildClientChartData(props.byClient, t('analytics.charts.unattributed_client_key')),
    [props.byClient, t],
  );

  return (
    <section className={styles.card}>
      <div className={styles.cardHeader}>
        <h2 className={styles.cardTitle}>{t('analytics.charts.by_client_title')}</h2>
        <p className={styles.cardHint}>{t('analytics.charts.by_client_hint')}</p>
      </div>
      {props.byClient.length === 0 ? (
        <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
      ) : (
        <ResponsiveContainer width="100%" height={Math.max(190, clientChartData.length * 30 + 28)}>
          <BarChart data={clientChartData} layout="vertical" margin={{ top: 2, right: 18, left: 4, bottom: 2 }}>
            <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" horizontal={false} />
            <XAxis type="number" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
            <YAxis type="category" dataKey="label" stroke={COLORS.axis} fontSize={11} width={130} />
            <Tooltip
              contentStyle={{
                background: 'var(--bg-primary)',
                border: '1px solid var(--border-color)',
                borderRadius: 8,
                fontSize: 12,
              }}
              formatter={(value) => formatNumber(Number(value))}
            />
            <Bar dataKey="request_count" name={t('analytics.charts.requests_axis')} activeBar={false}>
              {clientChartData.map((row, i) => (
                <Cell key={`${row.client_key_id}|${row.label}`} fill={COLORS.bars[i % COLORS.bars.length]} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
    </section>
  );
}
