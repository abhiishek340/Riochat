/**
 * The cost breakdown of a billed reply, read from the pricing snapshot the
 * server stores with it. Amounts are nano-USD (1 USD = 1e9); display converts
 * to the configured currency.
 */
import { isRecord } from "@/lib/utils/guards";
import type { BillingCostDTO } from "../types";

export const NANO = 1_000_000_000;

export interface Currency {
  code: "USD" | "CNY";
  /** USD -> display currency. */
  rate: number;
}

export const USD: Currency = { code: "USD", rate: 1 };

export function symbolOf(code: string): string {
  return code === "CNY" ? "¥" : "$";
}

/** `$0.003`, `$3.00`, `$0`: up to six decimals, trailing zeros trimmed down to two. */
export function formatMoney(nano: number, currency: Currency = USD): string {
  const value = (nano / NANO) * currency.rate;
  const sym = symbolOf(currency.code);
  if (!Number.isFinite(value) || value === 0) return `${sym}0`;
  const fixed = Math.abs(value).toFixed(6);
  const [whole, rawFrac = ""] = fixed.split(".");
  let frac = rawFrac.replace(/0+$/, "");
  while (frac.length < 2) frac += "0";
  const sign = value < 0 ? "-" : "";
  return `${sign}${sym}${whole}.${frac}`;
}

/** Unit price per million tokens: always at least two decimals. */
export function formatRate(nanoPerM: number, currency: Currency = USD): string {
  return formatMoney(nanoPerM, currency);
}

