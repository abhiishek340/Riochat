/** Usage charts and log: aggregation and grouping rules. */
import type { DaySummary, LedgerItem, Metrics, ModelDay, MonthSummary } from "./types";
import { parseSnapshot, serviceItemsOf } from "./billing-breakdown";

export const MAX_MODEL_SERIES = 10;
export const OTHER_SERIES = "__other__";

/** Weighted mean over entries that have both a positive latency and a positive record count. */
export function averageLatency(entries: readonly Pick<Metrics, "avgLatencyMS" | "recordCount">[]): number {
  let sum = 0;
  let n = 0;
  for (const e of entries) {
    if (e.avgLatencyMS > 0 && e.recordCount > 0) {
      sum += e.avgLatencyMS * e.recordCount;
      n += e.recordCount;
    }
  }
  return n > 0 ? Math.round(sum / n) : 0;
}

export function sumMetrics(entries: readonly Metrics[]): { cost: number; tokens: number; calls: number; latency: number } {
  return {
    cost: entries.reduce((s, e) => s + e.billedUSD, 0),
    tokens: entries.reduce((s, e) => s + e.totalTokens, 0),
    calls: entries.reduce((s, e) => s + e.callCount, 0),
    latency: averageLatency(entries),
  };
}

/** Drops days after today (the client's local calendar) and sorts ascending by date. */
export function visibleDays(days: readonly DaySummary[], now: Date = new Date()): DaySummary[] {
  const p = (n: number) => String(n).padStart(2, "0");
  const today = `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`;
  return days.filter((d) => d.usageDate.slice(0, 10) <= today).sort((a, b) => a.usageDate.localeCompare(b.usageDate));
}

export interface Series {
  key: string;
  /** Model name, or OTHER_SERIES. */
  label: string;
  total: number;
}

/**
 * Up to ten models by total tokens in the window (ties broken by name); every
 * other model is merged into one "other" series. Empty when no day has a
 * model breakdown.
 */
export function modelSeries(days: readonly DaySummary[]): Series[] {
  const totals = new Map<string, number>();
  for (const d of days) for (const m of d.models) totals.set(m.platformModelName, (totals.get(m.platformModelName) ?? 0) + m.totalTokens);
  const ranked = [...totals].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const top = ranked.slice(0, MAX_MODEL_SERIES).map(([label, total]) => ({ key: label, label, total }));
  const rest = ranked.slice(MAX_MODEL_SERIES).reduce((s, [, t]) => s + t, 0);
  return ranked.length > MAX_MODEL_SERIES ? [...top, { key: OTHER_SERIES, label: OTHER_SERIES, total: rest }] : top;
}

/** Which series a model belongs to. */
export function seriesKeyOf(model: string, series: readonly Series[]): string {
  return series.some((s) => s.key === model) ? model : OTHER_SERIES;
}

/** Tokens of one day per series key. */
export function dayTokensBySeries(day: DaySummary, series: readonly Series[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const m of day.models) {
    const key = seriesKeyOf(m.platformModelName, series);
    out.set(key, (out.get(key) ?? 0) + m.totalTokens);
  }
  return out;
}

/** Latency of a day recomputed over the visible series only. */
export function dayLatencyFor(day: DaySummary, series: readonly Series[], hidden: ReadonlySet<string>): number {
  const visible: ModelDay[] = day.models.filter((m) => !hidden.has(seriesKeyOf(m.platformModelName, series)));
  return averageLatency(visible);
}

export function hasAnyUsage(entries: readonly Metrics[]): boolean {
  return entries.some((e) => e.billedUSD > 0 || e.totalTokens > 0 || e.callCount > 0 || e.recordCount > 0);
}

export function monthSorted(months: readonly MonthSummary[]): MonthSummary[] {
  return [...months].sort((a, b) => a.monthStartAt.localeCompare(b.monthStartAt));
}

// ---- usage log -----------------------------------------------------------

export interface LogRow {
  item: LedgerItem;
  /** Service-only entries folded into this row. */
  attached: LedgerItem[];
}

/**
 * An entry that only records per-service charges: its snapshot says so, or
 * (when the flag is absent) it lists service items while the main model
 * charge is zero.
 */
export function isServiceOnly(item: LedgerItem): boolean {
  const snap = parseSnapshot(item.pricingSnapshotJSON);
  if (typeof snap.service_only === "boolean") return snap.service_only;
  if (serviceItemsOf(snap).length === 0) return false;
  const main = ["input", "cache_read", "cache_write", "output", "call", "duration"].reduce(
    (s, k) => s + (typeof snap[`${k}_billed_nanousd`] === "number" ? (snap[`${k}_billed_nanousd`] as number) : 0),
    0,
  );
  return main === 0;
}

function timeOf(item: LedgerItem): number {
  const t = Date.parse(item.billingAt || item.createdAt);
  return Number.isNaN(t) ? 0 : t;
}

/**
 * Hides service-only entries and attaches each to the nearest-in-time other
 * entry of the same conversation; entries with no such row are dropped.
 */
export function groupLedger(items: readonly LedgerItem[]): LogRow[] {
  const rows: LogRow[] = [];
  const services: LedgerItem[] = [];
  for (const item of items) {
    if (isServiceOnly(item)) services.push(item);
    else rows.push({ item, attached: [] });
  }
  for (const svc of services) {
    let best: LogRow | undefined;
    let bestGap = Number.POSITIVE_INFINITY;
    for (const row of rows) {
      if (row.item.conversationID !== svc.conversationID) continue;
      const gap = Math.abs(timeOf(row.item) - timeOf(svc));
      if (gap < bestGap) {
        best = row;
        bestGap = gap;
      }
    }
    best?.attached.push(svc);
  }
  return rows;
}

export const LOG_SORTS = ["newest", "oldest", "tokens_desc", "cost_desc", "latency_desc"] as const;
export type LogSort = (typeof LOG_SORTS)[number];
