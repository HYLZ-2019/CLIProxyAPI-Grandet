import { DAY_SECONDS } from './constants';
import type { UsageWindow } from './types';

export function formatNumber(n: number): string {
  if (n === 0) return '0';
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (Math.abs(n) >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (Math.abs(n) >= 1e3) return (n / 1e3).toFixed(1) + 'k';
  return String(Math.round(n * 100) / 100);
}

export function formatUSD(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) >= 1000) return '$' + formatNumber(n);
  if (Math.abs(n) >= 1) return '$' + n.toFixed(2);
  if (n === 0) return '$0';
  return '$' + n.toFixed(4);
}

export function formatPercent(n: number): string {
  if (!Number.isFinite(n)) return '—';
  return `${Math.round(n * 10) / 10}%`;
}

export function formatTimestamp(ts: number, window?: UsageWindow | null): string {
  const d = new Date(ts * 1000);
  const spanSeconds = window && window.to > window.from ? window.to - window.from : DAY_SECONDS;
  if (spanSeconds <= DAY_SECONDS) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function formatDateTime(ts: number): string {
  return ts > 0 ? new Date(ts * 1000).toLocaleString() : '—';
}

export function formatDateTimeLocal(ts: number): string {
  const d = new Date(ts * 1000);
  const local = new Date(d.getTime() - d.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

export function parseDateTimeLocal(value: string): number | null {
  const ms = new Date(value).getTime();
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : null;
}

export function formatBucketSize(seconds?: number): string {
  if (!seconds || seconds <= 0) return '—';
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}min`;
  return `${seconds}s`;
}
