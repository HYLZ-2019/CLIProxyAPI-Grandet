import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { QuotaEventRow } from '@/services/api';
import styles from './QuotaHeatmap.module.scss';

interface Props {
  provider: string;
  windowType: string;
  events: QuotaEventRow[];
}

interface Bucket {
  row: number;
  col: number;
  count: number;
  label: string;
}

interface Layout {
  rows: number;
  cols: number;
  buckets: Bucket[];
  rowLabels?: string[];
  colLabels?: Array<{ col: number; label: string }>;
}

const SECONDS_PER_DAY = 86400;

function level(count: number): 0 | 1 | 2 | 3 | 4 {
  if (count === 0) return 0;
  if (count >= 10) return 4;
  if (count >= 4) return 3;
  if (count >= 2) return 2;
  return 1;
}

// Count events whose timestamp falls in [startTs, endTs).
function countInRange(events: QuotaEventRow[], startTs: number, endTs: number): number {
  let n = 0;
  for (const e of events) {
    if (e.ts >= startTs && e.ts < endTs) n++;
  }
  return n;
}

// Build a date-only Date (local-midnight) for a given offset from `today`.
function dayAtOffset(today: Date, dayOffset: number): Date {
  const d = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  d.setDate(d.getDate() + dayOffset);
  return d;
}

function buildFiveHourLayout(events: QuotaEventRow[], today: Date): Layout {
  const days = 14;
  const rows = 5; // slots: 00–05, 05–10, 10–15, 15–20, 20–24
  const cols = days;
  const buckets: Bucket[] = [];
  const colLabels: Array<{ col: number; label: string }> = [];

  for (let col = 0; col < cols; col++) {
    const dayStart = dayAtOffset(today, -(cols - 1 - col));
    if (col === 0 || col === cols - 1 || col === Math.floor(cols / 2)) {
      colLabels.push({
        col,
        label: dayStart.toLocaleDateString([], { month: 'short', day: 'numeric' }),
      });
    }
    for (let row = 0; row < rows; row++) {
      const slotStart = new Date(dayStart);
      slotStart.setHours(row * 5, 0, 0, 0);
      const slotEnd = new Date(dayStart);
      // Last slot is 20:00–24:00 (4 hours), others are 5 hours.
      slotEnd.setHours(row === 4 ? 24 : row * 5 + 5, 0, 0, 0);

      const startTs = Math.floor(slotStart.getTime() / 1000);
      const endTs = Math.floor(slotEnd.getTime() / 1000);
      const count = countInRange(events, startTs, endTs);

      const startH = row * 5;
      const endH = row === 4 ? 24 : row * 5 + 5;
      const label = `${dayStart.toLocaleDateString([], {
        month: 'short',
        day: 'numeric',
      })} ${String(startH).padStart(2, '0')}:00–${String(endH).padStart(2, '0')}:00 · ${count} × 429`;
      buckets.push({ row, col, count, label });
    }
  }

  return {
    rows,
    cols,
    buckets,
    rowLabels: ['00–05', '05–10', '10–15', '15–20', '20–24'],
    colLabels,
  };
}

function buildWeeklyLayout(events: QuotaEventRow[], today: Date): Layout {
  const weeks = 26;
  const cols = weeks;
  const buckets: Bucket[] = [];
  const colLabels: Array<{ col: number; label: string }> = [];

  // Find this week's Monday at local midnight (ISO week start).
  const dayOfWeek = today.getDay() || 7; // Sun -> 7
  const thisMonday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  thisMonday.setDate(thisMonday.getDate() - (dayOfWeek - 1));

  for (let col = 0; col < cols; col++) {
    const weekStart = new Date(thisMonday);
    weekStart.setDate(weekStart.getDate() - (cols - 1 - col) * 7);
    const weekEnd = new Date(weekStart);
    weekEnd.setDate(weekEnd.getDate() + 7);

    if (col === 0 || col === cols - 1 || col % 4 === 0) {
      colLabels.push({
        col,
        label: weekStart.toLocaleDateString([], { month: 'short', day: 'numeric' }),
      });
    }
    const startTs = Math.floor(weekStart.getTime() / 1000);
    const endTs = Math.floor(weekEnd.getTime() / 1000);
    const count = countInRange(events, startTs, endTs);
    const label = `${weekStart.toLocaleDateString([], {
      month: 'short',
      day: 'numeric',
    })} – ${new Date(weekEnd.getTime() - SECONDS_PER_DAY * 1000).toLocaleDateString([], {
      month: 'short',
      day: 'numeric',
    })} · ${count} × 429`;
    buckets.push({ row: 0, col, count, label });
  }

  return { rows: 1, cols, buckets, colLabels };
}