function num(rec: Record<string, unknown>, key: string): number {
  const v = rec[key];
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

function text(rec: Record<string, unknown>, key: string): string {
  const v = rec[key];
  return typeof v === "string" ? v : "";
}

export type LineKind = "input" | "output" | "cacheRead" | "cacheWrite" | "cacheWrite5m" | "cacheWrite1h" | "call" | "duration";

export interface BillingLine {
  kind: LineKind;
  tokens?: number;
  seconds?: number;
  count?: number;
  rateNano: number;
  amountNano: number;
}

export interface ServiceItemLine {
  name: string;
  code: string;
  count: number;
  amountNano: number;
  tokens: number;
}

export interface Breakdown {
  mode: string;
  lines: BillingLine[];
  items: ServiceItemLine[];
  totalNano: number;
  free: boolean;
  rateMultiplier: number;
  cacheTimeout: string;
  fastMode: boolean;
  speed: string;
  serviceTier: string;
  schedule: { name: string; percent: number } | null;
  tier: { from: number; upTo: number | null } | null;
  billedReason: string;
  hasMixedCache: boolean;
}

export interface UsageTokens {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
}

export function parseSnapshot(json: string | undefined): Record<string, unknown> | null {
  if (!json) return null;
  try {
    const parsed: unknown = JSON.parse(json);
    return isRecord(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

/** Builds the breakdown; null when the snapshot cannot be read. */
export function buildBreakdown(billing: BillingCostDTO | null | undefined, usage: UsageTokens): Breakdown | null {
  if (!billing) return null;
  const snap = parseSnapshot(billing.pricingSnapshotJSON);
  if (!snap) return null;
  const mode = text(snap, "pricing_mode") || "token";
  const lines: BillingLine[] = [];
  const multiplier = num(snap, "rate_multiplier") || 1;
  const t5 = num(snap, "cache_write_5m_tokens");
  const t1 = num(snap, "cache_write_1h_tokens");
  const mixed = t5 > 0 && t1 > 0;

  if (mode === "call") {
    lines.push({ kind: "call", count: 1, rateNano: num(snap, "call_nanousd_per_call"), amountNano: num(snap, "call_billed_nanousd") });
  } else if (mode === "duration") {
    lines.push({
      kind: "duration",
      seconds: num(snap, "duration_seconds"),
      rateNano: num(snap, "duration_nanousd_per_second"),
      amountNano: num(snap, "duration_billed_nanousd"),
    });
  } else {
    const add = (kind: LineKind, tokens: number, rateKey: string, billedKey: string) => {
      const amount = num(snap, billedKey);
      if (tokens <= 0 && amount <= 0) return;
      lines.push({ kind, tokens, rateNano: num(snap, rateKey), amountNano: amount });
    };
    add("input", usage.input, "input_nanousd_per_m_tokens", "input_billed_nanousd");
    add("output", usage.output + usage.reasoning, "output_nanousd_per_m_tokens", "output_billed_nanousd");
    add("cacheRead", usage.cacheRead, "cache_read_nanousd_per_m_tokens", "cache_read_billed_nanousd");
    if (mixed) {
      add("cacheWrite5m", t5, "cache_write_5m_nanousd_per_m_tokens", "cache_write_5m_billed_nanousd");
      add("cacheWrite1h", t1, "cache_write_1h_nanousd_per_m_tokens", "cache_write_1h_billed_nanousd");
    } else {
      const kind: LineKind = t5 > 0 ? "cacheWrite5m" : t1 > 0 ? "cacheWrite1h" : "cacheWrite";
      add(kind, usage.cacheWrite, "cache_write_nanousd_per_m_tokens", "cache_write_billed_nanousd");
    }
  }

  const items: ServiceItemLine[] = [];
  const rawItems = snap.service_items;
  if (Array.isArray(rawItems)) {
    for (const raw of rawItems) {
      if (!isRecord(raw)) continue;
      const amount = num(raw, "billed_nanousd");
      items.push({
        name: text(raw, "service_name") || text(raw, "service_code"),
        code: text(raw, "service_code"),
        count: num(raw, "call_count") || 1,
        amountNano: amount,
        tokens: num(raw, "input_tokens") + num(raw, "output_tokens"),
      });
    }
  }

  const fromTokens = num(snap, "tiered_from_tokens");
  const upTo = snap.tiered_up_to_tokens;
  const tiered = typeof upTo === "number" || fromTokens > 0;
  const schedulePercent = num(snap, "schedule_rate_percent");
  const scheduleName = text(snap, "schedule_period_name");

  const itemsTotal = items.reduce((sum, it) => sum + it.amountNano, 0);
  const totalNano = billing.billedNanousd > 0 ? billing.billedNanousd : lines.reduce((s, l) => s + l.amountNano, 0) + itemsTotal;

  return {
    mode: tiered && mode === "token" ? "tiered" : mode,
    lines,
    items,
    totalNano,
    free: snap.is_free_model === true,
    rateMultiplier: multiplier,
    cacheTimeout: text(snap, "cache_timeout"),
    fastMode: snap.fast_mode === true,
    speed: text(snap, "billing_speed"),
    serviceTier: text(snap, "billing_service_tier"),
    schedule: scheduleName && schedulePercent && schedulePercent !== 100 ? { name: scheduleName, percent: schedulePercent } : null,
    tier: tiered ? { from: fromTokens, upTo: typeof upTo === "number" ? upTo : null } : null,
    billedReason: text(snap, "billed_reason"),
    hasMixedCache: mixed,
  };
}

/** Free-model note only when the whole bill is zero. */
export function showsFreeNote(b: Breakdown): boolean {
  return b.free && b.totalNano === 0 && b.items.every((i) => i.amountNano === 0);
}

/** `1,000 tokens * $3.00 / 1M = $0.003` numeric parts for a token line. */
export function tokenLineParts(line: BillingLine, currency: Currency, locale = "en-US"): { tokens: string; rate: string; amount: string } {
  return {
    tokens: new Intl.NumberFormat(locale).format(line.tokens ?? 0),
    rate: formatRate(line.rateNano, currency),
    amount: formatMoney(line.amountNano, currency),
  };
}

/** Whether the cost badge is shown at all. */
export function shouldShowCost(billing: BillingCostDTO | null | undefined, billingMode: string): boolean {
  if (!billing) return false;
  if (billingMode === "self" || billing.billingMode === "self") return false;
  return true;
}
