import { Dispatch, SetStateAction, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { ByModelRow } from '@/services/api';
import { COLORS } from '../constants';
import { formatNumber } from '../formatters';
import { buildModelTokenBreakdown, modelKey } from '../transforms';
import styles from '../Analytics.module.scss';

export function TopModelsChart(props: {
  byModel: ByModelRow[];
  expandedModelKey: string;
  setExpandedModelKey: Dispatch<SetStateAction<string>>;
}) {
  const { t } = useTranslation();
  const topModels = useMemo(() => props.byModel.slice(0, 10), [props.byModel]);
  const expandedModel = useMemo(
    () => topModels.find((row) => modelKey(row) === props.expandedModelKey) || null,
    [props.expandedModelKey, topModels],
  );
  const expandedModelBreakdown = useMemo(
    () =>
      buildModelTokenBreakdown(expandedModel, {
        input: t('analytics.charts.token_breakdown_input'),
        output: t('analytics.charts.token_breakdown_output'),
        cached: t('analytics.charts.token_breakdown_cached_input'),
        cacheRead: t('analytics.charts.token_breakdown_cache_read'),
        makeCache: t('analytics.charts.token_breakdown_make_cache'),
        reasoning: t('analytics.charts.token_breakdown_reasoning'),
      }),
    [expandedModel, t],
  );

  return (
    <section className={styles.card}>
      <div className={styles.cardHeader}>
        <h2 className={styles.cardTitle}>{t('analytics.charts.by_model_title')}</h2>
        <p className={styles.cardHint}>{t('analytics.charts.by_model_hint')}</p>
      </div>
      {props.byModel.length === 0 ? (
        <div className={styles.chartEmpty}>{t('analytics.no_data')}</div>
      ) : (
        <>
          <ResponsiveContainer width="100%" height={Math.max(190, topModels.length * 30 + 28)}>
            <BarChart data={topModels} layout="vertical" margin={{ top: 2, right: 18, left: 4, bottom: 2 }}>
              <CartesianGrid stroke={COLORS.grid} strokeDasharray="3 3" horizontal={false} />
              <XAxis type="number" stroke={COLORS.axis} fontSize={11} tickFormatter={formatNumber} />
              <YAxis type="category" dataKey="model" stroke={COLORS.axis} fontSize={11} width={140} />
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
                {topModels.map((row, i) => {
                  const key = modelKey(row);
                  return (
                    <Cell
                      key={key}
                      fill={COLORS.bars[i % COLORS.bars.length]}
                      cursor="pointer"
                      onClick={() => props.setExpandedModelKey((current) => (current === key ? '' : key))}
                    />
                  );
                })}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
          {expandedModel && (
            <div className={styles.modelBreakdownPanel}>
              <div className={styles.modelBreakdownTitle}>
                {t('analytics.charts.token_breakdown_title', { model: expandedModel.model })}
              </div>
              {expandedModelBreakdown.length === 0 ? (
                <div className={styles.chartEmpty}>{t('analytics.charts.token_breakdown_empty')}</div>
              ) : (
                <ResponsiveContainer width="100%" height={Math.max(130, expandedModelBreakdown.length * 28 + 26)}>
                  <BarChart data={expandedModelBreakdown} layout="vertical" margin={{ top: 2, right: 18, left: 4, bottom: 2 }}>
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
                    <Bar dataKey="value" name={t('analytics.charts.tokens_axis')} activeBar={false}>
                      {expandedModelBreakdown.map((item, i) => (
                        <Cell key={item.key} fill={COLORS.bars[(i + 2) % COLORS.bars.length]} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}