function buildDailyLayout(events: QuotaEventRow[], today: Date): Layout {
  const days = 30;
  const cols = days;
  const buckets: Bucket[] = [];
  const colLabels: Array<{ col: number; label: string }> = [];

  for (let col = 0; col < cols; col++) {
    const dayStart = dayAtOffset(today, -(cols - 1 - col));
    const dayEnd = dayAtOffset(today, -(cols - 1 - col) + 1);

    if (col === 0 || col === cols - 1 || col % 5 === 0) {
      colLabels.push({
        col,
        label: dayStart.toLocaleDateString([], { month: 'short', day: 'numeric' }),
      });
    }
    const startTs = Math.floor(dayStart.getTime() / 1000);
    const endTs = Math.floor(dayEnd.getTime() / 1000);
    const count = countInRange(events, startTs, endTs);
    const label = `${dayStart.toLocaleDateString([], {
      month: 'short',
      day: 'numeric',
    })} · ${count} × 429`;
    buckets.push({ row: 0, col, count, label });
  }
  return { rows: 1, cols, buckets, colLabels };
}

function buildLayout(windowType: string, events: QuotaEventRow[], today: Date): Layout {
  switch (windowType.toLowerCase()) {
    case '5h':
      return buildFiveHourLayout(events, today);
    case 'weekly':
      return buildWeeklyLayout(events, today);
    default:
      return buildDailyLayout(events, today);
  }
}

export function QuotaHeatmap({ provider, windowType, events }: Props) {
  const { t } = useTranslation();

  const providerEvents = useMemo(
    () => events.filter((e) => e.provider.toLowerCase() === provider.toLowerCase()),
    [events, provider],
  );

  // Recompute "today" once per mount.
  const today = useMemo(() => new Date(), []);
  const layout = useMemo(
    () => buildLayout(windowType, providerEvents, today),
    [windowType, providerEvents, today],
  );

  const totalInView = layout.buckets.reduce((acc, b) => acc + b.count, 0);

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <span className={styles.title}>
          <span className={styles.provider}>{provider}</span>
          <span className={styles.windowBadge}>{windowType}</span>
        </span>
        <span className={styles.summary}>
          {t('analytics.heatmap.total_in_view', { count: totalInView })}
        </span>
      </div>

      <div className={styles.gridWrap}>
        {layout.rowLabels && (
          <div className={styles.rowLabels}>
            {layout.rowLabels.map((lbl) => (
              <div key={lbl} className={styles.rowLabel}>
                {lbl}
              </div>
            ))}
          </div>
        )}
        <div
          className={styles.grid}
          style={{
            gridTemplateColumns: `repeat(${layout.cols}, var(--cell-size))`,
            gridTemplateRows: `repeat(${layout.rows}, var(--cell-size))`,
          }}
        >
          {layout.buckets.map((b, idx) => (
            <div
              key={idx}
              className={`${styles.cell} ${styles[`level${level(b.count)}`]}`}
              style={{ gridRow: b.row + 1, gridColumn: b.col + 1 }}
              title={b.label}
              aria-label={b.label}
            />
          ))}
        </div>
      </div>

      {layout.colLabels && layout.colLabels.length > 0 && (
        <div
          className={styles.colLabelRow}
          style={{
            gridTemplateColumns: `repeat(${layout.cols}, var(--cell-size))`,
            marginLeft: layout.rowLabels ? 'var(--row-label-w)' : 0,
          }}
        >
          {layout.colLabels.map((cl) => (
            <span
              key={cl.col}
              className={styles.colLabel}
              style={{ gridColumn: `${cl.col + 1} / span 1` }}
            >
              {cl.label}
            </span>
          ))}
        </div>
      )}

      <div className={styles.legend}>
        <span className={styles.legendLabel}>{t('analytics.heatmap.less')}</span>
        <div className={`${styles.cell} ${styles.level0}`} />
        <div className={`${styles.cell} ${styles.level1}`} />
        <div className={`${styles.cell} ${styles.level2}`} />
        <div className={`${styles.cell} ${styles.level3}`} />
        <div className={`${styles.cell} ${styles.level4}`} />
        <span className={styles.legendLabel}>{t('analytics.heatmap.more')}</span>
      </div>
    </div>
  );
}


