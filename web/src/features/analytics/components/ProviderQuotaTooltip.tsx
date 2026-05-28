import { formatBucketSize, formatDateTime, formatPercent, formatUSD } from '../formatters';
import { findNearestQuotaPoint, isQuotaBucketPoint } from '../transforms';
import type { ProviderQuotaTooltipPayload, ProviderQuotaTooltipPoint } from '../types';
import styles from '../Analytics.module.scss';

export function ProviderQuotaTooltip(props: {
  active?: boolean;
  label?: string | number;
  payload?: ProviderQuotaTooltipPayload[];
  data: ProviderQuotaTooltipPoint[];
  bucketSizeLabel: string;
  bucketUSDLabel: string;
  quotaLabel: string;
  cumulativeLabel: string;
  eventsLabel: string;
}) {
  if (!props.active) return null;

  const linePayload = props.payload?.find(
    (item) =>
      (item.dataKey === 'quota_used_percent' || item.dataKey === 'cliproxy_cumulative_usd') &&
      isQuotaBucketPoint(item.payload),
  )?.payload;
  const labelTS = Number(linePayload?.x_ts ?? props.label ?? 0);
  const point = linePayload ?? findNearestQuotaPoint(props.data, labelTS);
  const ts = Number(point?.x_ts ?? labelTS);
  if (!Number.isFinite(ts) || ts <= 0) return null;

  return (
    <div className={styles.tooltipBox}>
      <div className={styles.tooltipTitle}>{formatDateTime(ts)}</div>
      <div>
        {props.bucketSizeLabel}: {formatBucketSize(point?.bucket_seconds)}
      </div>
      <div>
        {props.quotaLabel}: {formatPercent(point?.quota_used_percent ?? 0)}
      </div>
      <div>
        {props.bucketUSDLabel}: {formatUSD(point?.cliproxy_hour_usd ?? 0)}
      </div>
      <div>
        {props.cumulativeLabel}: {formatUSD(point?.cliproxy_cumulative_usd ?? 0)}
      </div>
      {(point?.quota_events_count ?? 0) > 0 && (
        <div>
          {props.eventsLabel}: {point?.quota_events_count}
        </div>
      )}
    </div>
  );
}
