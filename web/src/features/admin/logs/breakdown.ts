/** Reads a call log's pricing snapshot into rows the cost tooltip can show. */
import { isPlainObject } from "../lib/json";

export type BreakdownKind = "input" | "cacheRead" | "cacheWrite" | "cacheWrite5m" | "cacheWrite1h" | "output" | "call" | "duration" | "service";

export interface BreakdownRow {
  kind: BreakdownKind;
  /** Tokens, calls or seconds the line was priced on, when known. */
  quantity?: number;
  /** USD per million tokens, per call or per second. */
  rateUSD?: number;
  billedUSD: number;
  name?: string;
}

export interface Breakdown {
  pricingMode: string;
  free: boolean;
  rows: BreakdownRow[];
  rateMultiplier: number | null;
  schedule: { name: string; percent: number } | null;
  tier: { from: number; upTo: number | null } | null;
  fastMode: boolean;
  serviceTier: string;
  totalUSD: number;
}

const NANO = 1e9;

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function usd(nano: unknown): number {
  return num(nano) / NANO;
}

export function parseSnapshot(text: string | null | undefined): Record<string, unknown> | null {
  if (!text) return null;
  try {
    const value: unknown = JSON.parse(text);
    return isPlainObject(value) ? value : null;
  } catch {
    return null;
  }
}

export interface UsageCounts {
  inputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheWrite5mTokens: number;
  cacheWrite1hTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  callCount: number;
  durationSeconds: number;
}

/** Billable output = output + reasoning; total = input + cache read + cache write + billable output. */
export function billableOutput(c: Pick<UsageCounts, "outputTokens" | "reasoningTokens">): number {
  return c.outputTokens + c.reasoningTokens;
}

export function totalTokens(c: Pick<UsageCounts, "inputTokens" | "cacheReadTokens" | "cacheWriteTokens" | "outputTokens" | "reasoningTokens">): number {
  return c.inputTokens + c.cacheReadTokens + c.cacheWriteTokens + billableOutput(c);
}

export function breakdownOf(snapshot: string | null | undefined, counts: UsageCounts, billedUSD: number): Breakdown | null {
  const snap = parseSnapshot(snapshot);
  if (!snap) return null;
  const rows: BreakdownRow[] = [];
  const add = (row: BreakdownRow) => {
    if (row.billedUSD > 0 || (row.rateUSD ?? 0) > 0) rows.push(row);
  };
  add({ kind: "input", quantity: counts.inputTokens, rateUSD: usd(snap.input_nanousd_per_m_tokens), billedUSD: usd(snap.input_billed_nanousd) });
  add({ kind: "cacheRead", quantity: counts.cacheReadTokens, rateUSD: usd(snap.cache_read_nanousd_per_m_tokens), billedUSD: usd(snap.cache_read_billed_nanousd) });
  const split5 = num(snap.cache_write_5m_tokens);
  const split1h = num(snap.cache_write_1h_tokens);
  if (split5 > 0 || split1h > 0) {
    add({ kind: "cacheWrite5m", quantity: split5, rateUSD: usd(snap.cache_write_5m_nanousd_per_m_tokens), billedUSD: usd(snap.cache_write_5m_billed_nanousd) });
    add({ kind: "cacheWrite1h", quantity: split1h, rateUSD: usd(snap.cache_write_1h_nanousd_per_m_tokens), billedUSD: usd(snap.cache_write_1h_billed_nanousd) });
  } else {
    add({ kind: "cacheWrite", quantity: counts.cacheWriteTokens, rateUSD: usd(snap.cache_write_nanousd_per_m_tokens), billedUSD: usd(snap.cache_write_billed_nanousd) });
  }
  add({ kind: "output", quantity: billableOutput(counts), rateUSD: usd(snap.output_nanousd_per_m_tokens), billedUSD: usd(snap.output_billed_nanousd) });
  add({ kind: "call", quantity: counts.callCount, rateUSD: usd(snap.call_nanousd_per_call), billedUSD: usd(snap.call_billed_nanousd) });
  add({ kind: "duration", quantity: counts.durationSeconds, rateUSD: usd(snap.duration_nanousd_per_second), billedUSD: usd(snap.duration_billed_nanousd) });
  if (Array.isArray(snap.service_items)) {
    for (const item of snap.service_items) {
      if (!isPlainObject(item)) continue;
      const billed = usd(item.billed_nanousd ?? item.total_billed_nanousd);
      if (billed > 0) rows.push({ kind: "service", name: typeof item.name === "string" ? item.name : typeof item.code === "string" ? item.code : undefined, quantity: num(item.call_count), billedUSD: billed });
    }
  }
  const scheduleName = typeof snap.schedule_period_name === "string" ? snap.schedule_period_name : "";
  const schedulePercent = num(snap.schedule_rate_percent);
  const upTo = snap.tiered_up_to_tokens;
  return {
    pricingMode: typeof snap.pricing_mode === "string" ? snap.pricing_mode : "",
    free: snap.is_free_model === true,
    rows,
    rateMultiplier: typeof snap.rate_multiplier === "number" ? snap.rate_multiplier : null,
    schedule: scheduleName || schedulePercent > 0 ? { name: scheduleName, percent: schedulePercent } : null,
    tier: snap.pricing_mode === "tiered" ? { from: num(snap.tiered_from_tokens), upTo: typeof upTo === "number" ? upTo : null } : null,
    fastMode: snap.fast_mode === true,
    serviceTier: typeof snap.billing_service_tier === "string" ? snap.billing_service_tier : "",
    totalUSD: billedUSD,
  };
}

/** The raw usage object the upstream reported, for the detail sheet. */
export function upstreamUsageOf(snapshot: string | null | undefined): unknown {
  const snap = parseSnapshot(snapshot);
  return snap && "upstream_usage" in snap ? snap.upstream_usage : undefined;
}
