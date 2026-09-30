/** Pure rules of the statistics screen. */
import { parseDateInput, toDateInput } from "../lib/format";

export type Metric = "cost" | "tokens" | "calls";
export const METRICS: readonly Metric[] = ["cost", "tokens", "calls"];
export type BillingScope = "all" | "free" | "billable";
export type Granularity = "day" | "week" | "month";

export interface Metrics {
  recordCount: number;
  inputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  totalTokens: number;
  callCount: number;
  durationSeconds: number;
  avgLatencyMS: number;
  billedNanousd: number;
  billedUSD: number;
}

export interface TrendPoint extends Metrics {
  periodStart: string;
}

export interface ModelStat extends Metrics {
  platformModelName: string;
  trend: TrendPoint[];
}

export interface UserStat extends Metrics {
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  trend: TrendPoint[];
}

export interface Stats {
  section: string;
  range: { startDate: string; endDate: string; granularity: Granularity | (string & {}) };
  totals: Metrics;
  trend: TrendPoint[];
  topModels: ModelStat[];
  topUsers: UserStat[];
}

export type RangeProblem = "incomplete" | "invalid" | "tooLong";

export const MAX_DAYS = 366;
export const PRESETS = [7, 30, 90] as const;

/** The last `days` calendar days ending today (inclusive). */
export function presetRange(days: number, today: Date = new Date()): { start: string; end: string } {
  const end = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const start = new Date(end);
  start.setDate(start.getDate() - (days - 1));
  return { start: toDateInput(start), end: toDateInput(end) };
}

/** Inclusive number of days between two real dates. */
export function inclusiveDays(start: Date, end: Date): number {
  const a = Date.UTC(start.getFullYear(), start.getMonth(), start.getDate());
  const b = Date.UTC(end.getFullYear(), end.getMonth(), end.getDate());
  return Math.round((b - a) / 86_400_000) + 1;
}

export function validateRange(start: string, end: string): RangeProblem | null {
  if (!start.trim() || !end.trim()) return "incomplete";
  const a = parseDateInput(start);
  const b = parseDateInput(end);
  if (!a || !b || b < a) return "invalid";
  return inclusiveDays(a, b) > MAX_DAYS ? "tooLong" : null;
}

export interface Filters {
  subject: { kind: "all" } | { kind: "user"; id: number; label: string } | { kind: "group"; id: number };
  model: string;
  scope: BillingScope;
}

export const NO_FILTERS: Filters = { subject: { kind: "all" }, model: "", scope: "all" };

export function activeFilterCount(f: Filters): number {
  return Number(f.subject.kind === "user" ? f.subject.id > 0 : f.subject.kind === "group") + Number(f.model.trim() !== "") + Number(f.scope !== "all");
}

/** Choosing a user replaces a group and the other way round. */
export function withSubject(f: Filters, subject: Filters["subject"]): Filters {
  return { ...f, subject };
}

export interface QueryArgs {
  start: string;
  end: string;
  filters: Filters;
  section: "all" | "models" | "users";
  modelRankBy: Metric;
  userRankBy: Metric;
}

/** Exactly one subject filter is ever sent. */
export function buildQuery(a: QueryArgs): Record<string, string | number | undefined> {
  const q: Record<string, string | number | undefined> = {
    start_date: a.start,
    end_date: a.end,
    billing_scope: a.filters.scope,
    section: a.section,
    model_rank_by: a.modelRankBy,
    user_rank_by: a.userRankBy,
  };
  if (a.filters.subject.kind === "user" && a.filters.subject.id > 0) q.user_id = a.filters.subject.id;
  else if (a.filters.subject.kind === "group" && a.filters.subject.id > 0) q.permission_group_id = a.filters.subject.id;
  const model = a.filters.model.trim();
  if (model) q.platform_model_name = model;
  return q;
}

export function metricValue(m: Metrics, metric: Metric): number {
  return metric === "cost" ? m.billedUSD : metric === "tokens" ? m.totalTokens : m.callCount;
}

/** A key that changes whenever anything but the rank metrics changes. */
export function cacheKey(start: string, end: string, filters: Filters): string {
  return JSON.stringify([start, end, filters]);
}

export class RankCache<T> {
  private key = "";
  private store = new Map<string, T>();
  reset(key: string): void {
    if (key !== this.key) {
      this.key = key;
      this.store.clear();
    }
  }
  get(metric: Metric): T | undefined {
    return this.store.get(metric);
  }
  set(metric: Metric, value: T): void {
    this.store.set(metric, value);
  }
}

/**
 * Label of a trend bucket: days show the day, weeks and months show their
 * first day, clipped to the requested range.
 */
export function bucketLabel(periodStart: string, granularity: string, range: { startDate: string; endDate: string }, locale: string): string {
  // The server labels buckets with the calendar date of its own zone; reading
  // that date from the text keeps a browser in another zone from shifting it.
  const calendar = /^(\d{4}-\d{2}-\d{2})/.exec(periodStart.trim());
  const calendarDate = calendar?.[1];
  let shown = calendarDate ? parseDateInput(calendarDate) : undefined;
  if (!shown) {
    const d = new Date(periodStart);
    if (Number.isNaN(d.getTime())) return periodStart;
    shown = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  }
  const start = parseDateInput(range.startDate);
  if ((granularity === "week" || granularity === "month") && start && shown < start) shown = start;
  return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(shown);
}

export interface NiceAxis {
  max: number;
  ticks: number[];
}

/** A round upper bound and a few ticks for a value axis. */
export function niceAxis(maxValue: number, count = 4): NiceAxis {
  if (!Number.isFinite(maxValue) || maxValue <= 0) return { max: 1, ticks: [0, 1] };
  const raw = maxValue / count;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= raw) ?? 10 * pow;
  const max = Math.ceil(maxValue / step) * step;
  const ticks: number[] = [];
  for (let v = 0; v <= max + step / 2; v += step) ticks.push(Math.round(v * 1e9) / 1e9);
  return { max, ticks };
}
